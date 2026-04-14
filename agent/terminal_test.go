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

package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/terminalstreamapi"
	"github.com/argoproj-labs/argocd-agent/principal/apis/terminalstream/mock"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

func createTestTerminalAgent() *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	agent := &Agent{
		context:          ctx,
		cancelFn:         cancel,
		inflightLogs:     make(map[string]struct{}),
		inflightTerminal: make(map[string]struct{}),
		inflightMu:       sync.Mutex{},
	}
	return agent
}

func createTestTerminalAgentWithKube(t *testing.T, ctx context.Context, cancel context.CancelFunc) *Agent {
	t.Helper()
	cfg := &rest.Config{Host: "https://fake-k8s:6443"}
	clientset, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	return &Agent{
		context:  ctx,
		cancelFn: cancel,
		kubeClient: &kube.KubernetesClient{
			RestConfig: cfg,
			Clientset:  clientset,
		},
		inflightTerminal: make(map[string]struct{}),
	}
}

func createTestTerminalRequest() *event.ContainerTerminalRequest {
	return &event.ContainerTerminalRequest{
		UUID:          uuid.New().String(),
		Namespace:     "test-namespace",
		PodName:       "test-pod",
		ContainerName: "test-container",
		Command:       []string{"/bin/sh"},
		TTY:           true,
		Stdin:         true,
		Stdout:        true,
		Stderr:        true,
	}
}

// Test terminalSizeQueue
func TestTerminalSizeQueue(t *testing.T) {
	t.Run("Next returns size from queue", func(t *testing.T) {
		queue := &terminalSizeQueue{
			sizes: make(chan *remotecommand.TerminalSize, 10),
		}
		expectedSize := &remotecommand.TerminalSize{
			Width:  80,
			Height: 24,
		}
		queue.sizes <- expectedSize

		result := queue.Next()
		require.NotNil(t, result)
		assert.Equal(t, uint16(80), result.Width)
		assert.Equal(t, uint16(24), result.Height)
	})

	t.Run("Next returns nil when channel is closed", func(t *testing.T) {
		queue := &terminalSizeQueue{
			sizes: make(chan *remotecommand.TerminalSize, 10),
		}
		close(queue.sizes)

		result := queue.Next()
		assert.Nil(t, result)
	})
}

// Test terminalStreamHandler Read method
func TestTerminalStreamHandlerRead(t *testing.T) {
	t.Run("Read returns data from stdin channel", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		testData := []byte("test input data")
		handler.stdin <- testData

		buf := make([]byte, 100)
		n, err := handler.Read(buf)

		require.NoError(t, err)
		assert.Equal(t, len(testData), n)
		assert.Equal(t, testData, buf[:n])
	})

	t.Run("Read returns EOF when done channel is closed", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		close(handler.done)

		buf := make([]byte, 100)
		n, err := handler.Read(buf)

		assert.Equal(t, 0, n)
		assert.Equal(t, io.EOF, err)
	})

	t.Run("Read returns EOF when stdin channel is closed", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		close(handler.stdin)

		buf := make([]byte, 100)
		n, err := handler.Read(buf)

		assert.Equal(t, 0, n)
		assert.Equal(t, io.EOF, err)
	})
}

// Test terminalStreamHandler Write method
func TestTerminalStreamHandlerWrite(t *testing.T) {
	t.Run("Write sends data to stream", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		testData := []byte("test output data")
		n, err := handler.Write(testData)

		require.NoError(t, err)
		assert.Equal(t, len(testData), n)

		sentData := mockStream.GetSentData()
		require.Len(t, sentData, 1)
		assert.Equal(t, "test-uuid", sentData[0].RequestUuid)
		assert.Equal(t, testData, sentData[0].Data)
		assert.Equal(t, "stdout", sentData[0].StreamType)
	})

	t.Run("Write returns error on send failure", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		expectedErr := errors.New("send failed")
		mockStream.SetSendError(expectedErr)

		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		testData := []byte("test output data")
		n, err := handler.Write(testData)

		assert.Equal(t, 0, n)
		assert.Equal(t, expectedErr, err)
	})
}

// Test terminalStreamHandler close method
func TestTerminalStreamHandlerClose(t *testing.T) {
	t.Run("Close closes done channel once", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		handler.close()

		// Verify done channel is closed
		select {
		case <-handler.done:
			// Expected
		default:
			t.Error("done channel should be closed")
		}

		// Calling close again should not panic
		handler.close()
	})
}

func TestReceiveFromPrincipal(t *testing.T) {
	t.Run("Receives stdin data and forwards to stdin channel", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)

		// Set up receive function to return test data then EOF
		callCount := 0
		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			callCount++
			if callCount == 1 {
				return &terminalstreamapi.TerminalStreamData{
					RequestUuid: "test-uuid",
					Data:        []byte("test stdin"),
					StreamType:  "stdin",
				}, nil
			}
			return nil, io.EOF
		})

		sizeQueue := &terminalSizeQueue{
			sizes: make(chan *remotecommand.TerminalSize, 10),
		}

		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			sizeQueue:   sizeQueue,
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		// Run receiveFromPrincipal in a goroutine
		go handler.receiveFromPrincipal()

		// Wait for data on stdin channel
		select {
		case data := <-handler.stdin:
			assert.Equal(t, []byte("test stdin"), data)
		case <-time.After(1 * time.Second):
			t.Error("Timeout waiting for stdin data")
		}
	})

	t.Run("Handles terminal resize messages", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)

		// Set up receive function to return resize data then EOF
		callCount := 0
		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			callCount++
			if callCount == 1 {
				return &terminalstreamapi.TerminalStreamData{
					RequestUuid: "test-uuid",
					Resize:      true,
					Cols:        120,
					Rows:        40,
				}, nil
			}
			return nil, io.EOF
		})

		sizeQueue := &terminalSizeQueue{
			sizes: make(chan *remotecommand.TerminalSize, 10),
		}

		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			sizeQueue:   sizeQueue,
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		// Run receiveFromPrincipal in a goroutine
		go handler.receiveFromPrincipal()

		// Wait for resize on sizeQueue
		select {
		case size := <-sizeQueue.sizes:
			assert.Equal(t, uint16(120), size.Width)
			assert.Equal(t, uint16(40), size.Height)
		case <-time.After(1 * time.Second):
			t.Error("Timeout waiting for resize data")
		}
	})

	t.Run("Handles EOF message from principal", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)

		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			return &terminalstreamapi.TerminalStreamData{
				RequestUuid: "test-uuid",
				Eof:         true,
			}, nil
		})

		sizeQueue := &terminalSizeQueue{
			sizes: make(chan *remotecommand.TerminalSize, 10),
		}

		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			sizeQueue:   sizeQueue,
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		// Run receiveFromPrincipal - it should return when it receives EOF
		go handler.receiveFromPrincipal()

		// Wait for stdin channel to be closed
		timeout := time.After(1 * time.Second)
	drainLoop:
		for {
			select {
			case _, ok := <-handler.stdin:
				if !ok {
					// Channel closed as expected
					break drainLoop
				}
				// Drain any remaining data
			case <-timeout:
				t.Error("Timeout waiting for stdin channel to close")
				break drainLoop
			}
		}
	})

	t.Run("Handles error message from principal", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamClient(ctx)

		// Use atomic.Bool to avoid race conditions as cancelExec is called from the receiveFromPrincipal goroutine
		var execCancelled atomic.Bool
		cancelExec := func() {
			execCancelled.Store(true)
		}

		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			return &terminalstreamapi.TerminalStreamData{
				RequestUuid: "test-uuid",
				Error:       "some error from principal",
			}, nil
		})

		sizeQueue := &terminalSizeQueue{
			sizes: make(chan *remotecommand.TerminalSize, 10),
		}

		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			cancelExec:  cancelExec,
			sizeQueue:   sizeQueue,
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		// Run receiveFromPrincipal
		go handler.receiveFromPrincipal()

		// Wait for cancelExec to be called
		assert.Eventually(t, func() bool {
			return execCancelled.Load()
		}, 5*time.Second, 10*time.Millisecond, "cancelExec should be called on error")
	})

	t.Run("context cancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(context.Background())
		mockStream := mock.NewMockTerminalStreamClient(cancelCtx)

		// Set up receive to block
		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			<-cancelCtx.Done()
			return nil, cancelCtx.Err()
		})

		sizeQueue := &terminalSizeQueue{
			sizes: make(chan *remotecommand.TerminalSize, 10),
		}

		handler := &terminalStreamHandler{
			stream:      mockStream,
			sessionUUID: "test-uuid",
			stdin:       make(chan []byte, 100),
			done:        make(chan struct{}),
			sizeQueue:   sizeQueue,
			logCtx:      logrus.NewEntry(logrus.New()),
		}

		done := make(chan struct{})
		go func() {
			handler.receiveFromPrincipal()
			close(done)
		}()

		// Cancel the context
		cancel()

		// Wait for goroutine to finish
		select {
		case <-done:
			// Expected
		case <-time.After(1 * time.Second):
			t.Error("receiveFromPrincipal should return after context cancellation")
		}
	})
}

// Test isContextCanceledError
func TestIsContextCanceledError(t *testing.T) {
	t.Run("Returns true for context.Canceled", func(t *testing.T) {
		assert.True(t, isContextCanceledError(context.Canceled))
	})

	t.Run("Returns true for error containing 'context canceled'", func(t *testing.T) {
		err := errors.New("operation failed: context canceled")
		assert.True(t, isContextCanceledError(err))
	})

	t.Run("Returns false for other errors", func(t *testing.T) {
		err := errors.New("some other error")
		assert.False(t, isContextCanceledError(err))
	})

	t.Run("Returns false for nil error", func(t *testing.T) {
		assert.False(t, isContextCanceledError(nil))
	})
}

// Test isShellNotFoundError
func TestIsShellNotFoundError(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "executable file not found",
			err:      errors.New("executable file not found in $PATH"),
			expected: true,
		},
		{
			name:     "no such file or directory",
			err:      errors.New("no such file or directory"),
			expected: true,
		},
		{
			name:     "other error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "wrapped executable not found error",
			err:      errors.New("failed to exec: executable file not found"),
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isShellNotFoundError(tc.err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// Test duplicate terminal request handling
func TestDuplicateTerminalRequest(t *testing.T) {
	agent := createTestTerminalAgent()
	terminalReq := createTestTerminalRequest()

	// Add a terminal request to inflight
	agent.inflightMu.Lock()
	agent.inflightTerminal[terminalReq.UUID] = struct{}{}
	agent.inflightMu.Unlock()

	// Create an event with the same UUID
	evs := event.NewEventSource("test")
	ev, err := evs.NewTerminalRequestEvent(terminalReq)
	require.NoError(t, err)

	wrappedEvent := event.New(ev, event.TargetTerminal)

	// Process the event - should return nil without error for duplicate
	err = agent.processIncomingTerminalRequest(wrappedEvent)
	assert.NoError(t, err)
}

// Test inflight terminal tracking
func TestInflightTerminalTracking(t *testing.T) {
	agent := createTestTerminalAgent()
	terminalReq := createTestTerminalRequest()
	sessionUUID := terminalReq.UUID

	t.Run("Adding and removing from inflight map", func(t *testing.T) {
		// Initially, the session should not be in inflight
		agent.inflightMu.Lock()
		_, exists := agent.inflightTerminal[sessionUUID]
		agent.inflightMu.Unlock()
		assert.False(t, exists)

		// Add to inflight
		agent.inflightMu.Lock()
		agent.inflightTerminal[sessionUUID] = struct{}{}
		agent.inflightMu.Unlock()

		// Verify it's in inflight
		agent.inflightMu.Lock()
		_, exists = agent.inflightTerminal[sessionUUID]
		agent.inflightMu.Unlock()
		assert.True(t, exists)

		// Remove from inflight
		agent.inflightMu.Lock()
		delete(agent.inflightTerminal, sessionUUID)
		agent.inflightMu.Unlock()

		// Verify it's removed
		agent.inflightMu.Lock()
		_, exists = agent.inflightTerminal[sessionUUID]
		agent.inflightMu.Unlock()
		assert.False(t, exists)
	})
}

// Test concurrent terminal operations
func TestConcurrentTerminalOperations(t *testing.T) {
	t.Run("concurrent inflight tracking", func(t *testing.T) {
		agent := createTestTerminalAgent()
		var wg sync.WaitGroup
		numGoroutines := 50

		// Concurrent adds
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				uuid := uuid.New().String()
				agent.inflightMu.Lock()
				agent.inflightTerminal[uuid] = struct{}{}
				agent.inflightMu.Unlock()
			}(i)
		}

		wg.Wait()

		// Should have all sessions tracked without race conditions
		agent.inflightMu.Lock()
		count := len(agent.inflightTerminal)
		agent.inflightMu.Unlock()
		assert.Equal(t, numGoroutines, count)
	})
}

// TestWebSocketToSPDYFallback exercises the real WebSocket and SPDY executors
// against a test HTTP server that rejects WebSocket upgrades with 403 Forbidden,
func TestWebSocketToSPDYFallback(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	config := &rest.Config{
		Host: server.URL,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
		},
	}

	execPath := "/api/v1/namespaces/test/pods/test-pod/exec?command=sh&stdin=true&stdout=true&tty=true"
	wsURL := server.URL + execPath
	spdyURL, err := url.Parse(wsURL)
	require.NoError(t, err)

	streamOpts := remotecommand.StreamOptions{
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: io.Discard,
		Tty:    true,
	}

	t.Run("WebSocket executor returns handshake error on 403", func(t *testing.T) {
		wsExec, err := remotecommand.NewWebSocketExecutor(config, "GET", wsURL)
		require.NoError(t, err)

		err = wsExec.StreamWithContext(context.Background(), streamOpts)
		require.Error(t, err)
		assert.True(t, isWebSocketHandshakeError(err),
			"expected WebSocket handshake error, got: %v", err)
	})

	t.Run("SPDY executor error is not a WebSocket handshake error", func(t *testing.T) {
		spdyExec, err := remotecommand.NewSPDYExecutor(config, "POST", spdyURL)
		require.NoError(t, err)

		err = spdyExec.StreamWithContext(context.Background(), streamOpts)
		require.Error(t, err, "SPDY should also fail against the test server")
		assert.False(t, isWebSocketHandshakeError(err),
			"SPDY error must not be mistaken for a WebSocket handshake error, got: %v", err)
	})

	t.Run("full fallback flow: WebSocket 403 then SPDY retry", func(t *testing.T) {
		wsExec, err := remotecommand.NewWebSocketExecutor(config, "GET", wsURL)
		require.NoError(t, err)

		err = wsExec.StreamWithContext(context.Background(), streamOpts)

		// Verify the condition that triggers the fallback
		require.Error(t, err)
		require.True(t, isWebSocketHandshakeError(err),
			"expected handshake error to trigger SPDY fallback, got: %v", err)

		// Execute the fallback path
		spdyExec, spdyErr := remotecommand.NewSPDYExecutor(config, "POST", spdyURL)
		require.NoError(t, spdyErr, "SPDY executor creation must succeed during fallback")

		err = spdyExec.StreamWithContext(context.Background(), streamOpts)
		if err != nil {
			assert.False(t, isWebSocketHandshakeError(err),
				"SPDY fallback error should not re-trigger WebSocket fallback, got: %v", err)
		}
	})
}

type fakeExecutor struct{ err error }

func (f *fakeExecutor) Stream(_ remotecommand.StreamOptions) error { return f.err }
func (f *fakeExecutor) StreamWithContext(_ context.Context, _ remotecommand.StreamOptions) error {
	return f.err
}

// patchExecutors swaps the package-level executor constructors for the
// duration of a single test and restores the originals via t.Cleanup.
func patchExecutors(
	t *testing.T,
	wsFunc func(*rest.Config, string, string) (remotecommand.Executor, error),
	spdyFunc func(*rest.Config, string, *url.URL) (remotecommand.Executor, error),
) {
	t.Helper()
	origWS, origSPDY := newWebSocketExecutor, newSPDYExecutor
	newWebSocketExecutor = wsFunc
	newSPDYExecutor = spdyFunc
	t.Cleanup(func() {
		newWebSocketExecutor = origWS
		newSPDYExecutor = origSPDY
	})
}

// TestTerminalInPodFallback drives the production terminalInPod control flow
// by injecting fake executors through the package-level constructor vars.
func TestTerminalInPodFallback(t *testing.T) {
	t.Run("WebSocket bad-handshake triggers SPDY fallback and succeeds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		agent := createTestTerminalAgentWithKube(t, ctx, cancel)
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			return nil, io.EOF
		})

		spdyCalled := false
		patchExecutors(t,
			func(_ *rest.Config, _, _ string) (remotecommand.Executor, error) {
				return &fakeExecutor{err: errors.New("websocket: bad handshake")}, nil
			},
			func(_ *rest.Config, _ string, _ *url.URL) (remotecommand.Executor, error) {
				spdyCalled = true
				return &fakeExecutor{err: nil}, nil
			},
		)

		err := agent.terminalInPod(ctx, mockStream, createTestTerminalRequest(), logrus.NewEntry(logrus.New()))
		require.NoError(t, err)
		assert.True(t, spdyCalled, "SPDY executor must be invoked as fallback")
	})

	t.Run("SPDY fallback error is propagated to caller", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		agent := createTestTerminalAgentWithKube(t, ctx, cancel)
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			return nil, io.EOF
		})

		spdyErr := errors.New("spdy: connection refused")
		patchExecutors(t,
			func(_ *rest.Config, _, _ string) (remotecommand.Executor, error) {
				return &fakeExecutor{err: errors.New("websocket: bad handshake")}, nil
			},
			func(_ *rest.Config, _ string, _ *url.URL) (remotecommand.Executor, error) {
				return &fakeExecutor{err: spdyErr}, nil
			},
		)

		err := agent.terminalInPod(ctx, mockStream, createTestTerminalRequest(), logrus.NewEntry(logrus.New()))
		require.Error(t, err)
		assert.ErrorIs(t, err, spdyErr)
	})

	t.Run("WebSocket success skips SPDY fallback entirely", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		agent := createTestTerminalAgentWithKube(t, ctx, cancel)
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			return nil, io.EOF
		})

		spdyCalled := false
		patchExecutors(t,
			func(_ *rest.Config, _, _ string) (remotecommand.Executor, error) {
				return &fakeExecutor{err: nil}, nil
			},
			func(_ *rest.Config, _ string, _ *url.URL) (remotecommand.Executor, error) {
				spdyCalled = true
				return &fakeExecutor{err: nil}, nil
			},
		)

		err := agent.terminalInPod(ctx, mockStream, createTestTerminalRequest(), logrus.NewEntry(logrus.New()))
		require.NoError(t, err)
		assert.False(t, spdyCalled, "SPDY must not be called when WebSocket succeeds")
	})

	t.Run("WebSocket creation failure is returned immediately without SPDY", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		agent := createTestTerminalAgentWithKube(t, ctx, cancel)
		mockStream := mock.NewMockTerminalStreamClient(ctx)
		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			return nil, io.EOF
		})

		wsCreateErr := errors.New("failed to create ws executor")
		spdyCalled := false
		patchExecutors(t,
			func(_ *rest.Config, _, _ string) (remotecommand.Executor, error) {
				return nil, wsCreateErr
			},
			func(_ *rest.Config, _ string, _ *url.URL) (remotecommand.Executor, error) {
				spdyCalled = true
				return &fakeExecutor{}, nil
			},
		)

		err := agent.terminalInPod(ctx, mockStream, createTestTerminalRequest(), logrus.NewEntry(logrus.New()))
		require.Error(t, err)
		assert.False(t, spdyCalled, "SPDY must not be called when WS executor creation fails")
	})
}
