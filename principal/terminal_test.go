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

package principal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/terminalstreamapi"
	"github.com/argoproj-labs/argocd-agent/principal/apis/terminalstream"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test streamChannel function
func TestStreamChannel(t *testing.T) {
	testCases := []struct {
		name       string
		streamType string
		expected   byte
	}{
		{
			name:       "stdout returns k8sChannelStdout",
			streamType: "stdout",
			expected:   K8sChannelStdout,
		},
		{
			name:       "stderr returns k8sChannelStderr",
			streamType: "stderr",
			expected:   K8sChannelStderr,
		},
		{
			name:       "error returns k8sChannelError",
			streamType: "error",
			expected:   K8sChannelError,
		},
		{
			name:       "empty string returns k8sChannelStdout (default)",
			streamType: "",
			expected:   K8sChannelStdout,
		},
		{
			name:       "unknown type returns k8sChannelStdout (default)",
			streamType: "unknown",
			expected:   K8sChannelStdout,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := streamChannel(tc.streamType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// Test parseK8sResizeFormat function
func TestParseK8sResizeFormat(t *testing.T) {
	testCases := []struct {
		name         string
		input        []byte
		expectedCols uint32
		expectedRows uint32
		expectedOk   bool
	}{
		{
			name:         "valid resize format",
			input:        []byte(`{"Width":120,"Height":40}`),
			expectedCols: 120,
			expectedRows: 40,
			expectedOk:   true,
		},
		{
			name:         "zero dimensions returns false",
			input:        []byte(`{"Width":0,"Height":0}`),
			expectedCols: 0,
			expectedRows: 0,
			expectedOk:   false,
		},
		{
			name:         "only width is zero",
			input:        []byte(`{"Width":0,"Height":24}`),
			expectedCols: 0,
			expectedRows: 24,
			expectedOk:   true,
		},
		{
			name:         "only height is zero",
			input:        []byte(`{"Width":80,"Height":0}`),
			expectedCols: 80,
			expectedRows: 0,
			expectedOk:   true,
		},
		{
			name:         "invalid JSON",
			input:        []byte(`invalid json`),
			expectedCols: 0,
			expectedRows: 0,
			expectedOk:   false,
		},
		{
			name:         "empty JSON",
			input:        []byte(`{}`),
			expectedCols: 0,
			expectedRows: 0,
			expectedOk:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows, ok := parseK8sResizeFormat(tc.input)
			assert.Equal(t, tc.expectedCols, cols)
			assert.Equal(t, tc.expectedRows, rows)
			assert.Equal(t, tc.expectedOk, ok)
		})
	}
}

// Test handleResizeMessage
func TestHandleResizeMessage(t *testing.T) {
	logCtx := logrus.NewEntry(logrus.New())
	s := &Server{}

	t.Run("handles k8s channel resize format", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		// K8s resize format: channel byte (4) followed by JSON
		resizeJSON := `{"Width":120,"Height":40}`
		data := append([]byte{K8sChannelResize}, []byte(resizeJSON)...)

		result := s.handleResizeMessage(session, data, logCtx)

		assert.True(t, result, "should return true for resize message")

		// Verify resize was sent to agent
		select {
		case msg := <-session.ToAgent:
			assert.True(t, msg.Resize)
			assert.Equal(t, uint32(120), msg.Cols)
			assert.Equal(t, uint32(40), msg.Rows)
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected resize message to be sent to agent")
		}
	})

	t.Run("handles operation resize format", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		resizeMsg := struct {
			Operation string `json:"operation"`
			Cols      uint32 `json:"cols"`
			Rows      uint32 `json:"rows"`
		}{
			Operation: "resize",
			Cols:      100,
			Rows:      30,
		}
		data, _ := json.Marshal(resizeMsg)

		result := s.handleResizeMessage(session, data, logCtx)

		assert.True(t, result, "should return true for operation resize message")

		// Verify resize was sent to agent
		select {
		case msg := <-session.ToAgent:
			assert.True(t, msg.Resize)
			assert.Equal(t, uint32(100), msg.Cols)
			assert.Equal(t, uint32(30), msg.Rows)
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected resize message to be sent to agent")
		}
	})

	t.Run("returns false for non-resize data", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		data := []byte("regular stdin data")

		result := s.handleResizeMessage(session, data, logCtx)

		assert.False(t, result, "should return false for non-resize data")
	})

	t.Run("returns false for empty data", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		result := s.handleResizeMessage(session, []byte{}, logCtx)

		assert.False(t, result, "should return false for empty data")
	})

	t.Run("handles JSON resize with channel prefix", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		// JSON with a channel prefix that's not the resize channel
		resizeJSON := `{"Width":80,"Height":24}`
		data := append([]byte{0}, []byte(resizeJSON)...) // Channel 0 (stdin) with JSON

		result := s.handleResizeMessage(session, data, logCtx)

		assert.True(t, result, "should return true for JSON resize with channel prefix")

		// Verify resize was sent
		select {
		case msg := <-session.ToAgent:
			assert.True(t, msg.Resize)
			assert.Equal(t, uint32(80), msg.Cols)
			assert.Equal(t, uint32(24), msg.Rows)
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected resize message to be sent to agent")
		}
	})
}

// Test notifyWebSocketError
func TestNotifyWebSocketError(t *testing.T) {
	logCtx := logrus.NewEntry(logrus.New())

	t.Run("does not send if done channel is closed", func(t *testing.T) {
		// Create a WebSocket server to handle the test
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			// Wait for message (should not receive any)
			_, _, err = conn.ReadMessage()
			assert.Error(t, err) // Should error because we close without sending
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer conn.Close()

		done := make(chan struct{})
		close(done) // Close immediately

		notifyWebSocketError(conn, done, "test error", logCtx)
		// Function should return without sending
	})
}

// Test notifyWebSocketEOF
func TestNotifyWebSocketEOF(t *testing.T) {
	logCtx := logrus.NewEntry(logrus.New())

	t.Run("does not send if done channel is closed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			// Wait for message (should not receive any)
			_, _, err = conn.ReadMessage()
			assert.Error(t, err)
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer conn.Close()

		done := make(chan struct{})
		close(done) // Close immediately

		notifyWebSocketEOF(conn, done, logCtx)
		// Function should return without sending
	})
}

// Test closeTerminalStreamChannels
func TestCloseTerminalStreamChannels(t *testing.T) {
	t.Run("safely closes both channels", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, 10),
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		// Close should not panic
		closeTerminalStreamChannels(session)

		// Verify channels are closed
		select {
		case _, ok := <-session.ToAgent:
			assert.False(t, ok, "ToAgent channel should be closed")
		default:
			t.Error("ToAgent channel should be closed and readable")
		}

		select {
		case _, ok := <-session.FromAgent:
			assert.False(t, ok, "FromAgent channel should be closed")
		default:
			t.Error("FromAgent channel should be closed and readable")
		}
	})

	t.Run("safe to call multiple times", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, 10),
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:      make(chan struct{}),
		}

		// Should not panic on multiple calls
		closeTerminalStreamChannels(session)
		closeTerminalStreamChannels(session)
	})
}

// Test safeCloseTerminalStreamDataChan
func TestSafeCloseTerminalStreamDataChan(t *testing.T) {
	t.Run("closes open channel", func(t *testing.T) {
		ch := make(chan *terminalstreamapi.TerminalStreamData, 10)
		safeCloseTerminalStreamDataChan(ch)

		select {
		case _, ok := <-ch:
			assert.False(t, ok, "channel should be closed")
		default:
			t.Error("channel should be readable after close")
		}
	})

	t.Run("does not panic on already closed channel", func(t *testing.T) {
		ch := make(chan *terminalstreamapi.TerminalStreamData, 10)
		close(ch)

		// Should not panic
		safeCloseTerminalStreamDataChan(ch)
	})
}

// Test sendResizeToAgent
func TestSendResizeToAgent(t *testing.T) {
	logCtx := logrus.NewEntry(logrus.New())
	s := &Server{}

	t.Run("sends resize message to agent", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		s.sendResizeToAgent(session, 120, 40, logCtx)

		select {
		case msg := <-session.ToAgent:
			assert.Equal(t, "test-uuid", msg.RequestUuid)
			assert.True(t, msg.Resize)
			assert.Equal(t, uint32(120), msg.Cols)
			assert.Equal(t, uint32(40), msg.Rows)
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected resize message to be sent")
		}
	})

	t.Run("does not block when done channel is closed", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData), // Unbuffered to test blocking
			Done:    make(chan struct{}),
		}

		close(session.Done)

		// This should not block
		done := make(chan struct{})
		go func() {
			s.sendResizeToAgent(session, 120, 40, logCtx)
			close(done)
		}()

		select {
		case <-done:
			// Expected
		case <-time.After(100 * time.Millisecond):
			t.Error("sendResizeToAgent should not block when done is closed")
		}
	})
}

// Test agentToWebSocketChannel goroutine behavior
func TestAgentToWebSocketChannelBehavior(t *testing.T) {
	t.Run("handles FromAgent channel close", func(t *testing.T) {
		// Create a test WebSocket server that keeps connection open
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			// Keep connection open until test completes
			time.Sleep(2 * time.Second)
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer clientConn.Close()

		session := &terminalstream.TerminalSession{
			WSConn:    clientConn,
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData),
			Done:      make(chan struct{}),
		}

		s := &Server{}
		logCtx := logrus.NewEntry(logrus.New())

		// Run agentToWebSocketChannel in goroutine
		done := make(chan struct{})
		go func() {
			s.agentToWebSocketChannel(session, logCtx)
			close(done)
		}()

		// Close FromAgent channel - should cause goroutine to exit
		close(session.FromAgent)

		// Verify the goroutine exits
		select {
		case <-done:
			// Expected - goroutine exited
		case <-time.After(1 * time.Second):
			t.Error("agentToWebSocketChannel should exit when FromAgent is closed")
		}
	})

	t.Run("forwards data to WebSocket", func(t *testing.T) {
		received := make(chan []byte, 1)

		// Create a test WebSocket server that captures received data
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_, data, err := conn.ReadMessage()
			if err == nil {
				received <- data
			}
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer clientConn.Close()

		session := &terminalstream.TerminalSession{
			WSConn:    clientConn,
			FromAgent: make(chan *terminalstreamapi.TerminalStreamData, 1),
			Done:      make(chan struct{}),
		}

		s := &Server{}
		logCtx := logrus.NewEntry(logrus.New())

		// Run agentToWebSocketChannel in goroutine
		go s.agentToWebSocketChannel(session, logCtx)

		// Send data through FromAgent channel
		session.FromAgent <- &terminalstreamapi.TerminalStreamData{
			Data:       []byte("test output"),
			StreamType: "stdout",
		}

		// Verify data was forwarded to WebSocket
		select {
		case data := <-received:
			// First byte is channel (1 for stdout), rest is data
			assert.Equal(t, byte(K8sChannelStdout), data[0])
			assert.Equal(t, []byte("test output"), data[1:])
		case <-time.After(1 * time.Second):
			t.Error("Expected data to be forwarded to WebSocket")
		}

		close(session.FromAgent)
	})
}

// Test terminal constants
func TestTerminalConstants(t *testing.T) {
	// Verify K8s channel constants are correct
	assert.EqualValues(t, 0, K8sChannelStdin)
	assert.EqualValues(t, 1, K8sChannelStdout)
	assert.EqualValues(t, 2, K8sChannelStderr)
	assert.EqualValues(t, 3, K8sChannelError)
	assert.EqualValues(t, 4, K8sChannelResize)

	// Verify other constants
	assert.Equal(t, 100, terminalChannelBufferSize)
	assert.Equal(t, 30*time.Minute, terminalSessionTimeout)
}

// Test handleK8sResizeMessage
func TestHandleK8sResizeMessage(t *testing.T) {
	logCtx := logrus.NewEntry(logrus.New())
	s := &Server{}

	t.Run("handles valid K8s resize JSON", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		resizeJSON := []byte(`{"Width":120,"Height":40}`)
		result := s.handleK8sResizeMessage(session, resizeJSON, logCtx)

		assert.True(t, result)

		select {
		case msg := <-session.ToAgent:
			assert.True(t, msg.Resize)
			assert.Equal(t, uint32(120), msg.Cols)
			assert.Equal(t, uint32(40), msg.Rows)
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected resize message")
		}
	})

	t.Run("returns false for invalid JSON", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		result := s.handleK8sResizeMessage(session, []byte("invalid"), logCtx)
		assert.False(t, result)
	})
}

// Test handleJSONResizeMessage
func TestHandleJSONResizeMessage(t *testing.T) {
	logCtx := logrus.NewEntry(logrus.New())
	s := &Server{}

	t.Run("handles K8s format JSON", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		resizeJSON := []byte(`{"Width":80,"Height":24}`)
		result := s.handleJSONResizeMessage(session, resizeJSON, logCtx)

		assert.True(t, result)

		select {
		case msg := <-session.ToAgent:
			assert.True(t, msg.Resize)
			assert.Equal(t, uint32(80), msg.Cols)
			assert.Equal(t, uint32(24), msg.Rows)
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected resize message")
		}
	})

	t.Run("handles operation format JSON", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		resizeMsg := map[string]interface{}{
			"operation": "resize",
			"cols":      float64(100),
			"rows":      float64(30),
		}
		resizeJSON, _ := json.Marshal(resizeMsg)
		result := s.handleJSONResizeMessage(session, resizeJSON, logCtx)

		assert.True(t, result)

		select {
		case msg := <-session.ToAgent:
			assert.True(t, msg.Resize)
			assert.Equal(t, uint32(100), msg.Cols)
			assert.Equal(t, uint32(30), msg.Rows)
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected resize message")
		}
	})

	t.Run("returns false for non-resize operation", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		msg := map[string]interface{}{
			"operation": "other",
			"cols":      float64(100),
			"rows":      float64(30),
		}
		msgJSON, _ := json.Marshal(msg)
		result := s.handleJSONResizeMessage(session, msgJSON, logCtx)

		assert.False(t, result)
	})

	t.Run("returns false for invalid JSON", func(t *testing.T) {
		session := &terminalstream.TerminalSession{
			UUID:    "test-uuid",
			ToAgent: make(chan *terminalstreamapi.TerminalStreamData, 10),
			Done:    make(chan struct{}),
		}

		result := s.handleJSONResizeMessage(session, []byte("not json"), logCtx)
		assert.False(t, result)
	})
}
