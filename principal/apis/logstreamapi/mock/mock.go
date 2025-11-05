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
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/logstreamapi"
	"google.golang.org/grpc"
)

// MockLogStreamServer implements grpc.ClientStreamingServer for testing
type MockLogStreamServer struct {
	grpc.ServerStream
	ctx       context.Context
	recvData  []*logstreamapi.LogStreamData
	recvIndex int
	recvError error
	sendError error
	mu        sync.Mutex
	closed    bool
}

func NewMockLogStreamServer(ctx context.Context) *MockLogStreamServer {
	return &MockLogStreamServer{
		ctx:      ctx,
		recvData: make([]*logstreamapi.LogStreamData, 0),
	}
}

func (m *MockLogStreamServer) Context() context.Context {
	return m.ctx
}

func (m *MockLogStreamServer) Recv() (*logstreamapi.LogStreamData, error) {
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

	// Return the data as-is, even if it's nil
	return data, nil
}

func (m *MockLogStreamServer) SendAndClose(resp *logstreamapi.LogStreamResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("stream already closed")
	}

	m.closed = true
	return m.sendError
}

func (m *MockLogStreamServer) AddRecvData(data *logstreamapi.LogStreamData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvData = append(m.recvData, data)
}

func (m *MockLogStreamServer) SetRecvError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvError = err
}

func (m *MockLogStreamServer) SetSendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendError = err
}

func (m *MockLogStreamServer) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

// MockHTTPResponseWriter implements http.ResponseWriter and http.Flusher for testing
type MockHTTPResponseWriter struct {
	headers     http.Header
	body        strings.Builder
	statusCode  int
	flushCalled bool
}

func NewMockHTTPResponseWriter() *MockHTTPResponseWriter {
	return &MockHTTPResponseWriter{
		headers: make(http.Header),
	}
}

func (m *MockHTTPResponseWriter) Header() http.Header {
	return m.headers
}

func (m *MockHTTPResponseWriter) Write(data []byte) (int, error) {
	return m.body.Write(data)
}

func (m *MockHTTPResponseWriter) WriteHeader(statusCode int) {
	m.statusCode = statusCode
}

func (m *MockHTTPResponseWriter) Flush() {
	m.flushCalled = true
}

func (m *MockHTTPResponseWriter) GetBody() string {
	return m.body.String()
}

func (m *MockHTTPResponseWriter) GetStatusCode() int {
	return m.statusCode
}

func (m *MockHTTPResponseWriter) IsFlushCalled() bool {
	return m.flushCalled
}

func (m *MockHTTPResponseWriter) Reset() {
	m.body.Reset()
	m.flushCalled = false
	m.statusCode = 0
	m.headers = make(http.Header)
}

// PanicFlusher implements http.Flusher but panics on Flush()
type PanicFlusher struct{}

func (p *PanicFlusher) Flush() {
	panic("simulated panic")
}

// MockWriterWithoutFlusher implements http.ResponseWriter but NOT http.Flusher
type MockWriterWithoutFlusher struct {
	headers    http.Header
	body       strings.Builder
	statusCode int
}

func (m *MockWriterWithoutFlusher) Header() http.Header {
	if m.headers == nil {
		m.headers = make(http.Header)
	}
	return m.headers
}

func (m *MockWriterWithoutFlusher) Write(data []byte) (int, error) {
	return m.body.Write(data)
}

func (m *MockWriterWithoutFlusher) WriteHeader(statusCode int) {
	m.statusCode = statusCode
}
