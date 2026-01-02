// Copyright 2025 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/logstreamapi"
	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// processIncomingContainerLogRequest handles container log requests from Principal
func (a *Agent) processIncomingContainerLogRequest(ev *event.Event) error {
	logReq, err := ev.ContainerLogRequest()
	if err != nil {
		return err
	}

	logCtx := log().WithFields(logrus.Fields{
		"uuid":      logReq.UUID,
		"namespace": logReq.Namespace,
		"pod":       logReq.PodName,
		"container": logReq.Container,
		"follow":    logReq.Follow,
	})

	err = a.startLogStreamIfNew(logReq, logCtx)
	if err != nil {
		logCtx.WithError(err).Error("Log processing failed")
		return err
	}

	return nil
}

// startLogStreamIfNew manages log streaming with duplicate detection
func (a *Agent) startLogStreamIfNew(logReq *event.ContainerLogRequest, logCtx *logrus.Entry) error {
	a.inflightMu.Lock()
	if a.inflightLogs == nil {
		a.inflightLogs = make(map[string]struct{})
	}
	if _, dup := a.inflightLogs[logReq.UUID]; dup {
		a.inflightMu.Unlock()
		logCtx.Warn("duplicate log request; already streaming")
		return nil
	}
	ctx, cancel := context.WithCancel(a.context)
	a.inflightLogs[logReq.UUID] = struct{}{}
	a.inflightMu.Unlock()

	cleanup := func() {
		cancel()
		a.inflightMu.Lock()
		delete(a.inflightLogs, logReq.UUID)
		a.inflightMu.Unlock()
	}

	logCtx.Info("Processing log request")

	if logReq.Follow {
		// Handle live logs with early ACK
		return a.handleLiveStreaming(ctx, logReq, logCtx, cleanup)
	}
	defer cleanup()
	// Handle static logs with completion ACK
	return a.handleStaticLogs(ctx, logReq, logCtx)
}

// handleStaticLogs handles static log requests (follow=false)
func (a *Agent) handleStaticLogs(ctx context.Context, logReq *event.ContainerLogRequest, logCtx *logrus.Entry) error {
	// Create gRPC stream
	stream, err := a.createLogStream(ctx)
	if err != nil {
		return err
	}
	err = stream.Send(&logstreamapi.LogStreamData{
		RequestUuid: logReq.UUID,
		Data:        []byte{},
		Eof:         false,
	})
	if err != nil {
		return err
	}
	// Create Kubernetes log stream
	rc, err := a.createKubernetesLogStream(ctx, logReq)
	if err != nil {
		_ = stream.Send(&logstreamapi.LogStreamData{RequestUuid: logReq.UUID, Eof: true, Error: err.Error()})
		_, _ = stream.CloseAndRecv()
		return err
	}
	err = a.streamLogsToCompletion(ctx, stream, rc, logReq, logCtx)
	if err != nil {
		// Stop immediately on intentional server stops or auth issues
		switch status.Code(err) {
		case codes.Canceled, codes.NotFound:
			// Intentional stop (UI gone / request not found) -> do not retry
			logCtx.WithError(err).Info("Log stream ended")
			return nil
		case codes.Unauthenticated, codes.PermissionDenied:
			logCtx.WithError(err).Warn("Auth/permission failure")
			a.SetConnected(false)
			return err
		default:
			logCtx.WithError(err).Warn("Stream error")
		}
	}
	return err
}

// handleLiveStreaming handles live log requests (follow=true) with early ACK and resume capability
func (a *Agent) handleLiveStreaming(ctx context.Context, logReq *event.ContainerLogRequest, logCtx *logrus.Entry, cleanup func()) error {
	// Start streaming with resume capability in background goroutine
	go func() {
		defer cleanup()
		defer func() {
			if r := recover(); r != nil {
				logCtx.WithField("panic", r).Error("Panic in live log streaming")
			}
		}()
		a.streamLogsWithResume(ctx, logReq, logCtx)
	}()
	// Return success immediately - this sends the ACK to Principal
	return nil
}

// createLogStream creates a gRPC LogStream to the principal
func (a *Agent) createLogStream(ctx context.Context) (logstreamapi.LogStreamService_StreamLogsClient, error) {
	conn := a.remote.Conn()
	if conn == nil {
		return nil, fmt.Errorf("gRPC connection is nil")
	}
	client := logstreamapi.NewLogStreamServiceClient(conn)
	return client.StreamLogs(ctx)
}

// createKubernetesLogStream creates a Kubernetes log stream
func (a *Agent) createKubernetesLogStream(ctx context.Context, logReq *event.ContainerLogRequest) (io.ReadCloser, error) {
	logOptions := &corev1.PodLogOptions{
		Container:                    logReq.Container,
		Follow:                       logReq.Follow,
		Timestamps:                   true,
		Previous:                     logReq.Previous,
		InsecureSkipTLSVerifyBackend: logReq.InsecureSkipTLSVerifyBackend,
		TailLines:                    logReq.TailLines,
		SinceSeconds:                 logReq.SinceSeconds,
		LimitBytes:                   logReq.LimitBytes,
	}
	// Handle SinceTime if provided
	if logReq.SinceTime != "" {
		if sinceTime, err := time.Parse(time.RFC3339, logReq.SinceTime); err == nil {
			mt := v1.NewTime(sinceTime)
			logOptions.SinceTime = &mt
		}
	}
	request := a.kubeClient.Clientset.CoreV1().Pods(logReq.Namespace).GetLogs(logReq.PodName, logOptions)
	return request.Stream(ctx)
}

// streamLogsToCompletion streams ALL available (static) logs from k8s to the principal.
// It flushes raw data without processing, using chunk size (64KB) or time-based flushing.
func (a *Agent) streamLogsToCompletion(
	ctx context.Context,
	stream logstreamapi.LogStreamService_StreamLogsClient,
	rc io.ReadCloser,
	logReq *event.ContainerLogRequest,
	logCtx *logrus.Entry,
) error {
	const chunkMax = 64 * 1024
	defer rc.Close()
	readBuf := make([]byte, chunkMax)

	for {
		// Respect cancellations before attempting a potentially blocking read
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-stream.Context().Done():
			return stream.Context().Err()
		default:
		}

		n, err := rc.Read(readBuf)

		if n > 0 {
			if sendErr := stream.Send(&logstreamapi.LogStreamData{
				RequestUuid: logReq.UUID,
				Data:        readBuf[:n],
			}); sendErr != nil {
				logCtx.WithError(sendErr).Warn("Send failed")
				if _, closedErr := stream.CloseAndRecv(); closedErr != nil {
					return closedErr
				}
				return sendErr
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				logCtx.Info("Static log stream reached EOF")
				// IMPORTANT: don't ignore EOF send errors. If this fails, the principal will
				// not signal completion (it only completes on receiving Eof=true) and the
				// HTTP handler may hit "Static logs timeout" even though we read all logs.
				if sendErr := stream.Send(&logstreamapi.LogStreamData{RequestUuid: logReq.UUID, Eof: true}); sendErr != nil {
					logCtx.WithError(sendErr).Warn("Failed to send EOF frame")
					if _, closedErr := stream.CloseAndRecv(); closedErr != nil {
						return closedErr
					}
					return sendErr
				}
				// IMPORTANT: Must call CloseAndRecv to properly close the client-streaming RPC.
				// This ensures all messages are flushed and the server receives the final response.
				if _, closeErr := stream.CloseAndRecv(); closeErr != nil {
					logCtx.WithError(closeErr).Warn("Failed to close stream after EOF")
					return closeErr
				}
				return nil
			}
			logCtx.WithError(err).Error("Error reading log stream")
			_ = stream.Send(&logstreamapi.LogStreamData{RequestUuid: logReq.UUID, Error: "log stream read failed"})
			_, _ = stream.CloseAndRecv()
			return err
		}
	}
}

func (a *Agent) streamLogsWithResume(ctx context.Context, logReq *event.ContainerLogRequest, logCtx *logrus.Entry) {
	const (
		waitForReconnect = 10 * time.Second // how long we poll IsConnected() after Unauthenticated
		pollEvery        = 1 * time.Second
	)
	var lastTimestamp *time.Time
	// Configure exponential backoff with jitter
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 200 * time.Millisecond
	b.Multiplier = 2.0
	b.MaxInterval = 5 * time.Second
	b.MaxElapsedTime = 30 * time.Second
	bo := backoff.WithContext(b, ctx)

	for {
		// Build resume request
		resumeReq := *logReq
		if lastTimestamp != nil {
			t := lastTimestamp.Add(-100 * time.Millisecond)
			resumeReq.SinceTime = t.Format(time.RFC3339)
		}

		// One attempt to create + stream
		attempt := func() (err error) {
			stream, err := a.createLogStream(ctx)
			if err != nil {
				return err
			}
			// Send initial empty message to establish the stream connection
			// This allows the principal to acknowledge the stream and prepare for log data
			err = stream.Send(&logstreamapi.LogStreamData{
				RequestUuid: logReq.UUID,
				Data:        []byte{},
				Eof:         false,
			})
			if err != nil {
				_, err = stream.CloseAndRecv()
				return err
			}
			rc, err := a.createKubernetesLogStream(ctx, &resumeReq)
			if err != nil {
				_ = stream.Send(&logstreamapi.LogStreamData{RequestUuid: logReq.UUID, Eof: true, Error: err.Error()})
				_, _ = stream.CloseAndRecv()
				return err
			}
			newLastTimestamp, err := a.streamLogs(ctx, stream, rc, &resumeReq, logCtx)
			if newLastTimestamp != nil {
				lastTimestamp = newLastTimestamp
			}
			return err
		}
		err := attempt()
		if err == nil {
			return
		}

		switch status.Code(err) {
		case codes.Canceled, codes.NotFound:
			// Intentional stop (UI gone / request not found) -> do not retry
			logCtx.WithError(err).Info("Log stream ended")
			return
		case codes.Unauthenticated, codes.PermissionDenied:
			// Do NOT backoff-retry; instead block waiting for connector to become connected.
			logCtx.WithError(err).Warn("Auth/permission failure")
			a.SetConnected(false)

			waitCtx, cancel := context.WithTimeout(ctx, waitForReconnect)
			t := time.NewTicker(pollEvery)

			reconnected := false
			for !reconnected {
				select {
				case <-waitCtx.Done():
					cancel()
					t.Stop()
					return
				case <-t.C:
					if a.IsConnected() {
						reconnected = true
					}
				}
			}
			cancel()
			t.Stop()
			b.Reset()
			continue
		default:
			// Transient or unknown -> exponential backoff
			d := bo.NextBackOff()
			if d == backoff.Stop {
				logCtx.WithError(err).Error("Backoff stopped")
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
			}
		}
	}
}

// streamLogs streams logs until the context is done, returning the last seen timestamp.
// It flushes raw data, using chunk size 64KB
// Timestamps are extracted from raw lines for retry capability.
// If an error occurs during send, it attempts to close the stream and propagate
// the appropriate error back to the caller for retry or termination.
func (a *Agent) streamLogs(ctx context.Context, stream logstreamapi.LogStreamService_StreamLogsClient, rc io.ReadCloser, logReq *event.ContainerLogRequest, logCtx *logrus.Entry) (*time.Time, error) {
	const chunkMax = 64 * 1024 // 64KB chunks
	var lastTimestamp *time.Time
	readBuf := make([]byte, chunkMax)
	defer rc.Close()

	for {
		select {
		case <-ctx.Done():
			return lastTimestamp, ctx.Err()
		case <-stream.Context().Done():
			return lastTimestamp, stream.Context().Err()
		default:
		}
		n, err := rc.Read(readBuf)
		if n > 0 {
			b := readBuf[:n]
			// Extract timestamp from the last complete line in the buffer to enable resume capability.
			if end := bytes.LastIndexByte(b, '\n'); end >= 0 {
				start := bytes.LastIndexByte(b[:end], '\n') + 1
				line := b[start:end]
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				if ts := extractTimestamp(string(line)); ts != nil {
					lastTimestamp = ts
				}
			}
			if sendErr := stream.Send(&logstreamapi.LogStreamData{
				RequestUuid: logReq.UUID,
				Data:        readBuf[:n],
			}); sendErr != nil {
				// For client side streaming, the actual gRPC error may only surface
				// after stream closure. Attempt to close and return the final error.
				if _, closedErr := stream.CloseAndRecv(); closedErr != nil {
					return lastTimestamp, closedErr
				}
				return lastTimestamp, sendErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				logCtx.WithError(err).Info("Log stream ended")
				_ = stream.Send(&logstreamapi.LogStreamData{RequestUuid: logReq.UUID, Eof: true})
				_, _ = stream.CloseAndRecv()
				return lastTimestamp, nil
			}
			_ = stream.Send(&logstreamapi.LogStreamData{RequestUuid: logReq.UUID, Error: err.Error()})
			_, _ = stream.CloseAndRecv()
			return lastTimestamp, err
		}
	}
}

// extractTimestamp extracts timestamp from a log line for resume capability
func extractTimestamp(line string) *time.Time {
	if len(line) < 20 { // "2006-01-02T15:04:05Z" is 20 chars
		return nil
	}
	// Grab the first token (up to whitespace). k8s puts a space after the timestamp.
	space := strings.IndexAny(line, " \t")
	if space == -1 {
		// Fall back: try whole line (cheap fast-fail)
		space = len(line)
	}
	// Guard against absurdly long "tokens"
	const maxTSLen = 40 // a tad higher than needed; RFC3339Nano+offset is 35
	if space > maxTSLen {
		return nil
	}
	token := line[:space]

	// Try the common RFC3339 flavors (covers with/without fractional seconds and offsets)
	if ts, err := time.Parse(time.RFC3339Nano, token); err == nil {
		return &ts
	}
	if ts, err := time.Parse(time.RFC3339, token); err == nil {
		return &ts
	}
	return nil
}
