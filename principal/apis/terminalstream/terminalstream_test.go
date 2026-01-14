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

package terminalstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/terminalstreamapi"
	"github.com/argoproj-labs/argocd-agent/principal/apis/terminalstream/mock"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test NewServer
func TestNewServer(t *testing.T) {
	t.Run("creates server with session manager", func(t *testing.T) {
		server := NewServer()
		require.NotNil(t, server)
		require.NotNil(t, server.sessions)
		require.NotNil(t, server.sessions.sessions)
	})
}

// Test SessionManager Add
func TestSessionManagerAdd(t *testing.T) {
	t.Run("adds session to manager", func(t *testing.T) {
		sm := &SessionManager{
			sessions: make(map[string]*TerminalSession),
		}

		session := &TerminalSession{
			UUID:      "test-uuid",
			AgentName: "test-agent",
			ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, 10),
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		sm.Add(session)

		// Verify session was added
		sm.mutex.RLock()
		addedSession, exists := sm.sessions["test-uuid"]
		sm.mutex.RUnlock()

		assert.True(t, exists)
		assert.Equal(t, session, addedSession)
	})
}

// Test SessionManager Get
func TestSessionManagerGet(t *testing.T) {
	t.Run("returns session by UUID", func(t *testing.T) {
		sm := &SessionManager{
			sessions: make(map[string]*TerminalSession),
		}

		session := &TerminalSession{
			UUID:      "test-uuid",
			AgentName: "test-agent",
			ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, 10),
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		sm.sessions["test-uuid"] = session

		result := sm.Get("test-uuid")
		assert.Equal(t, session, result)
	})

	t.Run("returns nil for non-existent session", func(t *testing.T) {
		sm := &SessionManager{
			sessions: make(map[string]*TerminalSession),
		}

		result := sm.Get("non-existent")
		assert.Nil(t, result)
	})
}

// Test SessionManager Remove
func TestSessionManagerRemove(t *testing.T) {
	t.Run("removes session and calls close", func(t *testing.T) {
		sm := &SessionManager{
			sessions: make(map[string]*TerminalSession),
		}

		session := &TerminalSession{
			UUID:      "test-uuid",
			AgentName: "test-agent",
			ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, 10),
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		sm.sessions["test-uuid"] = session

		sm.Remove("test-uuid")

		// Verify session was removed
		sm.mutex.RLock()
		_, exists := sm.sessions["test-uuid"]
		sm.mutex.RUnlock()

		assert.False(t, exists)

		// Verify Done channel was closed
		select {
		case <-session.Done:
			// Expected
		default:
			t.Error("Done channel should be closed")
		}
	})
}

// Test TerminalSession Close
func TestTerminalSessionClose(t *testing.T) {
	t.Run("closes done channel and websocket", func(t *testing.T) {
		// Create a test WebSocket server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			// Keep connection open briefly
			time.Sleep(100 * time.Millisecond)
			conn.Close()
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)

		session := &TerminalSession{
			UUID:      "test-uuid",
			AgentName: "test-agent",
			WSConn:    wsConn,
			ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, 10),
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		session.Close()

		// Verify Done channel is closed
		select {
		case <-session.Done:
			// Expected
		default:
			t.Error("Done channel should be closed")
		}
	})

	t.Run("close is idempotent", func(t *testing.T) {
		session := &TerminalSession{
			UUID:      "test-uuid",
			AgentName: "test-agent",
			ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, 10),
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		// Should not panic on multiple calls
		session.Close()
		session.Close()
		session.Close()

		// Verify Done channel is closed
		select {
		case <-session.Done:
			// Expected
		default:
			t.Error("Done channel should be closed")
		}
	})

	t.Run("close handles nil websocket", func(t *testing.T) {
		session := &TerminalSession{
			UUID:      "test-uuid",
			AgentName: "test-agent",
			WSConn:    nil, // No WebSocket
			ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, 10),
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		// Should not panic
		session.Close()

		// Verify Done channel is closed
		select {
		case <-session.Done:
			// Expected
		default:
			t.Error("Done channel should be closed")
		}
	})
}

// Test RegisterSession and UnregisterSession
func TestRegisterUnregisterSession(t *testing.T) {
	t.Run("register and unregister session", func(t *testing.T) {
		server := NewServer()

		session := &TerminalSession{
			UUID:      "test-uuid",
			AgentName: "test-agent",
			ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, 10),
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		server.RegisterSession(session)

		// Verify session is registered
		result := server.sessions.Get("test-uuid")
		assert.Equal(t, session, result)

		server.UnregisterSession("test-uuid")

		// Verify session is unregistered
		result = server.sessions.Get("test-uuid")
		assert.Nil(t, result)
	})
}

// Test StreamTerminal handshake
func TestStreamTerminalHandshake(t *testing.T) {
	t.Run("fails when handshake message has empty UUID", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamServer(ctx)

		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			return &terminalstreamapi.TerminalStreamData{
				RequestUuid: "", // Empty UUID
			}, nil
		})

		server := NewServer()
		err := server.StreamTerminal(mockStream)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty session UUID")
	})

	t.Run("fails when session not found", func(t *testing.T) {
		ctx := context.Background()
		mockStream := mock.NewMockTerminalStreamServer(ctx)

		mockStream.SetRecvFunc(func() (*terminalstreamapi.TerminalStreamData, error) {
			return &terminalstreamapi.TerminalStreamData{
				RequestUuid: "non-existent-uuid",
			}, nil
		})

		server := NewServer()
		err := server.StreamTerminal(mockStream)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

// Test tryRecvWithCancel
func TestTryRecvWithCancel(t *testing.T) {
	t.Run("returns result when receive succeeds", func(t *testing.T) {
		ctx := context.Background()
		expected := &terminalstreamapi.TerminalStreamData{
			RequestUuid: "test-uuid",
			Data:        []byte("test data"),
		}

		recvFn := func() (*terminalstreamapi.TerminalStreamData, error) {
			return expected, nil
		}

		result, err := tryRecvWithCancel(ctx, recvFn)

		require.NoError(t, err)
		assert.Equal(t, expected, result)
	})

	t.Run("returns error when receive fails", func(t *testing.T) {
		ctx := context.Background()
		expectedErr := io.EOF

		recvFn := func() (*terminalstreamapi.TerminalStreamData, error) {
			return nil, expectedErr
		}

		result, err := tryRecvWithCancel(ctx, recvFn)

		assert.Equal(t, expectedErr, err)
		assert.Nil(t, result)
	})

	t.Run("returns context error when context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		recvFn := func() (*terminalstreamapi.TerminalStreamData, error) {
			// This will block until context is cancelled
			time.Sleep(1 * time.Second)
			return nil, nil
		}

		result, err := tryRecvWithCancel(ctx, recvFn)

		assert.Equal(t, context.Canceled, err)
		assert.Nil(t, result)
	})

	t.Run("returns result before context times out", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		expected := &terminalstreamapi.TerminalStreamData{
			RequestUuid: "test-uuid",
		}

		recvFn := func() (*terminalstreamapi.TerminalStreamData, error) {
			return expected, nil
		}

		result, err := tryRecvWithCancel(ctx, recvFn)

		require.NoError(t, err)
		assert.Equal(t, expected, result)
	})
}

// Test sendToWebSocket
func TestSendToWebSocket(t *testing.T) {
	t.Run("forwards data to FromAgent channel", func(t *testing.T) {
		server := NewServer()
		logCtx := logrus.NewEntry(logrus.New())

		session := &TerminalSession{
			UUID:      "test-uuid",
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		msg := &terminalstreamapi.TerminalStreamData{
			RequestUuid: "test-uuid",
			Data:        []byte("test data"),
		}

		err := server.sendToWebSocket(session, msg, logCtx)
		require.NoError(t, err)

		// Verify data was forwarded
		select {
		case received := <-session.FromAgent:
			assert.Equal(t, msg, received)
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected data to be forwarded")
		}
	})

	t.Run("returns nil when done channel is closed", func(t *testing.T) {
		server := NewServer()
		logCtx := logrus.NewEntry(logrus.New())

		session := &TerminalSession{
			UUID:      "test-uuid",
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData), // Unbuffered
			Done:      make(chan struct{}),
		}

		close(session.Done)

		msg := &terminalstreamapi.TerminalStreamData{
			RequestUuid: "test-uuid",
			Data:        []byte("test data"),
		}

		err := server.sendToWebSocket(session, msg, logCtx)
		assert.NoError(t, err) // Should not error, just skip
	})
}

// Test log function
func TestLogFunction(t *testing.T) {
	t.Run("returns module logger", func(t *testing.T) {
		entry := log()
		require.NotNil(t, entry)
	})
}

// Test concurrent session operations
func TestConcurrentSessionOperations(t *testing.T) {
	t.Run("handles concurrent add and get", func(t *testing.T) {
		sm := &SessionManager{
			sessions: make(map[string]*TerminalSession),
		}

		var wg sync.WaitGroup
		numSessions := 100

		// Add sessions concurrently
		for i := 0; i < numSessions; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				session := &TerminalSession{
					UUID:      fmt.Sprintf("uuid-%d", idx),
					AgentName: "agent",
					Done:      make(chan struct{}),
				}
				sm.Add(session)
			}(i)
		}

		// Get sessions concurrently
		for i := 0; i < numSessions; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_ = sm.Get(fmt.Sprintf("uuid-%d", idx))
			}(i)
		}

		wg.Wait()
	})

	t.Run("handles concurrent add and remove", func(t *testing.T) {
		sm := &SessionManager{
			sessions: make(map[string]*TerminalSession),
		}

		var wg sync.WaitGroup
		numSessions := 100

		// Add sessions
		for i := 0; i < numSessions; i++ {
			session := &TerminalSession{
				UUID:      fmt.Sprintf("uuid-%d", i),
				AgentName: "agent",
				Done:      make(chan struct{}),
			}
			sm.sessions[fmt.Sprintf("uuid-%d", i)] = session
		}

		// Remove sessions concurrently
		for i := 0; i < numSessions; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				sm.Remove(fmt.Sprintf("uuid-%d", idx))
			}(i)
		}

		wg.Wait()

		// Verify all sessions are removed
		sm.mutex.RLock()
		assert.Empty(t, sm.sessions)
		sm.mutex.RUnlock()
	})
}
