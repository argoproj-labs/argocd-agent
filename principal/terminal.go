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

package principal

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/terminalstreamapi"
	"github.com/argoproj-labs/argocd-agent/principal/apis/terminalstream"
	"github.com/argoproj-labs/argocd-agent/principal/resourceproxy"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	remotecommandconsts "k8s.io/apimachinery/pkg/util/remotecommand"
)

const (
	K8sChannelStdin           = 0
	K8sChannelStdout          = 1
	K8sChannelStderr          = 2
	K8sChannelError           = 3
	K8sChannelResize          = 4
	terminalChannelBufferSize = 100
	terminalSessionTimeout    = 30 * time.Minute
)

// terminalUpgrader will convert HTTP web terminal requests into WebSocket.
var terminalUpgrader = websocket.Upgrader{
	Subprotocols: append(
		[]string{remotecommandconsts.StreamProtocolV5Name},
		remotecommandconsts.SupportedStreamingProtocols...,
	),
	CheckOrigin: func(r *http.Request) bool {
		// Nice to have a logic to allow only ArgoCD UI to connect.
		// Though incoming http request is processed only after client certificate validation.
		// Hence it is safe to open the WebSocket connection for incoming requests.
		return true
	},
}

// processTerminalRequest handles a web terminal request by upgrading the HTTP connection to a WebSocket,
// verifying agent connection, and implementing web terminal streaming to a target pod/container via the connected agent.
func (s *Server) processTerminalRequest(w http.ResponseWriter, r *http.Request, params resourceproxy.Params, agentName string) {
	logCtx := log().WithField("function", "processTerminalRequest")

	if !s.agentMode(agentName).IsManaged() {
		http.Error(w, "web terminal is only supported for managed agents", http.StatusBadRequest)
		return
	}

	// Check agent connectivity before WebSocket upgrade
	if !s.queues.HasQueuePair(agentName) {
		logCtx.WithField("agent", agentName).Debug("Agent is not connected, cannot start terminal session")
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	namespace := params.Get("namespace")
	podName := params.Get("name")
	containerName := r.URL.Query().Get("container")
	command := r.URL.Query()["command"]

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.Namespace: namespace,
		"pod":               podName,
		"container":         containerName,
		"shell_name":        command,
	})

	logCtx.Info("Processing web terminal request")

	// Upgrade HTTP request to WebSocket
	// WebSocket connection is used to stream data back and forth between browser and principal
	// This is a bidirectional connection which stays open for the duration of the web terminal session
	wsConn, err := terminalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		logCtx.WithError(err).Error("Failed to upgrade to WebSocket")
		return
	}
	defer wsConn.Close()

	logCtx.Info("WebSocket connection upgraded")

	// Create a unique session UUID
	sessionUUID := uuid.NewString()
	logCtx = logCtx.WithField("session_uuid", sessionUUID)

	// Create web terminal request
	terminalReq := &event.ContainerTerminalRequest{
		UUID:          sessionUUID,
		Namespace:     namespace,
		PodName:       podName,
		ContainerName: containerName,
		Command:       command,
		TTY:           true,
		Stdin:         true,
		Stdout:        true,
		Stderr:        true,
	}

	// Create web terminal session
	session := &terminalstream.TerminalSession{
		UUID:      sessionUUID,
		AgentName: agentName,
		WSConn:    wsConn,
		ToAgent:   make(chan *terminalstreamapi.TerminalStreamData, terminalChannelBufferSize),
		FromAgent: make(chan *terminalstreamapi.TerminalStreamData, terminalChannelBufferSize),
		Done:      make(chan struct{}),
	}

	// Register the session
	s.terminalStreamServer.RegisterSession(session)
	defer s.terminalStreamServer.UnregisterSession(sessionUUID)

	logCtx.Info("Web terminal session registered")

	// Send web terminal request event to agent
	terminalEvent, err := s.events.NewTerminalRequestEvent(terminalReq)
	if err != nil {
		logCtx.WithError(err).Error("Failed to create web terminal request event")
		return
	}

	q := s.queues.SendQ(agentName)
	if q == nil {
		logCtx.Warn("Agent disconnected after WebSocket upgrade, closing terminal session")
		_ = wsConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, "agent disconnected"))
		return
	}

	q.Add(terminalEvent)
	logCtx.Info("Web terminal request event sent to agent")

	go s.agentToWebSocketChannel(session, logCtx)
	go s.webSocketToAgentChannel(session, logCtx)

	ctx, cancel := context.WithTimeout(s.ctx, terminalSessionTimeout)
	defer cancel()

	// Wait for session to complete or time out
	select {
	case <-session.Done:
		logCtx.Info("Web terminal session completed, cleaning up")
	case <-ctx.Done():
		logCtx.Warn("Web terminal session timeout after 30 minutes")
	}

	// Ensure channels are closed to terminate the agent web terminal stream
	closeTerminalStreamChannels(session)

	logCtx.Info("Web terminal session unregistered")
}

// webSocketToAgentChannel is a goroutine that reads data from the WebSocket for the browser and writes it to the agent.
// This data is input to commands executed in the application pod running in managed cluster.
func (s *Server) webSocketToAgentChannel(session *terminalstream.TerminalSession, logCtx *logrus.Entry) {
	wsConn := session.WSConn
	defer func() {
		logCtx.Info("WebSocket to Agent channel goroutine exited")
		session.Close()
	}()

	for {
		select {
		case <-session.Done:
			return
		default:
		}

		messageType, data, err := wsConn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				logCtx.WithError(err).Warn("WebSocket closed unexpectedly")
			} else {
				logCtx.Info("WebSocket closed")
			}
			return
		}

		if messageType != websocket.BinaryMessage && messageType != websocket.TextMessage {
			continue
		}

		if s.handleResizeMessage(session, data, logCtx) {
			continue
		}

		// Get stdin data from the message
		var stdinData []byte
		if len(data) > 0 && data[0] == K8sChannelStdin {
			stdinData = data[1:]
		} else {
			stdinData = data
		}

		select {
		case session.ToAgent <- &terminalstreamapi.TerminalStreamData{
			RequestUuid: session.UUID,
			Data:        stdinData,
			StreamType:  "stdin",
		}:
		case <-session.Done:
			return
		}
	}
}

// agentToWebSocketChannel is a goroutine that reads data from the agent and writes it to the WebSocket for the browser.
// This data is output of commands executed in the application pod running in managed cluster.
func (s *Server) agentToWebSocketChannel(session *terminalstream.TerminalSession, logCtx *logrus.Entry) {
	wsConn := session.WSConn
	defer func() {
		logCtx.Info("Agent to WebSocket channel goroutine exited")
		session.Close()
	}()

	for {
		data, ok := <-session.FromAgent
		if !ok {
			logCtx.Info("FromAgent channel closed")
			return
		}

		if data.Error != "" {
			logCtx.WithField("error", data.Error).Error("Agent reported error, closing web terminal session")
			notifyWebSocketError(wsConn, session.Done, data.Error, logCtx)
			return
		}

		if data.Eof {
			logCtx.Info("Agent sent EOF, closing web terminal session")
			notifyWebSocketEOF(wsConn, session.Done, logCtx)
			return
		}

		if len(data.Data) == 0 {
			continue
		}

		channel := streamChannel(data.StreamType)
		wsData := make([]byte, len(data.Data)+1)
		wsData[0] = channel
		copy(wsData[1:], data.Data)

		select {
		case <-session.Done:
			logCtx.WithField("data_size", len(data.Data)).WithField("channel", channel).Debug("Dropping data after web terminal session closed")
			return
		default:
			if err := wsConn.WriteMessage(websocket.BinaryMessage, wsData); err != nil {
				logCtx.WithError(err).Error("Failed to write to WebSocket, closing web terminal session")
				return
			}
			logCtx.WithField("data_size", len(data.Data)).WithField("channel", channel).Debug("Sent data to WebSocket")
		}
	}
}

// handleResizeMessage handles terminal resize messages from the ArgoCD UI browser.
// It is needed for shell running in application pod to render the output content accordingly.
// This supports both K8s exec protocol and ArgoCD UI resize format.
func (s *Server) handleResizeMessage(session *terminalstream.TerminalSession, data []byte, logCtx *logrus.Entry) bool {
	if len(data) == 0 {
		return false
	}

	// Check for K8s exec channel format (first byte is channel number)
	// Channel 4 is the resize channel in K8s exec protocol
	if data[0] == K8sChannelResize && len(data) > 1 {
		return s.handleK8sResizeMessage(session, data[1:], logCtx)
	}

	// Check for JSON resize messages
	payload := data
	// If first byte looks like a channel prefix (0-4), strip it
	if len(data) > 1 && data[0] < K8sChannelResize && data[1] == '{' {
		payload = data[1:]
	}

	if len(payload) == 0 || payload[0] != '{' {
		return false
	}

	return s.handleJSONResizeMessage(session, payload, logCtx)
}

func (s *Server) handleK8sResizeMessage(session *terminalstream.TerminalSession, data []byte, logCtx *logrus.Entry) bool {
	cols, rows, ok := parseK8sResizeFormat(data)
	if !ok {
		return false
	}
	s.sendResizeToAgent(session, cols, rows, logCtx)
	return true
}

func (s *Server) handleJSONResizeMessage(session *terminalstream.TerminalSession, data []byte, logCtx *logrus.Entry) bool {
	// Try K8s format first: {"Width":cols,"Height":rows}
	if cols, rows, ok := parseK8sResizeFormat(data); ok {
		s.sendResizeToAgent(session, cols, rows, logCtx)
		return true
	}

	// Try operation format: {"operation":"resize","cols":...,"rows":...}
	var resizeMsg struct {
		Operation string `json:"operation"`
		Cols      uint32 `json:"cols"`
		Rows      uint32 `json:"rows"`
	}

	if err := json.Unmarshal(data, &resizeMsg); err != nil || resizeMsg.Operation != "resize" {
		return false
	}

	s.sendResizeToAgent(session, resizeMsg.Cols, resizeMsg.Rows, logCtx)
	return true
}

// parseK8sResizeFormat parses the Kubernetes resize message format: {"Width":cols,"Height":rows}
func parseK8sResizeFormat(data []byte) (cols, rows uint32, ok bool) {
	var k8sResize struct {
		Width  uint32 `json:"Width"`
		Height uint32 `json:"Height"`
	}
	if err := json.Unmarshal(data, &k8sResize); err != nil {
		return 0, 0, false
	}
	if k8sResize.Width == 0 && k8sResize.Height == 0 {
		return 0, 0, false
	}
	return k8sResize.Width, k8sResize.Height, true
}

// sendResizeToAgent sends a resize event to the agent
func (s *Server) sendResizeToAgent(session *terminalstream.TerminalSession, cols, rows uint32, logCtx *logrus.Entry) {
	logCtx.WithFields(logrus.Fields{
		"cols": cols,
		"rows": rows,
	}).Debug("Sending terminal resize to agent")

	select {
	case session.ToAgent <- &terminalstreamapi.TerminalStreamData{
		RequestUuid: session.UUID,
		Resize:      true,
		Cols:        cols,
		Rows:        rows,
	}:
	case <-session.Done:
	}
}

// streamChannel converts the stream type to the corresponding channel number
func streamChannel(streamType string) byte {
	switch streamType {
	case "stderr":
		return K8sChannelStderr
	case "error":
		return K8sChannelError
	default:
		return K8sChannelStdout
	}
}

// notifyWebSocketError sends an error message to the WebSocket
func notifyWebSocketError(wsConn *websocket.Conn, done <-chan struct{}, msg string, logCtx *logrus.Entry) {
	select {
	case <-done:
		logCtx.Debug("WebSocket closed, cannot send error")
	default:
		_ = wsConn.WriteMessage(websocket.TextMessage, []byte(msg))
	}
}

// notifyWebSocketEOF sends an EOF message to the WebSocket
func notifyWebSocketEOF(wsConn *websocket.Conn, done <-chan struct{}, logCtx *logrus.Entry) {
	select {
	case <-done:
		logCtx.Debug("WebSocket closed, cannot send EOF")
	default:
		_ = wsConn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
	}
}

// closeTerminalStreamChannels closes the channels for the web terminal session
func closeTerminalStreamChannels(session *terminalstream.TerminalSession) {
	safeCloseTerminalStreamDataChan(session.ToAgent)
	safeCloseTerminalStreamDataChan(session.FromAgent)
}

// safeCloseTerminalStreamDataChan safely closes the channel
func safeCloseTerminalStreamDataChan(ch chan *terminalstreamapi.TerminalStreamData) {
	defer func() { _ = recover() }()
	close(ch)
}
