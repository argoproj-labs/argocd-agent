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

// closeChannels safely closes doneCh and completeCh if open.
// Caller must hold the server mutex.
func (sess *session) closeChannels() {
	if sess.doneCh != nil {
		close(sess.doneCh)
		sess.doneCh = nil
	}
	if sess.completeCh != nil {
		close(sess.completeCh)
		sess.completeCh = nil
	}
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

	// watchdog for client disconnection. When client disconnects, immediately cancel the stream.
	// doneCh is passed as a parameter to avoid a data race with closeChannels setting it to nil.
	go func(reqID string, done <-chan struct{}, doneCh <-chan struct{}) {
		// Wait for either client disconnection or session finalization
		select {
		case <-done:
			logrus.WithFields(logrus.Fields{
				"request_id": reqID,
				"reason":     "client_disconnected",
			}).Debug("HTTP client disconnected; canceling stream")
		case <-doneCh:
			// Session was finalized, watchdog is no longer needed
			return
		}
		s.mu.Lock()
		if sess, ok := s.sessions[reqID]; ok {
			sess.hw = nil
			if sess.cancelFn != nil {
				// Tag stream as canceled due to client detach.
				sess.cancelFn()
			}
		}
		s.mu.Unlock()
	}(requestUUID, r.Context().Done(), sess.doneCh)

	return nil
}

// StreamLogs receives log data from agent and forwards to HTTP writer
func (s *Server) StreamLogs(stream logstreamapi.LogStreamService_StreamLogsServer) error {
	c := s.newLogClient(stream.Context())
	// One background pump goroutine per stream.
	dataCh := make(chan *logstreamapi.LogStreamData)
	errCh := make(chan error, 1)
	go func() {
		defer close(dataCh)
		for {
			msg, err := stream.Recv()
			if err != nil {
				// Best-effort: don't block shutdown if receiver already exited.
				select {
				case errCh <- err:
				default:
				}
				return
			}
			select {
			case dataCh <- msg:
			case <-c.ctx.Done():
				return
			case <-stream.Context().Done():
				return
			}
		}
	}()

	for {
		select {
		case <-c.ctx.Done():
			// Ensure we terminate promptly when HTTP client detaches.
			if c.terminateErr == nil {
				c.terminateErr = status.Error(codes.Canceled, "client detached timeout")
			}
			goto done
		case err := <-errCh:
			// io.EOF means the client finished sending (normal close on the agent side).
			if err != nil && err != io.EOF {
				// Prefer a consistent reason for the agent on cancellations.
				if status.Code(err) == codes.Canceled && c.terminateErr == nil {
					c.terminateErr = status.Error(codes.Canceled, "client detached timeout")
				} else if c.terminateErr == nil {
					c.terminateErr = err
				}
			}
			goto done
		case msg, ok := <-dataCh:
			if !ok {
				// Pump exited without delivering an error (likely due to ctx cancellation).
				goto done
			}
			if msg == nil {
				c.terminateErr = status.Error(codes.InvalidArgument, "invalid log message")
				goto done
			}

			// First message, capture request UUID and expose cancelFn for detach handler
			if c.requestID == "" {
				c.requestID = msg.GetRequestUuid()
				c.logCtx = c.logCtx.WithField("request_id", c.requestID)
				c.logCtx.Info("LogStream started")

				s.mu.Lock()
				if sess, ok := s.sessions[c.requestID]; ok {
					sess.cancelFn = func() {
						// tag this stream as terminated due to client detach
						c.terminateErr = status.Error(codes.Canceled, "client detached timeout")
						c.cancelFn()
					}
				}
				s.mu.Unlock()
			}

			if err := s.processLogMessage(c, msg); err != nil {
				// processLogMessage can return io.EOF or a terminal status
				if err == io.EOF {
					goto done
				}
				c.terminateErr = err
				goto done
			}
		}
	}
done:
	// Cleanup session
	if c.requestID != "" {
		s.finalizeSession(c.requestID)
	}

	if c.terminateErr != nil {
		return c.terminateErr
	}
	resp := &logstreamapi.LogStreamResponse{RequestUuid: c.requestID, Status: http.StatusOK}
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

// clearWriterAndCancel clears the HTTP writer and invokes the cancel function.
// Used when write/flush fails or client disconnects.
func (s *Server) clearWriterAndCancel(reqID string) {
	s.mu.Lock()
	sess := s.sessions[reqID]
	var cancel context.CancelFunc
	if sess != nil {
		sess.hw = nil
		cancel = sess.cancelFn
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
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
		if sess, ok := s.sessions[reqID]; ok {
			// Close doneCh FIRST to stop watchdog before HTTP handler returns.
			// This prevents a race where the watchdog cancels the stream after
			// WaitForCompletion returns but before finalizeSession is called.
			if sess.doneCh != nil {
				close(sess.doneCh)
				sess.doneCh = nil
			}
			// Signal completion so WaitForCompletion can return (non-blocking).
			select {
			case sess.completeCh <- true:
			default:
			}
		}
		s.mu.Unlock()
		return io.EOF
	}

	data := msg.GetData()
	// Agent sends empty frame as probe, no flush needed
	if len(data) == 0 {
		return nil
	}
	logCtx.WithField("data_length", len(data)).Trace("data received")

	// Get current writer
	s.mu.RLock()
	hw := sess.hw
	s.mu.RUnlock()

	// If writer is gone, end the stream (vanilla semantics: new request will be created)
	if hw == nil {
		logCtx.Info("HTTP writer missing; terminating stream")
		return status.Error(codes.Canceled, "client disconnected")
	}

	// Write data and flush; on failure, clear writer and cancel stream
	if _, err := hw.w.Write(data); err != nil {
		logCtx.WithError(err).Warn("HTTP write failed; canceling stream")
		s.clearWriterAndCancel(reqID)
		return status.Error(codes.Canceled, "HTTP write failed")
	}
	if err := safeFlush(hw.flusher); err != nil {
		logCtx.WithError(err).Warn("HTTP flush failed; canceling stream")
		s.clearWriterAndCancel(reqID)
		return status.Error(codes.Canceled, "HTTP flush failed")
	}
	logCtx.WithField("data_length", len(data)).Trace("HTTP write and flush successful")
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

// RemoveSession removes a session if it exists.
func (s *Server) RemoveSession(requestUUID string) {
	s.finalizeSession(requestUUID)
}

func (s *Server) finalizeSession(requestUUID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess := s.sessions[requestUUID]; sess != nil {
		// closeChannels unblocks WaitForCompletion and stops watchdog.
		// Channels may already be closed from EOF handling.
		sess.closeChannels()
	}
	delete(s.sessions, requestUUID)
}
