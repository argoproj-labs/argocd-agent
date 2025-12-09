// Copyright 2025 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/logstreamapi"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements logstreamapi.LogStreamServiceServer
type Server struct {
	logstreamapi.UnimplementedLogStreamServiceServer
	mu       sync.RWMutex
	sessions map[string]*session
}

type session struct {
	hw         *httpWriter
	completeCh chan bool // signaled on EOF (static logs)
	cancelFn   context.CancelFunc
	doneCh     chan struct{} // closed on finalization to stop watchdog goroutine
}

type httpWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

type logClient struct {
	ctx          context.Context
	cancelFn     context.CancelFunc
	logCtx       *logrus.Entry
	requestID    string
	terminateErr error // returned as stream status when set
}

func (s *Server) newLogClient(ctx context.Context) *logClient {
	cctx, cancel := context.WithCancel(ctx)
	return &logClient{
		ctx:      cctx,
		cancelFn: cancel,
		logCtx:   logrus.WithField("module", "LogStream"),
	}
}

func NewServer() *Server {
	logrus.Info("Starting LogStream gRPC service")
	return &Server{
		sessions: make(map[string]*session),
	}
}

// RegisterHTTP registers an HTTP writer for a given request UUID
func (s *Server) RegisterHTTP(requestUUID string, w http.ResponseWriter, r *http.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	flusher, ok := w.(http.Flusher)
	if !ok {
		return status.Error(codes.FailedPrecondition, "writer does not support flushing")
	}
	// streaming headers
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// upsert session
	sess := s.sessions[requestUUID]
	if sess == nil {
		sess = &session{
			hw:         &httpWriter{w: w, flusher: flusher},
			completeCh: make(chan bool, 1),
			doneCh:     make(chan struct{}),
		}
		s.sessions[requestUUID] = sess
	} else {
		sess.hw = &httpWriter{w: w, flusher: flusher}
	}

	//watchdog for client disconnection. When client disconnects, immediately cancel the stream
	go func(reqID, ua, ra string, done <-chan struct{}) {
		// Wait for either client disconnection or session finalization
		select {
		case <-done:
			logrus.WithFields(logrus.Fields{
				"request_id": reqID,
				"reason":     "client_disconnected",
			}).Info("Stream terminated due to client disconnection")
		case <-sess.doneCh:
			// Session was finalized, watchdog is no longer needed
			return
		}

		s.mu.Lock()
		sess := s.sessions[reqID]
		if sess != nil {
			sess.hw = nil
			if sess.cancelFn != nil {
				// Tag stream as canceled due to client detach.
				sess.cancelFn()
			}
		}
		s.mu.Unlock()
	}(requestUUID, r.Header.Get("User-Agent"), r.RemoteAddr, r.Context().Done())

	return nil
}

// tryRecvWithCancel wraps stream.Recv() so we can abort via c.ctx.Done().
func tryRecvWithCancel[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type res struct {
		m   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		m, err := fn()
		select {
		case ch <- res{m, err}: // send result if someone is still listening
		case <-ctx.Done(): // otherwise just exit quietly
		}
	}()
	select {
	case <-ctx.Done():
		var zero T
		return zero, status.Error(codes.Canceled, "client detached timeout")
	case r := <-ch:
		return r.m, r.err
	}
}

// StreamLogs receives log data from agent and forwards to HTTP writer
func (s *Server) StreamLogs(stream logstreamapi.LogStreamService_StreamLogsServer) error {
	c := s.newLogClient(stream.Context())
	for {
		msg, err := tryRecvWithCancel(c.ctx, stream.Recv) // ← cancelable
		if err != nil {
			// prefer a consistent reason for the agent
			if status.Code(err) == codes.Canceled && c.terminateErr == nil {
				c.terminateErr = status.Error(codes.Canceled, "client detached timeout")
			}
			break
		}
		if msg == nil {
			c.terminateErr = status.Error(codes.InvalidArgument, "invalid log message")
			break
		}

		// First message, capture request UUID and expose cancelFn for detach handler
		if c.requestID == "" {
			c.requestID = msg.GetRequestUuid()
			c.logCtx = c.logCtx.WithField("request_id", c.requestID)
			c.logCtx.Info("LogStream started")

			s.mu.Lock()
			if sess, ok := s.sessions[c.requestID]; ok {
				sess.cancelFn = func() {
					// tag this stream as terminated due to client detach TTL
					c.terminateErr = status.Error(codes.Canceled, "client detached timeout")
					c.cancelFn()
				}
			}
			s.mu.Unlock()
		}

		if err := s.processLogMessage(c, msg); err != nil {
			// processLogMessage can return io.EOF or a terminal status
			if err == io.EOF {
				break
			}
			c.terminateErr = err
			break
		}
	}
	// Cleanup session
	if c.requestID != "" {
		s.finalizeSession(c.requestID)
	}

	if c.terminateErr != nil {
		return c.terminateErr
	}
	resp := &logstreamapi.LogStreamResponse{RequestUuid: c.requestID, Status: 200}
	return stream.SendAndClose(resp)
}

// safeFlush prevents process crash if ResponseWriter is gone.
func safeFlush(f http.Flusher) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("flush panic: %v", r)
		}
	}()
	f.Flush()
	return nil
}

func (s *Server) processLogMessage(c *logClient, msg *logstreamapi.LogStreamData) error {
	reqID := msg.GetRequestUuid()
	logCtx := c.logCtx

	// Lookup session
	s.mu.RLock()
	sess := s.sessions[reqID]
	s.mu.RUnlock()
	if sess == nil {
		logCtx.Warn("received data for unknown request; terminating")
		return status.Error(codes.NotFound, "unknown request id")
	}

	// Agent forwarded error
	if msg.GetError() != "" {
		logCtx.WithField("error", msg.GetError()).Warn("log stream error from agent")
		return status.Error(codes.Internal, msg.GetError())
	}
	// EOF
	if msg.GetEof() {
		logCtx.Info("LogStream EOF")
		s.mu.Lock()
		if sess, ok := s.sessions[reqID]; ok && sess.completeCh != nil {
			select {
			case sess.completeCh <- true:
			default: // don't block if already signaled
			}
		}
		s.mu.Unlock()
		return io.EOF
	}

	data := msg.GetData()
	// Agent sends empty frame as prob, no flush needed
	if len(data) == 0 {
		return nil
	}
	logCtx.WithField("data_length", len(data)).Trace("data received")

	// Get current writer
	s.mu.RLock()
	hw := sess.hw
	cancel := sess.cancelFn
	s.mu.RUnlock()

	// If writer is gone, end the stream (vanilla semantics: new request will be created)
	if hw == nil {
		logCtx.Info("HTTP writer missing; terminating stream")
		return status.Error(codes.Canceled, "client disconnected")
	}

	// write + flush; on failure, null writer and cancel
	if _, err := hw.w.Write(data); err != nil {
		logCtx.WithError(err).Warn("HTTP write failed; canceling stream")
		s.mu.Lock()
		if sess := s.sessions[reqID]; sess != nil {
			sess.hw = nil
		}
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return status.Error(codes.Canceled, "HTTP write failed")
	}
	if err := safeFlush(hw.flusher); err != nil {
		logCtx.WithError(err).Warn("HTTP flush failed; canceling stream")
		s.mu.Lock()
		if sess := s.sessions[reqID]; sess != nil {
			sess.hw = nil
		}
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return status.Error(codes.Canceled, "HTTP flush failed")
	}
	logCtx.WithFields(logrus.Fields{
		"data_length": len(data),
		"request_id":  reqID,
	}).Info("HTTP write and flush successful")
	return nil
}

// WaitForCompletion waits for a LogStream to complete (static logs) or times out
func (s *Server) WaitForCompletion(requestUUID string, timeout time.Duration) bool {
	s.mu.RLock()
	sess := s.sessions[requestUUID]
	s.mu.RUnlock()

	if sess == nil || sess.completeCh == nil {
		return false
	}
	select {
	case <-sess.completeCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (s *Server) finalizeSession(requestUUID string) {
	s.mu.Lock()
	sess := s.sessions[requestUUID]
	if sess != nil && sess.doneCh != nil {
		// Close doneCh to signal watchdog goroutine to exit
		close(sess.doneCh)
	}
	delete(s.sessions, requestUUID)
	s.mu.Unlock()
}
