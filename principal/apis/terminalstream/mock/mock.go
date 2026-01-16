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

package mock

import (
	"context"
	"io"
	"sync"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/terminalstreamapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// MockTerminalStreamServer implements the gRPC bidirectional stream server interface for testing
type MockTerminalStreamServer struct {
	grpc.ServerStream
	ctx       context.Context
	recvData  []*terminalstreamapi.TerminalStreamData
	sentData  []*terminalstreamapi.TerminalStreamData
	recvIndex int
	recvError error
	sendError error
	recvFunc  func() (*terminalstreamapi.TerminalStreamData, error)
	sendFunc  func(*terminalstreamapi.TerminalStreamData) error
	mu        sync.RWMutex
	closed    bool
}

func NewMockTerminalStreamServer(ctx context.Context) *MockTerminalStreamServer {
	return &MockTerminalStreamServer{
		ctx:      ctx,
		recvData: make([]*terminalstreamapi.TerminalStreamData, 0),
		sentData: make([]*terminalstreamapi.TerminalStreamData, 0),
	}
}

func (m *MockTerminalStreamServer) Context() context.Context {
	return m.ctx
}

func (m *MockTerminalStreamServer) Send(data *terminalstreamapi.TerminalStreamData) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sendFunc != nil {
		return m.sendFunc(data)
	}

	if m.sendError != nil {
		return m.sendError
	}

	m.sentData = append(m.sentData, data)
	return nil
}

func (m *MockTerminalStreamServer) Recv() (*terminalstreamapi.TerminalStreamData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.recvFunc != nil {
		return m.recvFunc()
	}

	if m.closed {
		return nil, io.EOF
	}

	if m.recvError != nil {
		return nil, m.recvError
	}

	if m.recvIndex >= len(m.recvData) {
		return nil, io.EOF
	}

	data := m.recvData[m.recvIndex]
	m.recvIndex++
	return data, nil
}

func (m *MockTerminalStreamServer) AddRecvData(data *terminalstreamapi.TerminalStreamData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvData = append(m.recvData, data)
}

func (m *MockTerminalStreamServer) GetSentData() []*terminalstreamapi.TerminalStreamData {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*terminalstreamapi.TerminalStreamData, len(m.sentData))
	copy(result, m.sentData)
	return result
}

func (m *MockTerminalStreamServer) SetRecvError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvError = err
}

func (m *MockTerminalStreamServer) SetSendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendError = err
}

func (m *MockTerminalStreamServer) SetRecvFunc(fn func() (*terminalstreamapi.TerminalStreamData, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvFunc = fn
}

func (m *MockTerminalStreamServer) SetSendFunc(fn func(*terminalstreamapi.TerminalStreamData) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendFunc = fn
}

func (m *MockTerminalStreamServer) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

func (m *MockTerminalStreamServer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvData = make([]*terminalstreamapi.TerminalStreamData, 0)
	m.sentData = make([]*terminalstreamapi.TerminalStreamData, 0)
	m.recvIndex = 0
	m.recvError = nil
	m.sendError = nil
	m.recvFunc = nil
	m.sendFunc = nil
	m.closed = false
}

// MockTerminalStreamClient implements the gRPC bidirectional stream client interface for testing.
// This is used by the agent to send/receive data to/from the principal.
type MockTerminalStreamClient struct {
	ctx         context.Context
	sentData    []*terminalstreamapi.TerminalStreamData
	recvData    []*terminalstreamapi.TerminalStreamData
	recvIndex   int
	recvError   error
	sendError   error
	recvFunc    func() (*terminalstreamapi.TerminalStreamData, error)
	sendFunc    func(*terminalstreamapi.TerminalStreamData) error
	closeSendFn func() error
	mu          sync.RWMutex
	closed      bool
}

func NewMockTerminalStreamClient(ctx context.Context) *MockTerminalStreamClient {
	return &MockTerminalStreamClient{
		ctx:      ctx,
		sentData: make([]*terminalstreamapi.TerminalStreamData, 0),
		recvData: make([]*terminalstreamapi.TerminalStreamData, 0),
		closeSendFn: func() error {
			return nil
		},
	}
}

func (m *MockTerminalStreamClient) Send(data *terminalstreamapi.TerminalStreamData) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sendFunc != nil {
		err := m.sendFunc(data)
		if err == nil {
			m.sentData = append(m.sentData, data)
		}
		return err
	}

	if m.sendError != nil {
		return m.sendError
	}

	m.sentData = append(m.sentData, data)
	return nil
}

func (m *MockTerminalStreamClient) Recv() (*terminalstreamapi.TerminalStreamData, error) {
	m.mu.Lock()
	recvFunc := m.recvFunc
	m.mu.Unlock()

	if recvFunc != nil {
		return recvFunc()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, io.EOF
	}

	if m.recvError != nil {
		return nil, m.recvError
	}

	if m.recvIndex >= len(m.recvData) {
		return nil, io.EOF
	}

	data := m.recvData[m.recvIndex]
	m.recvIndex++
	return data, nil
}

func (m *MockTerminalStreamClient) Header() (metadata.MD, error) {
	return metadata.New(nil), nil
}

func (m *MockTerminalStreamClient) Trailer() metadata.MD {
	return metadata.New(nil)
}

func (m *MockTerminalStreamClient) CloseSend() error {
	m.mu.Lock()
	closeSendFn := m.closeSendFn
	m.mu.Unlock()

	if closeSendFn != nil {
		return closeSendFn()
	}
	return nil
}

func (m *MockTerminalStreamClient) Context() context.Context {
	return m.ctx
}

func (m *MockTerminalStreamClient) SendMsg(msg interface{}) error {
	return nil
}

func (m *MockTerminalStreamClient) RecvMsg(msg interface{}) error {
	return nil
}

func (m *MockTerminalStreamClient) AddRecvData(data *terminalstreamapi.TerminalStreamData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvData = append(m.recvData, data)
}

func (m *MockTerminalStreamClient) GetSentData() []*terminalstreamapi.TerminalStreamData {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*terminalstreamapi.TerminalStreamData, len(m.sentData))
	copy(result, m.sentData)
	return result
}

func (m *MockTerminalStreamClient) SetRecvData(data []*terminalstreamapi.TerminalStreamData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvData = data
	m.recvIndex = 0
}

func (m *MockTerminalStreamClient) SetRecvError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvError = err
}

func (m *MockTerminalStreamClient) SetSendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendError = err
}

func (m *MockTerminalStreamClient) SetRecvFunc(fn func() (*terminalstreamapi.TerminalStreamData, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvFunc = fn
}

func (m *MockTerminalStreamClient) SetSendFunc(fn func(*terminalstreamapi.TerminalStreamData) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendFunc = fn
}

func (m *MockTerminalStreamClient) SetCloseSendFunc(fn func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeSendFn = fn
}

func (m *MockTerminalStreamClient) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

func (m *MockTerminalStreamClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentData = make([]*terminalstreamapi.TerminalStreamData, 0)
	m.recvData = make([]*terminalstreamapi.TerminalStreamData, 0)
	m.recvIndex = 0
	m.recvError = nil
	m.sendError = nil
	m.recvFunc = nil
	m.sendFunc = nil
	m.closed = false
}
