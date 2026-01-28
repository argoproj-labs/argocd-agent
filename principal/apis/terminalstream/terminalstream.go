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

package terminalstream

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/terminalstreamapi"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// Server implements the TerminalStreamService gRPC server.
// It maintains active terminal sessions and streams data between WebSocket and gRPC.
type Server struct {
	terminalstreamapi.UnimplementedTerminalStreamServiceServer
	sessions *SessionManager
}

// SessionManager manages active terminal sessions.
type SessionManager struct {
	mutex    sync.RWMutex
	sessions map[string]*TerminalSession
}

// TerminalSession represents an active terminal session.
type TerminalSession struct {
	UUID      string
	AgentName string
	WSConn    *websocket.Conn
	ToAgent   chan *terminalstreamapi.TerminalStreamData
	FromAgent chan *terminalstreamapi.TerminalStreamData
	Done      chan struct{}
	CloseOnce sync.Once
}

// NewServer creates a new TerminalStream gRPC server.
func NewServer() *Server {
	return &Server{
		sessions: &SessionManager{
			sessions: make(map[string]*TerminalSession),
		},
	}
}

// StreamTerminal implements the bidirectional gRPC streaming for terminal sessions.
// This is called by the agent to establish the terminal stream.
func (s *Server) StreamTerminal(stream terminalstreamapi.TerminalStreamService_StreamTerminalServer) error {
	logCtx := log().WithField("method", "StreamTerminal")
	logCtx.Info("Agent connected to terminal stream")

	// Read the first message to get the session UUID.
	// This is the initial empty message to handshake and establish the stream connection.
	handshakeMsg, err := tryRecvWithCancel(stream.Context(), stream.Recv)
	if err != nil {
		logCtx.WithError(err).Error("Failed to receive handshake message from agent")
		return err
	}

	sessionUUID := handshakeMsg.RequestUuid
	if sessionUUID == "" {
		logCtx.Error("Handshake message contains empty session UUID")
		return fmt.Errorf("handshake message contains empty session UUID")
	}

	logCtx = logCtx.WithField("session_uuid", sessionUUID)

	// Get the session
	session := s.sessions.Get(sessionUUID)
	if session == nil {
		logCtx.Error("Session not found")
		return fmt.Errorf("session %s not found", sessionUUID)
	}

	logCtx.Info("Terminal session handshake completed with agent")

	// Handle incoming data from agent and send to WebSocket
	go func() {
		defer func() {
			session.Close()
			// Close the channels when agent finishes to terminate the terminal
			func() {
				defer func() { _ = recover() }()
				close(session.ToAgent)
			}()
			func() {
				defer func() { _ = recover() }()
				close(session.FromAgent)
			}()
			logCtx.Info("Agent to WebSocket goroutine exited, channels closed")
		}()

		// Send handshake message to WebSocket
		if err := s.sendToWebSocket(session, handshakeMsg, logCtx); err != nil {
			logCtx.WithError(err).Error("Failed to send handshake message to WebSocket")
			return
		}

		// Process subsequent messages after handshake is completed
		for {
			msg, err := tryRecvWithCancel(stream.Context(), stream.Recv)
			if err != nil {
				switch err {
				case io.EOF:
					logCtx.Info("Agent closed the terminal stream")
				case context.Canceled:
					// Context canceled is expected during cleanup when user closes the terminal
					logCtx.Debug("Terminal stream context canceled due to user closing the terminal")
				default:
					logCtx.WithError(err).Error("Error receiving data from agent")
				}
				return
			}

			logCtx.WithField("data_size", len(msg.Data)).Info("Received data from agent")

			if err := s.sendToWebSocket(session, msg, logCtx); err != nil {
				logCtx.WithError(err).Error("Failed to send data to WebSocket")
				return
			}
		}
	}()

	// Handle outgoing data from WebSocket to agent
	for {
		select {
		case <-stream.Context().Done():
			logCtx.Info("Terminal stream context done due to user closing the terminal")
			return stream.Context().Err()
		case msg, ok := <-session.ToAgent:
			if !ok {
				logCtx.Info("ToAgent channel closed, terminal stream terminated")
				return nil
			}

			if err := stream.Send(msg); err != nil {
				logCtx.WithError(err).Error("Failed to send data to agent")
				return err
			}
		}
	}
}

// sendToWebSocket sends data from the agent to webSocket channel.
// The agent to webSocket goroutine in processTerminalRequest will read from this channel.
func (s *Server) sendToWebSocket(session *TerminalSession, msg *terminalstreamapi.TerminalStreamData, logCtx *logrus.Entry) error {
	select {
	case session.FromAgent <- msg:
		logCtx.WithField("data_size", len(msg.Data)).Debug("Forwarded data from agent to webSocket")
		return nil
	case <-session.Done:
		logCtx.Debug("Terminal session closed, cannot forward to webSocket")
		return nil
	}
}

func (s *Server) RegisterSession(session *TerminalSession) {
	s.sessions.Add(session)
}

func (s *Server) UnregisterSession(sessionUUID string) {
	s.sessions.Remove(sessionUUID)
}

func (sm *SessionManager) Add(session *TerminalSession) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	sm.sessions[session.UUID] = session
}

func (sm *SessionManager) Get(uuid string) *TerminalSession {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.sessions[uuid]
}

func (sm *SessionManager) Remove(uuid string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	if session, exists := sm.sessions[uuid]; exists {
		session.Close()
		delete(sm.sessions, uuid)
	}
}

func (ts *TerminalSession) Close() {
	ts.CloseOnce.Do(func() {
		select {
		case <-ts.Done:
			// Do nothing if already closed
		default:
			close(ts.Done)
		}

		if ts.WSConn != nil {
			_ = ts.WSConn.Close()
		}
	})
}

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ModuleLogger("terminalstream")
}

// tryRecvWithCancel is a helper function to receive data from a stream with cancellation support.
// If user closes browser, ctx gets cancelled. Without tryRecvWithCancel: Recv() blocks forever, goroutine leaks.
// With tryRecvWithCancel: Returns immediately with ctx.Err().
func tryRecvWithCancel[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type res struct {
		m   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		m, err := fn()
		select {
		case ch <- res{m, err}: // send result if client is still listening
		case <-ctx.Done(): // otherwise just exit quietly
			return
		}
	}()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case r := <-ch:
		return r.m, r.err
	}
}
