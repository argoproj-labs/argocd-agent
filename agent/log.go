package agent

import (
	"bufio"
	"context"
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
		a.inflightLogs = make(map[string]context.CancelFunc)
	}
	if _, dup := a.inflightLogs[logReq.UUID]; dup {
		a.inflightMu.Unlock()
		logCtx.WithField("request_uuid", logReq.UUID).Warn("duplicate log request; already streaming")
		return nil
	}
	ctx, cancel := context.WithCancel(a.context)
	a.inflightLogs[logReq.UUID] = cancel
	a.inflightMu.Unlock()

	defer func() {
		a.inflightMu.Lock()
		delete(a.inflightLogs, logReq.UUID)
		a.inflightMu.Unlock()
	}()

	if logReq.Follow {
		// Handle live logs with early ACK
		return a.handleLiveStreaming(ctx, logReq, logCtx)
	}

	// Handle static logs with completion ACK
	return a.handleStaticLogs(ctx, logReq, logCtx)
}

// handleStaticLogs handles static log requests (follow=false) with completion ACK
func (a *Agent) handleStaticLogs(ctx context.Context, logReq *event.ContainerLogRequest, logCtx *logrus.Entry) error {
	// Create gRPC stream
	stream, err := a.createLogStream(ctx)
	if err != nil {
		return err
	}

	err = stream.Send(&logstreamapi.LogStreamData{
		RequestUuid: logReq.UUID,
		Data:        "",
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
	defer rc.Close()

	err = a.streamLogsToCompletion(ctx, stream, rc, logReq, logCtx)

	// Give the server time to process the EOF message
	time.Sleep(100 * time.Millisecond)

	if _, cerr := stream.CloseAndRecv(); cerr != nil {
		err = cerr
	}

	if err != nil {
		// Stop immediately on intentional server stops or auth issues
		switch status.Code(err) {
		case codes.Canceled, codes.NotFound:
			return err
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
func (a *Agent) handleLiveStreaming(ctx context.Context, logReq *event.ContainerLogRequest, logCtx *logrus.Entry) error {
	// Start streaming with resume capability in background goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logCtx.WithField("panic", r).Error("Panic in live log streaming")
			}
		}()

		streamCtx := logCtx.WithField("mode", "live_streaming")
		a.streamLogsWithResume(ctx, logReq, streamCtx)
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

// createFlushFunction creates a reusable flush function for both static and live streaming
func (a *Agent) createFlushFunction(stream logstreamapi.LogStreamService_StreamLogsClient, requestUUID string, buf *[]byte, logCtx *logrus.Entry) func(string) error {
	return func(reason string) error {
		if len(*buf) == 0 {
			return nil
		}

		payload := string(*buf)
		if err := stream.Send(&logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Data:        payload,
		}); err != nil {
			logCtx.WithFields(logrus.Fields{
				"reason": reason,
				"bytes":  len(*buf),
				"error":  err.Error(),
			}).Warn("Send failed")
			return err
		}

		*buf = (*buf)[:0] // Reset buffer
		return nil
	}
}

// streamLogsToCompletion streams ALL available (static) logs from k8s to the principal.
// It batches lines into ~64KiB chunks and flushes every ~50ms.
func (a *Agent) streamLogsToCompletion(
	ctx context.Context,
	stream logstreamapi.LogStreamService_StreamLogsClient,
	rc io.ReadCloser,
	logReq *event.ContainerLogRequest,
	logCtx *logrus.Entry,
) error {
	const (
		chunkMax   = 64 * 1024             // 64KiB chunks
		flushEvery = 50 * time.Millisecond // keep UI latency small
	)

	br := bufio.NewReader(rc)
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()

	buf := make([]byte, 0, chunkMax)

	flush := a.createFlushFunction(stream, logReq.UUID, &buf, logCtx)

	eof := false
	for !eof {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := br.ReadString('\n')
		switch err {
		case nil:
			// full line (ends with '\n')
		case io.EOF:
			eof = true
			// deliver final partial line if any
			if len(line) == 0 {
				continue
			}
		default:
			logCtx.WithError(err).Error("Error reading log stream")
			_ = stream.Send(&logstreamapi.LogStreamData{
				RequestUuid: logReq.UUID,
				Eof:         true,
				Error:       err.Error(),
			})
			return err
		}

		processedLines, processErr := a.processLogLine(line)
		if processErr != nil {
			continue
		}

		if len(processedLines) == 0 {
			select {
			case <-ticker.C:
				if err := flush("timer"); err != nil {
					return err
				}
			default:
			}
			continue
		}

		// Add each processed line to the buffer
		for _, processedLine := range processedLines {
			wire := processedLine
			// append to chunk buffer, splitting if a single line is larger than remaining space
			for len(wire) > 0 {
				space := chunkMax - len(buf)
				if space == 0 {
					if err := flush("chunk_full"); err != nil {
						return err
					}
					space = chunkMax
				}
				n := len(wire)
				if n > space {
					n = space
				}
				buf = append(buf, wire[:n]...)
				wire = wire[n:]
			}
		}
		// time-based flush to keep UI moving
		select {
		case <-ticker.C:
			if err := flush("timer"); err != nil {
				return err
			}
		default:
		}
	}

	// final flush & EOF
	if err := flush("final"); err != nil {
		return err
	}
	if err := stream.Send(&logstreamapi.LogStreamData{
		RequestUuid: logReq.UUID,
		Eof:         true,
	}); err != nil {
		return err
	}

	return nil
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
			// Send empty data to indicate the start of the stream
			// Used for health checks
			err = stream.Send(&logstreamapi.LogStreamData{
				RequestUuid: logReq.UUID,
				Data:        "",
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
			defer rc.Close()

			newLast, runErr := a.streamLogs(ctx, stream, rc, &resumeReq, logCtx)
			if newLast != nil {
				lastTimestamp = newLast
			}
			if runErr != nil {
				_, err = stream.CloseAndRecv()
				return err
			}
			return nil
		}

		err := attempt()
		if err == nil {
			// stream ended cleanly; usually means EOF from server/UI done
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

// streamLogs streams logs until the context is done, returning the last seen timestamp
func (a *Agent) streamLogs(ctx context.Context, stream logstreamapi.LogStreamService_StreamLogsClient, rc io.ReadCloser, logReq *event.ContainerLogRequest, logCtx *logrus.Entry) (*time.Time, error) {
	const (
		chunkMax   = 64 * 1024 // 64KiB chunks
		flushEvery = 50 * time.Millisecond
	)

	br := bufio.NewReader(rc)
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	buf := make([]byte, 0, chunkMax)
	var lastTimestamp *time.Time

	// Use the same proven flush function as static logs
	flush := a.createFlushFunction(stream, logReq.UUID, &buf, logCtx)

	for {
		select {
		case <-ctx.Done():
			return lastTimestamp, ctx.Err()
		case <-stream.Context().Done():
			return lastTimestamp, stream.Context().Err()
		default:
		}

		// Set read timeout to avoid blocking forever
		if tcpConn, ok := rc.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = tcpConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		}

		line, err := br.ReadString('\n')
		switch err {
		case nil:
			// Got a complete line
		case io.EOF:
			// For live streaming, EOF might be temporary - retry
			if err := flush("eof_flush"); err != nil {
				return lastTimestamp, err
			}
			if line == "" {
				time.Sleep(100 * time.Millisecond)
				continue
			}

		default:
			// Check if it's a timeout (expected for live streams)
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				if err := flush("timeout_flush"); err != nil {
					return lastTimestamp, err
				}
				continue
			}
			return lastTimestamp, err
		}

		// Extract timestamp from log line if present (for resume capability)
		if logReq.Timestamps && strings.Contains(line, "T") {
			if ts := extractTimestamp(line); ts != nil {
				lastTimestamp = ts
			}
		}

		// Process the line using the new processLogLine function
		processedLines, processErr := a.processLogLine(line)
		if processErr != nil {
			continue
		}

		// Add each processed line to the buffer
		for _, processedLine := range processedLines {
			wire := processedLine

			// Append to buffer, splitting if needed
			for len(wire) > 0 {
				space := chunkMax - len(buf)
				if space == 0 {
					if err := flush("chunk_full"); err != nil {
						return lastTimestamp, err
					}
					space = chunkMax
				}
				n := len(wire)
				if n > space {
					n = space
				}
				buf = append(buf, wire[:n]...)
				wire = wire[n:]
			}
		}

		// Time-based flush for live streaming
		select {
		case <-ticker.C:
			if err := flush("timer"); err != nil {
				return lastTimestamp, err
			}
		default:
		}
	}
}

// processLogLine processes a single log line and ensures proper formatting
func (a *Agent) processLogLine(line string) ([]string, error) {
	var results []string
	// Split on carriage returns (important for terminal rewrites!)
	for _, sub := range strings.Split(line, "\r") {
		if sub == "" {
			continue
		}
		// Trim whitespace but keep content
		sub = strings.TrimSpace(sub)
		if sub == "" {
			continue
		}
		// Add newline if not present
		if !strings.HasSuffix(sub, "\n") {
			sub += "\n"
		}
		results = append(results, sub)
	}
	return results, nil
}

// extractTimestamp extracts timestamp from a log line for resume capability
func extractTimestamp(line string) *time.Time {
	// Look for RFC3339 timestamp at the beginning of the line
	// Format: 2023-12-07T10:30:45.123456789Z
	if len(line) < 20 {
		return nil
	}

	// Find the first space or tab after potential timestamp
	spaceIdx := strings.IndexAny(line, " \t")
	if spaceIdx == -1 || spaceIdx > 35 { // RFC3339 with nanoseconds is max ~35 chars
		return nil
	}

	timestampStr := strings.TrimSpace(line[:spaceIdx])

	// Try parsing common timestamp formats
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000000000Z",
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
	}

	for _, format := range formats {
		if ts, err := time.Parse(format, timestampStr); err == nil {
			return &ts
		}
	}

	return nil
}
