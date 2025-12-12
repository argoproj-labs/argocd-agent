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
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/terminalstreamapi"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// processIncomingTerminalRequest handles incoming web terminal requests from the principal.
// It establishes a Kubernetes exec session with a shell running inside the application pod
// and bridges it with the gRPC stream.
func (a *Agent) processIncomingTerminalRequest(ev *event.Event) error {
	terminalReq, err := ev.TerminalRequest()
	if err != nil {
		return fmt.Errorf("failed to parse terminal request: %w", err)
	}

	// Ensure at a time only one web terminal is opened for an application.
	a.inflightMu.Lock()
	if a.inflightTerminal == nil {
		a.inflightTerminal = make(map[string]struct{})
	}
	if _, dup := a.inflightTerminal[terminalReq.UUID]; dup {
		a.inflightMu.Unlock()
		log().WithField("session_uuid", terminalReq.UUID).Warn("duplicate terminal request; already processing")
		return nil
	}
	a.inflightTerminal[terminalReq.UUID] = struct{}{}
	a.inflightMu.Unlock()
	defer func() {
		a.inflightMu.Lock()
		delete(a.inflightTerminal, terminalReq.UUID)
		a.inflightMu.Unlock()
	}()

	logCtx := log().WithFields(logrus.Fields{
		"method":       "processIncomingTerminalRequest",
		"session_uuid": terminalReq.UUID,
		"namespace":    terminalReq.Namespace,
		"pod":          terminalReq.PodName,
		"container":    terminalReq.ContainerName,
		"shell_name":   terminalReq.Command,
	})

	logCtx.Info("Processing terminal request")

	ctx, cancel := context.WithCancel(a.context)
	defer cancel()

	// Connect to principal's terminal stream service
	conn := a.remote.Conn()
	terminalStreamClient := terminalstreamapi.NewTerminalStreamServiceClient(conn)

	stream, err := terminalStreamClient.StreamTerminal(ctx)
	if err != nil {
		logCtx.WithError(err).Error("Failed to open terminal stream to principal")
		return err
	}

	logCtx.Info("Terminal stream to principal established")

	// Send initial empty message to establish the stream connection
	// This allows the principal to acknowledge the stream and prepare for terminal data
	if err := stream.Send(&terminalstreamapi.TerminalStreamData{RequestUuid: terminalReq.UUID}); err != nil {
		logCtx.WithError(err).Error("Failed to send initial terminal frame")
		return err
	}

	// Execute the K8s exec API call
	if err := a.terminalInPod(ctx, stream, terminalReq, logCtx); err != nil {
		if !isShellNotFoundError(err) {
			logCtx.WithError(err).Error("Terminal failed")
		}
		// Send error to principal so UI can try next shell
		_ = stream.Send(&terminalstreamapi.TerminalStreamData{
			RequestUuid: terminalReq.UUID,
			Error:       err.Error(),
		})
		return err
	}

	logCtx.Info("Terminal completed successfully")
	return nil
}

// terminalInPod executes a command in a pod and streams I/O via gRPC.
func (a *Agent) terminalInPod(ctx context.Context, stream terminalstreamapi.TerminalStreamService_StreamTerminalClient, terminalReq *event.ContainerTerminalRequest, logCtx *logrus.Entry) error {
	// Build Kubernetes exec request
	req := a.kubeClient.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(terminalReq.PodName).
		Namespace(terminalReq.Namespace).
		SubResource("exec")

	// Set exec options
	execOptions := &corev1.PodExecOptions{
		Container: terminalReq.ContainerName,
		Command:   terminalReq.Command,
		Stdin:     terminalReq.Stdin,
		Stdout:    terminalReq.Stdout,
		Stderr:    terminalReq.Stderr,
		TTY:       terminalReq.TTY,
	}

	req.VersionedParams(execOptions, scheme.ParameterCodec)

	logCtx.Infof("Executing command: %v", execOptions.Command)

	// Create WebSocket executor,
	// it connects agent to the shell running inside the application pod and streams data back and forth.
	exec, err := remotecommand.NewWebSocketExecutor(a.kubeClient.RestConfig, "GET", req.URL().String())
	if err != nil {
		return fmt.Errorf("failed to create WebSocket executor: %w", err)
	}

	// Create cancellable context for the exec
	// This allows us to terminate the exec when EOF is received from principal
	execCtx, cancelExec := context.WithCancel(ctx)
	defer cancelExec()

	// Create terminal size queue to send terminal size to the shell
	// This is required by shell to render the output content accordingly.
	sizeQueue := &terminalSizeQueue{
		sizes: make(chan *remotecommand.TerminalSize, 10),
	}

	// Send default terminal size to the shell
	sizeQueue.sizes <- &remotecommand.TerminalSize{
		Width:  80,
		Height: 24,
	}

	// Create stream handler to bridge K8s exec with gRPC
	streamHandler := &terminalStreamHandler{
		stream:      stream,
		sessionUUID: terminalReq.UUID,
		stdin:       make(chan []byte, 100),
		done:        make(chan struct{}),
		cancelExec:  cancelExec,
		sizeQueue:   sizeQueue,
		logCtx:      logCtx,
	}

	// Start goroutine to receive stdin from principal and forward to K8s
	go streamHandler.receiveFromPrincipal()

	// Execute the command in the pod using the stream handler
	err = exec.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdin:             streamHandler,
		Stdout:            streamHandler,
		Stderr:            streamHandler,
		Tty:               terminalReq.TTY,
		TerminalSizeQueue: sizeQueue,
	})

	// Close the stream to signal the end of the terminal session
	streamHandler.close()

	if err != nil {
		if !isShellNotFoundError(err) {
			logCtx.WithError(err).Error("StreamWithContext failed")
		}
		return err
	}

	// Send EOF to principal to signal the end of the terminal session
	_ = stream.Send(&terminalstreamapi.TerminalStreamData{
		RequestUuid: terminalReq.UUID,
		Eof:         true,
	})

	logCtx.Info("Terminal stream completed")
	return nil
}

// terminalStreamHandler implements io.Reader and io.Writer to bridge K8s exec with gRPC.
type terminalStreamHandler struct {
	stream      terminalstreamapi.TerminalStreamService_StreamTerminalClient
	sessionUUID string
	stdin       chan []byte
	done        chan struct{}
	cancelExec  context.CancelFunc
	sizeQueue   *terminalSizeQueue
	logCtx      *logrus.Entry
	closeOnce   sync.Once
}

// close safely closes the done channel to signal the end of the terminal session
func (h *terminalStreamHandler) close() {
	h.closeOnce.Do(func() {
		close(h.done)
	})
}

type terminalSizeQueue struct {
	sizes chan *remotecommand.TerminalSize
}

func (t *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-t.sizes
	if !ok {
		return nil
	}
	return size
}

// Read implements io.Reader to read stdin data from the gRPC stream.
func (h *terminalStreamHandler) Read(p []byte) (int, error) {
	select {
	case <-h.done:
		return 0, io.EOF
	case data, ok := <-h.stdin:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, data)
		h.logCtx.WithField("data_size", n).Debug("Shell read stdin data")
		return n, nil
	}
}

// Write implements io.Writer to write stdout/stderr data to the gRPC stream.
func (h *terminalStreamHandler) Write(p []byte) (int, error) {
	data := make([]byte, len(p))
	copy(data, p)

	h.logCtx.WithField("data_size", len(data)).Debug("Shell produced output, sending to principal")

	err := h.stream.Send(&terminalstreamapi.TerminalStreamData{
		RequestUuid: h.sessionUUID,
		Data:        data,
		StreamType:  "stdout",
	})

	if err != nil {
		h.logCtx.WithError(err).Error("Failed to send data to principal")
		return 0, err
	}

	return len(p), nil
}

// receiveFromPrincipal receives data from the principal and forwards to stdin channel.
func (h *terminalStreamHandler) receiveFromPrincipal() {
	defer func() {
		// Close done channel to signal the end of the terminal session
		h.close()
		close(h.stdin)
	}()

	for {
		select {
		case <-h.done:
			return
		default:
		}

		msg, err := h.stream.Recv()
		if err != nil {
			if err == io.EOF {
				h.logCtx.Info("Principal closed terminal stream")
			} else if isContextCanceledError(err) {
				h.logCtx.Debug("Terminal stream context canceled due to user closing the terminal")
			} else {
				h.logCtx.WithError(err).Error("Error receiving from principal")
			}
			// Stream closed, stop receiving data from principal
			return
		}

		if msg.Error != "" {
			h.logCtx.WithField("error", msg.Error).Error("Principal reported error, cancelling terminal")
			h.cancelExec()
			return
		}

		if msg.Eof {
			h.logCtx.Info("Principal sent EOF, closing stdin")
			// Close stdin channel to signal the end of the terminal session
			return
		}

		// Handle terminal resize event
		if msg.Resize {
			h.logCtx.WithFields(logrus.Fields{
				"cols": msg.Cols,
				"rows": msg.Rows,
			}).Debug("Received terminal resize from principal")
			// Send resize to terminal size queue
			select {
			case h.sizeQueue.sizes <- &remotecommand.TerminalSize{
				Width:  uint16(msg.Cols),
				Height: uint16(msg.Rows),
			}:
			case <-h.done:
				return
			default:
				// Queue full, skip this resize
				h.logCtx.Warn("Terminal size queue full, skipping resize")
			}
			continue
		}

		if len(msg.Data) > 0 && msg.StreamType == "stdin" {
			select {
			case h.stdin <- msg.Data:
			case <-h.done:
				return
			}
		}
	}
}

func isContextCanceledError(err error) bool {
	if err == nil {
		return false
	}
	if err == context.Canceled {
		return true
	}
	return strings.Contains(err.Error(), "context canceled")
}

// isShellNotFoundError checks if the error is due to a shell executable not being found in container.
func isShellNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "executable file not found") ||
		strings.Contains(errStr, "no such file or directory")
}
