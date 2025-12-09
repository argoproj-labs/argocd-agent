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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/logstreamapi"
	"github.com/argoproj-labs/argocd-agent/principal/apis/logstreamapi/mock"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MockLogStreamClient wraps the existing MockLogStreamServer for client-side testing
type MockLogStreamClient struct {
	*mock.MockLogStreamServer
	sentData  []*logstreamapi.LogStreamData
	sendFunc  func(data *logstreamapi.LogStreamData) error
	requestID string
	mu        sync.RWMutex
}

func NewMockLogStreamClient(ctx context.Context, requestUUID string) *MockLogStreamClient {
	return &MockLogStreamClient{
		MockLogStreamServer: mock.NewMockLogStreamServer(ctx),
		sentData:            make([]*logstreamapi.LogStreamData, 0),
		requestID:           requestUUID,
		sendFunc: func(data *logstreamapi.LogStreamData) error {
			return nil
		},
	}
}

func (m *MockLogStreamClient) Send(data *logstreamapi.LogStreamData) error {
	// For client-side testing, we track sent data
	m.mu.Lock()
	m.sentData = append(m.sentData, data)
	m.mu.Unlock()
	return m.sendFunc(data)
}

func (m *MockLogStreamClient) CloseSend() error {
	return nil
}

func (m *MockLogStreamClient) Header() (metadata.MD, error) {
	return metadata.New(nil), nil
}

func (m *MockLogStreamClient) Trailer() metadata.MD {
	return metadata.New(nil)
}

func (m *MockLogStreamClient) SendMsg(msg interface{}) error {
	return nil
}

func (m *MockLogStreamClient) RecvMsg(msg interface{}) error {
	return nil
}

func (m *MockLogStreamClient) CloseAndRecv() (*logstreamapi.LogStreamResponse, error) {
	// Simulate closing and receiving a response
	m.mu.RLock()
	linesReceived := len(m.sentData)
	requestUUID := m.requestID
	if linesReceived > 0 && m.sentData[0] != nil && m.sentData[0].RequestUuid != "" {
		requestUUID = m.sentData[0].RequestUuid
	}
	m.mu.RUnlock()
	return &logstreamapi.LogStreamResponse{
		RequestUuid:   requestUUID,
		Status:        200,
		LinesReceived: int32(linesReceived),
	}, nil
}

func (m *MockLogStreamClient) GetSentData() []*logstreamapi.LogStreamData {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a copy to avoid race conditions
	result := make([]*logstreamapi.LogStreamData, len(m.sentData))
	copy(result, m.sentData)
	return result
}

func (m *MockLogStreamClient) Reset() {
	m.mu.Lock()
	m.sentData = make([]*logstreamapi.LogStreamData, 0)
	m.mu.Unlock()
}

func (m *MockLogStreamClient) SetSendFunc(fn func(data *logstreamapi.LogStreamData) error) {
	m.sendFunc = fn
}

// MockReadCloser implements io.ReadCloser for testing
type MockReadCloser struct {
	io.Reader
	closed bool
}

func (m *MockReadCloser) Close() error {
	m.closed = true
	return nil
}

func (m *MockReadCloser) IsClosed() bool {
	return m.closed
}

// Test helper functions
func createTestAgent() *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	agent := &Agent{
		context:      ctx,
		cancelFn:     cancel,
		inflightLogs: make(map[string]struct{}),
		inflightMu:   sync.Mutex{},
	}
	return agent
}

func createTestAgentWithKubeClient() *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	kubeClient := kube.NewKubernetesFakeClientWithResources()
	agent := &Agent{
		context:      ctx,
		cancelFn:     cancel,
		kubeClient:   kubeClient,
		inflightLogs: make(map[string]struct{}),
		inflightMu:   sync.Mutex{},
	}
	return agent
}

func createTestLogRequest(follow bool) *event.ContainerLogRequest {
	return &event.ContainerLogRequest{
		UUID:       uuid.New().String(),
		Namespace:  "test-namespace",
		PodName:    "test-pod",
		Container:  "test-container",
		Follow:     follow,
		Timestamps: true,
	}
}

// Test extractTimestamp function
func TestExtractTimestamp(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *time.Time
	}{
		{
			name:     "RFC3339 with nanoseconds",
			input:    "2025-12-07T10:30:45.123456789Z some log message",
			expected: timePtr(time.Date(2025, 12, 7, 10, 30, 45, 123456789, time.UTC)),
		},
		{
			name:     "RFC3339 without nanoseconds",
			input:    "2025-12-07T10:30:45Z some log message",
			expected: timePtr(time.Date(2025, 12, 7, 10, 30, 45, 0, time.UTC)),
		},
		{
			name:     "RFC3339 with milliseconds",
			input:    "2025-12-07T10:30:45.123Z some log message",
			expected: timePtr(time.Date(2025, 12, 7, 10, 30, 45, 123000000, time.UTC)),
		},
		{
			name:     "no timestamp",
			input:    "some log message without timestamp",
			expected: nil,
		},
		{
			name:     "timestamp with tab separator",
			input:    "2025-12-07T10:30:45Z\tsome log message",
			expected: timePtr(time.Date(2025, 12, 7, 10, 30, 45, 0, time.UTC)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTimestamp(tt.input)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, *tt.expected, *result)
			}
		})
	}
}

// Test createKubernetesLogStream
func TestCreateKubernetesLogStream(t *testing.T) {
	ctx := context.Background()
	logReq := createTestLogRequest(false)

	t.Run("Test createKubernetesLogStream with fake kube client and pod", func(t *testing.T) {
		agent := createTestAgentWithKubeClient()
		// Create a test pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      logReq.PodName,
				Namespace: logReq.Namespace,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: logReq.Container,
					},
				},
			},
		}
		// Create the pod in the fake client
		_, err := agent.kubeClient.Clientset.CoreV1().Pods(logReq.Namespace).Create(ctx, pod, metav1.CreateOptions{})
		require.NoError(t, err)
		// Now test the log stream creation
		rc, err := agent.createKubernetesLogStream(ctx, logReq)
		// The fake client actually supports streaming and returns a valid ReadCloser
		assert.NoError(t, err)
		assert.NotNil(t, rc)

		// Clean up
		if rc != nil {
			rc.Close()
		}
	})
	t.Run("Test createKubernetesLogStream with non-existent pod", func(t *testing.T) {
		agent := createTestAgentWithKubeClient()
		// Test with a non-existent pod
		logReqNotFound := createTestLogRequest(false)
		logReqNotFound.PodName = "non-existent-pod"
		_, err := agent.createKubernetesLogStream(ctx, logReqNotFound)
		assert.NoError(t, err)
	})
}

// Test startLogStreamIfNew
func TestStartLogStreamIfNew(t *testing.T) {
	logCtx := logrus.NewEntry(logrus.New())
	t.Run("duplicate request", func(t *testing.T) {
		logReq := createTestLogRequest(false)
		agent := createTestAgent()
		// Add a duplicate request
		agent.inflightMu.Lock()
		agent.inflightLogs[logReq.UUID] = struct{}{}
		agent.inflightMu.Unlock()
		err := agent.startLogStreamIfNew(logReq, logCtx)
		assert.NoError(t, err) // Should return early for duplicate
	})
	t.Run("new request", func(t *testing.T) {
		logReq := createTestLogRequest(false)
		agent := createTestAgent()
		// This will panic due to missing dependencies, but we can check if new request is processed
		assert.Panics(t, func() {
			agent.startLogStreamIfNew(logReq, logCtx)
		})
	})
}

// Test streamLogsToCompletion
func TestStreamLogsToCompletion(t *testing.T) {
	agent := createTestAgentWithKubeClient()
	ctx := context.Background()
	logReq := createTestLogRequest(false)
	logCtx := logrus.NewEntry(logrus.New())
	// Create a test reader with some log data
	testData := "2025-12-07T10:30:45Z line 1\n2025-12-07T10:30:46Z line 2\n"
	reader := &MockReadCloser{Reader: strings.NewReader(testData)}

	t.Run("successful streaming", func(t *testing.T) {
		mockStream := NewMockLogStreamClient(ctx, logReq.UUID)
		err := agent.streamLogsToCompletion(ctx, mockStream, reader, logReq, logCtx)
		assert.NoError(t, err)
		// Verify that data was sent
		sentData := mockStream.GetSentData()
		assert.GreaterOrEqual(t, len(sentData), 1) // At least 1 data message
		// Check that the last message is EOF
		lastMessage := sentData[len(sentData)-1]
		assert.True(t, lastMessage.Eof)
	})
	t.Run("context cancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately
		mockStream := NewMockLogStreamClient(cancelCtx, logReq.UUID)
		reader := &MockReadCloser{Reader: strings.NewReader(testData)}
		err := agent.streamLogsToCompletion(cancelCtx, mockStream, reader, logReq, logCtx)
		assert.Error(t, err)
		assert.Equal(t, context.Canceled, err)
	})
}

// Test streamLogs
func TestStreamLogs(t *testing.T) {
	agent := createTestAgentWithKubeClient()
	ctx := context.Background()
	logReq := createTestLogRequest(true)
	logCtx := logrus.NewEntry(logrus.New())

	t.Run("successful streaming with data verification", func(t *testing.T) {
		var lastTimestamp *time.Time
		var streamErr error
		// Create a context that we can cancel
		testCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		mockStream := NewMockLogStreamClient(testCtx, logReq.UUID)
		testData := "2025-12-07T10:30:45Z line 1\n2025-12-07T10:30:46Z line 2\n"
		reader := &MockReadCloser{Reader: strings.NewReader(testData)}
		logReq.Timestamps = true
		lastTimestamp, streamErr = agent.streamLogs(testCtx, mockStream, reader, logReq, logCtx)
		// Check if data was sent before cancelling
		sentData := mockStream.GetSentData()
		// Verify data was sent due to timer flush
		assert.Greater(t, len(sentData), 0, "Data should be sent due to timer flush")
		assert.Nil(t, streamErr, "Stream should complete successfully")
		// Verify timestamp extraction worked
		assert.NotNil(t, lastTimestamp, "Timestamp should be extracted")
		assert.Equal(t, 2025, lastTimestamp.Year())
	})
	t.Run("send failure returns timestamp and error", func(t *testing.T) {
		testCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		mockStream := NewMockLogStreamClient(testCtx, logReq.UUID)
		testData := "2025-12-07T10:30:45Z line 1\n2025-12-07T10:30:46Z line 2\n"
		reader := &MockReadCloser{Reader: strings.NewReader(testData)}
		logReq.Timestamps = true
		sendErr := errors.New("stream send failed")
		mockStream.SetSendFunc(func(data *logstreamapi.LogStreamData) error {
			return sendErr
		})
		lastTimestamp, streamErr := agent.streamLogs(testCtx, mockStream, reader, logReq, logCtx)
		require.ErrorIs(t, streamErr, sendErr)
		require.NotNil(t, lastTimestamp, "last timestamp should be captured before send failure")
		assert.Equal(t, 2025, lastTimestamp.Year())
		sentData := mockStream.GetSentData()
		require.Greater(t, len(sentData), 0, "expected a send attempt before failure")
		assert.Equal(t, testData, string(sentData[0].Data))
	})
}

// Helper function to create time pointer
func timePtr(t time.Time) *time.Time {
	return &t
}
