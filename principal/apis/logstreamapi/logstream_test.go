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

package logstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/logstreamapi"
	"github.com/argoproj-labs/argocd-agent/principal/apis/logstreamapi/mock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServer(t *testing.T) {
	server := NewServer()
	assert.NotNil(t, server)
	assert.NotNil(t, server.sessions)
	assert.Equal(t, 0, len(server.sessions))
}

func TestRegisterHTTP(t *testing.T) {
	server := NewServer()
	requestUUID := "test-request-123"

	t.Run("successful registration", func(t *testing.T) {
		w := mock.NewMockHTTPResponseWriter()
		r := httptest.NewRequest("GET", "/logs", nil)

		err := server.RegisterHTTP(requestUUID, w, r)
		assert.NoError(t, err)

		// Check headers were set
		assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Equal(t, "no-cache, no-transform", w.Header().Get("Cache-Control"))
		assert.Equal(t, "keep-alive", w.Header().Get("Connection"))
		assert.Equal(t, http.StatusOK, w.GetStatusCode())

		// Check session was created
		server.mu.RLock()
		sess, exists := server.sessions[requestUUID]
		server.mu.RUnlock()
		assert.True(t, exists)
		assert.NotNil(t, sess)
		assert.NotNil(t, sess.hw)
		assert.NotNil(t, sess.completeCh)
	})

	t.Run("writer without flusher", func(t *testing.T) {
		// Create a writer that doesn't implement http.Flusher
		w := &mock.MockWriterWithoutFlusher{}
		r := httptest.NewRequest("GET", "/logs", nil)

		err := server.RegisterHTTP(requestUUID, w, r)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "writer does not support flushing")
	})

	t.Run("upsert existing session", func(t *testing.T) {
		// First registration
		w1 := mock.NewMockHTTPResponseWriter()
		r1 := httptest.NewRequest("GET", "/logs", nil)
		err := server.RegisterHTTP(requestUUID, w1, r1)
		assert.NoError(t, err)

		// Second registration (upsert)
		w2 := mock.NewMockHTTPResponseWriter()
		r2 := httptest.NewRequest("GET", "/logs", nil)
		err = server.RegisterHTTP(requestUUID, w2, r2)
		assert.NoError(t, err)

		// Check session was updated
		server.mu.RLock()
		sess, exists := server.sessions[requestUUID]
		server.mu.RUnlock()
		assert.True(t, exists)
		assert.Equal(t, w2, sess.hw.w) // Should be the new writer
	})
}

func TestStreamLogs(t *testing.T) {
	server := NewServer()
	requestUUID := "test-request-123"

	t.Run("successful log streaming", func(t *testing.T) {
		// Register HTTP session first
		w := mock.NewMockHTTPResponseWriter()
		r := httptest.NewRequest("GET", "/logs", nil)
		err := server.RegisterHTTP(requestUUID, w, r)
		require.NoError(t, err)

		// Create mock stream
		ctx := context.Background()
		mockStream := mock.NewMockLogStreamServer(ctx)

		// Add test data
		mockStream.AddRecvData(&logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Data:        []byte("test log line 1\n"),
		})
		mockStream.AddRecvData(&logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Data:        []byte("test log line 2\n"),
		})
		mockStream.AddRecvData(&logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Eof:         true,
		})

		// Run StreamLogs
		err = server.StreamLogs(mockStream)
		assert.NoError(t, err)

		// Check that data was written to HTTP response
		body := w.GetBody()
		assert.Contains(t, body, "test log line 1")
		assert.Contains(t, body, "test log line 2")
	})

	t.Run("stream with error from agent", func(t *testing.T) {
		// Register HTTP session first
		w := mock.NewMockHTTPResponseWriter()
		r := httptest.NewRequest("GET", "/logs", nil)
		err := server.RegisterHTTP(requestUUID, w, r)
		require.NoError(t, err)

		// Create mock stream
		ctx := context.Background()
		mockStream := mock.NewMockLogStreamServer(ctx)

		// Add error data
		mockStream.AddRecvData(&logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Error:       "agent error occurred",
		})

		// Run StreamLogs
		err = server.StreamLogs(mockStream)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent error occurred")
	})

	t.Run("stream with unknown request ID", func(t *testing.T) {
		// Don't register HTTP session
		ctx := context.Background()
		mockStream := mock.NewMockLogStreamServer(ctx)

		// Add data with unknown request ID
		mockStream.AddRecvData(&logstreamapi.LogStreamData{
			RequestUuid: "unknown-request",
			Data:        []byte("test log line\n"),
		})

		// Run StreamLogs
		err := server.StreamLogs(mockStream)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown request id")
	})

	t.Run("stream with nil message", func(t *testing.T) {
		// Register HTTP session first
		w := mock.NewMockHTTPResponseWriter()
		r := httptest.NewRequest("GET", "/logs", nil)
		err := server.RegisterHTTP(requestUUID, w, r)
		require.NoError(t, err)

		// Create mock stream
		ctx := context.Background()
		mockStream := mock.NewMockLogStreamServer(ctx)

		// Add a nil message to the recv data
		mockStream.AddRecvData(nil)

		// Run StreamLogs
		err = server.StreamLogs(mockStream)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid log message")
	})

	t.Run("stream with context cancellation", func(t *testing.T) {
		// Register HTTP session first
		w := mock.NewMockHTTPResponseWriter()
		r := httptest.NewRequest("GET", "/logs", nil)
		err := server.RegisterHTTP(requestUUID, w, r)
		require.NoError(t, err)

		// Create mock stream with already canceled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately
		mockStream := mock.NewMockLogStreamServer(ctx)

		// Run StreamLogs
		err = server.StreamLogs(mockStream)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client detached timeout")
	})
}

func TestProcessLogMessage(t *testing.T) {
	server := NewServer()
	requestUUID := "test-request-123"

	// Register HTTP session first
	w := mock.NewMockHTTPResponseWriter()
	r := httptest.NewRequest("GET", "/logs", nil)
	err := server.RegisterHTTP(requestUUID, w, r)
	require.NoError(t, err)

	// Create log client
	ctx := context.Background()
	client := server.newLogClient(ctx)
	client.requestID = requestUUID

	t.Run("successful log processing", func(t *testing.T) {
		msg := &logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Data:        []byte("test log line\n"),
		}

		err := server.processLogMessage(client, msg)
		assert.NoError(t, err)

		// Check that data was written
		body := w.GetBody()
		assert.Contains(t, body, "test log line")
	})

	t.Run("empty data (no-op)", func(t *testing.T) {
		// Clear the body first
		w.Reset()

		msg := &logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Data:        []byte{}, // Empty data
		}

		err := server.processLogMessage(client, msg)
		assert.NoError(t, err)

		// Body should remain empty since empty data is ignored
		body := w.GetBody()
		assert.Empty(t, body)
	})

	t.Run("EOF handling", func(t *testing.T) {
		msg := &logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Eof:         true,
		}

		err := server.processLogMessage(client, msg)
		assert.Equal(t, io.EOF, err)

		// Check that completion channel was signaled
		server.mu.RLock()
		sess := server.sessions[requestUUID]
		server.mu.RUnlock()
		assert.NotNil(t, sess)

		// Wait for completion signal
		select {
		case <-sess.completeCh:
			// Success
		case <-time.After(100 * time.Millisecond):
			t.Fatal("completion channel was not signaled")
		}
	})

	t.Run("agent error", func(t *testing.T) {
		msg := &logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Error:       "agent error",
		}

		err := server.processLogMessage(client, msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent error")
	})

	t.Run("unknown request ID", func(t *testing.T) {
		msg := &logstreamapi.LogStreamData{
			RequestUuid: "unknown-request",
			Data:        []byte("test log line\n"),
		}

		err := server.processLogMessage(client, msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown request id")
	})

	t.Run("missing HTTP writer", func(t *testing.T) {
		// Remove the HTTP writer
		server.mu.Lock()
		if sess, ok := server.sessions[requestUUID]; ok {
			sess.hw = nil
		}
		server.mu.Unlock()

		msg := &logstreamapi.LogStreamData{
			RequestUuid: requestUUID,
			Data:        []byte("test log line\n"),
		}

		err := server.processLogMessage(client, msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client disconnected")
	})
}

func TestWaitForCompletion(t *testing.T) {
	server := NewServer()
	requestUUID := "test-request-123"

	t.Run("completion within timeout", func(t *testing.T) {
		// Register session
		w := mock.NewMockHTTPResponseWriter()
		r := httptest.NewRequest("GET", "/logs", nil)
		err := server.RegisterHTTP(requestUUID, w, r)
		require.NoError(t, err)

		// Signal completion
		server.mu.RLock()
		sess := server.sessions[requestUUID]
		server.mu.RUnlock()
		require.NotNil(t, sess)

		go func() {
			time.Sleep(10 * time.Millisecond)
			select {
			case sess.completeCh <- true:
			default:
			}
		}()

		// Wait for completion
		completed := server.WaitForCompletion(requestUUID, 100*time.Millisecond)
		assert.True(t, completed)
	})

	t.Run("timeout before completion", func(t *testing.T) {
		// Register session
		w := mock.NewMockHTTPResponseWriter()
		r := httptest.NewRequest("GET", "/logs", nil)
		err := server.RegisterHTTP(requestUUID, w, r)
		require.NoError(t, err)

		// Wait for completion (should timeout)
		completed := server.WaitForCompletion(requestUUID, 10*time.Millisecond)
		assert.False(t, completed)
	})

	t.Run("unknown request ID", func(t *testing.T) {
		completed := server.WaitForCompletion("unknown-request", 10*time.Millisecond)
		assert.False(t, completed)
	})
}

func TestFinalizeSession(t *testing.T) {
	server := NewServer()
	requestUUID := "test-request-123"

	// Register session
	w := mock.NewMockHTTPResponseWriter()
	r := httptest.NewRequest("GET", "/logs", nil)
	err := server.RegisterHTTP(requestUUID, w, r)
	require.NoError(t, err)

	// Verify session exists
	server.mu.RLock()
	_, exists := server.sessions[requestUUID]
	server.mu.RUnlock()
	assert.True(t, exists)

	// Finalize session
	server.finalizeSession(requestUUID)

	// Verify session was removed
	server.mu.RLock()
	_, exists = server.sessions[requestUUID]
	server.mu.RUnlock()
	assert.False(t, exists)
}

func TestSafeFlush(t *testing.T) {
	t.Run("successful flush", func(t *testing.T) {
		w := mock.NewMockHTTPResponseWriter()
		err := safeFlush(w)
		assert.NoError(t, err)
		assert.True(t, w.IsFlushCalled())
	})

	t.Run("flush with panic", func(t *testing.T) {
		// Create a flusher that panics
		panicFlusher := &mock.PanicFlusher{}

		// This should not panic due to the defer recover, but should return an error
		err := safeFlush(panicFlusher)
		assert.Error(t, err) // safeFlush catches panics and returns an error
		assert.Contains(t, err.Error(), "flush panic")
	})
}

func TestTryRecvWithCancel(t *testing.T) {
	t.Run("successful receive", func(t *testing.T) {
		ctx := context.Background()
		called := false
		fn := func() (string, error) {
			called = true
			return "test", nil
		}

		result, err := tryRecvWithCancel(ctx, fn)
		assert.NoError(t, err)
		assert.Equal(t, "test", result)
		assert.True(t, called)
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		fn := func() (string, error) {
			time.Sleep(100 * time.Millisecond) // Simulate slow operation
			return "test", nil
		}

		result, err := tryRecvWithCancel(ctx, fn)
		assert.Error(t, err)
		assert.Equal(t, "", result)
		assert.Contains(t, err.Error(), "client detached timeout")
	})

	t.Run("function error", func(t *testing.T) {
		ctx := context.Background()
		expectedErr := fmt.Errorf("function error")
		fn := func() (string, error) {
			return "", expectedErr
		}

		result, err := tryRecvWithCancel(ctx, fn)
		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Equal(t, "", result)
	})
}

func TestNewLogClient(t *testing.T) {
	ctx := context.Background()
	client := NewServer().newLogClient(ctx)

	assert.NotNil(t, client)
	assert.NotNil(t, client.ctx)
	assert.NotNil(t, client.cancelFn)
	assert.NotNil(t, client.logCtx)
	assert.Equal(t, "", client.requestID)
	assert.Nil(t, client.terminateErr)

	// Test that context can be canceled
	cancel := client.cancelFn
	cancel()
	select {
	case <-client.ctx.Done():
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Fatal("context was not canceled")
	}
}

func TestConcurrentAccess(t *testing.T) {
	server := NewServer()
	requestUUID := "test-request-123"

	// Register session
	w := mock.NewMockHTTPResponseWriter()
	r := httptest.NewRequest("GET", "/logs", nil)
	err := server.RegisterHTTP(requestUUID, w, r)
	require.NoError(t, err)

	// Test concurrent access to sessions map
	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Read from sessions
			server.mu.RLock()
			sess, exists := server.sessions[requestUUID]
			server.mu.RUnlock()

			assert.True(t, exists)
			assert.NotNil(t, sess)

			// Simulate some work
			time.Sleep(1 * time.Millisecond)
		}(i)
	}

	wg.Wait()
}

func init() {
	// Set log level to reduce noise during testing
	logrus.SetLevel(logrus.ErrorLevel)
}
