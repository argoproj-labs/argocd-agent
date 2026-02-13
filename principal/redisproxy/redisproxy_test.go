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

package redisproxy

import (
	"context"
	"fmt"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func Test_extractAgentNameFromRedisCommandKey(t *testing.T) {
	logEntry := logging.GetDefaultLogger().ModuleLogger("RedisProxy")

	type testEntry struct {
		name          string
		key           string
		value         string
		errorExpected bool
	}

	testEntries := []testEntry{
		{
			name:          "'app|managed-resources' with name/namespace",
			key:           "app|managed-resources|agent-managed_my-app|1.8.3",
			value:         "agent-managed",
			errorExpected: false,
		},
		{
			name:          "'app|resources-tree' with name/namespace",
			key:           "app|resources-tree|agent-managed_my-app|1.8.3.gz",
			value:         "agent-managed",
			errorExpected: false,
		},
		{
			name:          "'app|managed-resources' with only namespace (classic app without agent prefix)",
			key:           "app|managed-resources|my-app|1.8.3",
			value:         "",
			errorExpected: false,
		},
		{
			name:          "'app|resources-tree' with only namespace (classic app without agent prefix)",
			key:           "app|resources-tree|my-app|1.8.3.gz",
			value:         "",
			errorExpected: false,
		},
		{
			name:          "'app|resources-tree' with unexpected field format",
			key:           "app|resources-tree|agent-managed_my-app|THIS FIELD IS UNEXPECTED|1.8.3.gz",
			value:         "",
			errorExpected: true,
		},
		{
			name:          "mfst| key",
			key:           "mfst|annotation:app.kubernetes.io/instance|agent-managed_my-app|(revision id)|guestbook|3093507789|1.8.3.gz",
			value:         "",
			errorExpected: false,
		},
		{
			name:          "unexpected key should just return empty value, so it will be forwarded to principal",
			key:           "some other unexpected key",
			value:         "",
			errorExpected: false,
		},
	}

	for _, testEntry := range testEntries {
		t.Run(testEntry.name, func(t *testing.T) {
			res, err := extractAgentNameFromRedisCommandKey(testEntry.key, logEntry)

			require.Equal(t, testEntry.value, res)
			require.Equal(t, testEntry.errorExpected, err != nil, "err: %v", err)
		})
	}
}

// mockArgoCDRedisWriter is a mock implementation of argoCDRedisWriter interface for testing
type mockArgoCDRedisWriter struct {
	writeToArgoCDRedisSocketCalled bool
	writtenBytes                   []byte
	returnError                    error
}

func (m *mockArgoCDRedisWriter) writeToArgoCDRedisSocket(logCtx *logrus.Entry, bytes []byte) error {
	m.writeToArgoCDRedisSocketCalled = true
	m.writtenBytes = bytes
	return m.returnError
}

func Test_handleInternalNotify(t *testing.T) {
	logEntry := logging.GetDefaultLogger().ModuleLogger("RedisProxy")

	t.Run("successful internal notify with valid command", func(t *testing.T) {
		mockWriter := &mockArgoCDRedisWriter{}
		channelName := "app|managed-resources|agent-managed_my-app|1.8.3"
		vals := []string{constInternalNotify, channelName}

		err := handleInternalNotify(vals, mockWriter, logEntry)

		require.NoError(t, err)
		require.True(t, mockWriter.writeToArgoCDRedisSocketCalled)

		expectedMsg := fmt.Sprintf(">3\r\n$7\r\nmessage\r\n$%d\r\n%s\r\n$0\r\n\r\n", len([]byte(channelName)), channelName)
		require.Equal(t, expectedMsg, string(mockWriter.writtenBytes))
	})

	t.Run("successful internal notify with different channel name", func(t *testing.T) {
		mockWriter := &mockArgoCDRedisWriter{}
		channelName := "app|resources-tree|test-agent_my-app|1.8.4.gz"
		vals := []string{constInternalNotify, channelName}

		err := handleInternalNotify(vals, mockWriter, logEntry)

		require.NoError(t, err)
		require.True(t, mockWriter.writeToArgoCDRedisSocketCalled)

		expectedMsg := fmt.Sprintf(">3\r\n$7\r\nmessage\r\n$%d\r\n%s\r\n$0\r\n\r\n", len([]byte(channelName)), channelName)
		require.Equal(t, expectedMsg, string(mockWriter.writtenBytes))
	})

	t.Run("error when command is not internal-notify", func(t *testing.T) {
		mockWriter := &mockArgoCDRedisWriter{}
		channelName := "app|managed-resources|agent-managed_my-app|1.8.3"
		vals := []string{"invalid-command", channelName}

		err := handleInternalNotify(vals, mockWriter, logEntry)

		require.Error(t, err)
		require.Contains(t, err.Error(), "unexpected internal message command: invalid-command")
		require.False(t, mockWriter.writeToArgoCDRedisSocketCalled)
	})

	t.Run("error when writeToArgoCDRedisSocket fails", func(t *testing.T) {
		mockWriter := &mockArgoCDRedisWriter{
			returnError: fmt.Errorf("write socket error"),
		}
		channelName := "app|managed-resources|agent-managed_my-app|1.8.3"
		vals := []string{constInternalNotify, channelName}

		err := handleInternalNotify(vals, mockWriter, logEntry)

		require.Error(t, err)
		require.Contains(t, err.Error(), "unable to write response")
		require.True(t, mockWriter.writeToArgoCDRedisSocketCalled)
	})
}

// mockSendSynchronousMessageToAgentFunc is a mock implementation of sendSynchronousMessageToAgentFuncType
type mockSendSynchronousMessageToAgentFunc struct {
	callCount        int
	lastAgentName    string
	lastConnUUID     string
	lastBody         event.RedisCommandBody
	responseToReturn *event.RedisResponseBody
}

func (m *mockSendSynchronousMessageToAgentFunc) call(agentName string, connectionUUID string, body event.RedisCommandBody) *event.RedisResponseBody {
	m.callCount++
	m.lastAgentName = agentName
	m.lastConnUUID = connectionUUID
	m.lastBody = body
	return m.responseToReturn
}

func Test_handleAgentGet(t *testing.T) {
	t.Run("successful get with cache hit", func(t *testing.T) {
		// Setup mocks
		mockWriter := &mockArgoCDRedisWriter{}
		mockSendFunc := &mockSendSynchronousMessageToAgentFunc{
			responseToReturn: &event.RedisResponseBody{
				Get: &event.RedisResponseBodyGet{
					Bytes:    []byte("test-data"),
					CacheHit: true,
					Error:    "",
				},
			},
		}

		// Create RedisProxy instance
		rp := &RedisProxy{
			sendSynchronousMessageToAgentFn: mockSendFunc.call,
		}

		logEntry := rp.log()

		// Setup connection state
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		connState := &connectionState{
			connectionCtx:      ctx,
			connectionCancelFn: cancel,
			connUUID:           "test-connection-uuid",
			pingRoutineStarted: make(map[string]bool),
		}

		// Test data
		key := "app|managed-resources|test-agent_my-app|1.8.3"
		agentName := "test-agent"

		// Call the function
		err := rp.handleAgentGet(connState, key, mockWriter, agentName, logEntry)

		// Assertions
		require.NoError(t, err)
		require.Equal(t, 1, mockSendFunc.callCount)
		require.Equal(t, agentName, mockSendFunc.lastAgentName)
		require.Equal(t, "test-connection-uuid", mockSendFunc.lastConnUUID)
		require.NotNil(t, mockSendFunc.lastBody.Get)
		require.Equal(t, key, mockSendFunc.lastBody.Get.Key)

		// Check that the correct response was written
		require.True(t, mockWriter.writeToArgoCDRedisSocketCalled)
		expectedResponse := fmt.Sprintf("$%d\r\n%s\r\n", len([]byte("test-data")), "test-data")
		require.Equal(t, expectedResponse, string(mockWriter.writtenBytes))
	})

	t.Run("successful get with empty cache hit", func(t *testing.T) {
		// Setup mocks
		mockWriter := &mockArgoCDRedisWriter{}
		mockSendFunc := &mockSendSynchronousMessageToAgentFunc{
			responseToReturn: &event.RedisResponseBody{
				Get: &event.RedisResponseBodyGet{
					Bytes:    []byte{},
					CacheHit: true,
					Error:    "",
				},
			},
		}

		// Create RedisProxy instance
		rp := &RedisProxy{
			sendSynchronousMessageToAgentFn: mockSendFunc.call,
		}

		logEntry := rp.log()

		// Setup connection state
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		connState := &connectionState{
			connectionCtx:      ctx,
			connectionCancelFn: cancel,
			connUUID:           uuid.New().String(),
			pingRoutineStarted: make(map[string]bool),
		}

		// Test data
		key := "app|resources-tree|agent-managed_test-app|1.8.4.gz"
		agentName := "agent-managed"

		// Call the function
		err := rp.handleAgentGet(connState, key, mockWriter, agentName, logEntry)

		// Assertions
		require.NoError(t, err)
		require.Equal(t, 1, mockSendFunc.callCount)
		require.True(t, mockWriter.writeToArgoCDRedisSocketCalled)
		expectedResponse := "$0\r\n\r\n"
		require.Equal(t, expectedResponse, string(mockWriter.writtenBytes))
	})

	t.Run("cache miss returns 'CacheHit: false', with empty error string", func(t *testing.T) {
		// Setup mocks
		mockWriter := &mockArgoCDRedisWriter{}
		mockSendFunc := &mockSendSynchronousMessageToAgentFunc{
			responseToReturn: &event.RedisResponseBody{
				Get: &event.RedisResponseBodyGet{
					Bytes:    nil,
					CacheHit: false,
					Error:    "",
				},
			},
		}

		// Create RedisProxy instance
		rp := &RedisProxy{
			sendSynchronousMessageToAgentFn: mockSendFunc.call,
		}

		logEntry := rp.log()

		// Setup connection state
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		connState := &connectionState{
			connectionCtx:      ctx,
			connectionCancelFn: cancel,
			connUUID:           uuid.New().String(),
			pingRoutineStarted: make(map[string]bool),
		}

		// Test data
		key := "app|managed-resources|test-agent_my-app|1.8.3"
		agentName := "test-agent"

		// Call the function
		err := rp.handleAgentGet(connState, key, mockWriter, agentName, logEntry)

		// Assertions
		require.NoError(t, err)
		require.Equal(t, 1, mockSendFunc.callCount)
		require.True(t, mockWriter.writeToArgoCDRedisSocketCalled)
		expectedResponse := "-\r\n"
		require.Equal(t, expectedResponse, string(mockWriter.writtenBytes))
	})

	t.Run("error from get response should be returned within Get body", func(t *testing.T) {
		// Setup mocks
		mockWriter := &mockArgoCDRedisWriter{}
		mockSendFunc := &mockSendSynchronousMessageToAgentFunc{
			responseToReturn: &event.RedisResponseBody{
				Get: &event.RedisResponseBodyGet{
					Bytes:    nil,
					CacheHit: false,
					Error:    "redis connection failed",
				},
			},
		}

		// Create RedisProxy instance
		rp := &RedisProxy{
			sendSynchronousMessageToAgentFn: mockSendFunc.call,
		}

		logEntry := rp.log()

		// Setup connection state
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		connState := &connectionState{
			connectionCtx:      ctx,
			connectionCancelFn: cancel,
			connUUID:           uuid.New().String(),
			pingRoutineStarted: make(map[string]bool),
		}

		// Test data
		key := "app|managed-resources|test-agent_my-app|1.8.3"
		agentName := "test-agent"

		// Call the function
		err := rp.handleAgentGet(connState, key, mockWriter, agentName, logEntry)

		// Assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "unexpected error: redis connection failed")
		require.Equal(t, 1, mockSendFunc.callCount)
		require.False(t, mockWriter.writeToArgoCDRedisSocketCalled)
	})
}

func Test_handleAgentSubscribe(t *testing.T) {
	t.Run("successful subscription", func(t *testing.T) {
		// Setup mocks
		mockWriter := &mockArgoCDRedisWriter{}
		mockSendFunc := &mockSendSynchronousMessageToAgentFunc{
			responseToReturn: &event.RedisResponseBody{
				SubscribeResponse: &event.RedisResponseBodySubscribeResponse{
					Error: "", // no error
				},
			},
		}

		// Create RedisProxy instance with nil tracker since we're ignoring channel logic
		rp := &RedisProxy{
			sendSynchronousMessageToAgentFn: mockSendFunc.call,
			ConnectionIDTracker:             nil,
		}

		logEntry := rp.log()

		// Setup connection state
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		connState := &connectionState{
			connectionCtx:               ctx,
			connectionCancelFn:          cancel,
			connUUID:                    "test-connection-uuid",
			connectionUUIDEventsChannel: make(chan *cloudevents.Event, 1),
			pingRoutineStarted:          make(map[string]bool),
		}

		// Test data
		channelName := "app|managed-resources|test-agent_my-app|1.8.3"
		agentName := "test-agent"

		// Call the function
		err := rp.handleAgentSubscribe(connState, channelName, agentName, mockWriter, logEntry)

		// Assertions
		require.NoError(t, err)
		require.Equal(t, 1, mockSendFunc.callCount)
		require.Equal(t, agentName, mockSendFunc.lastAgentName)
		require.Equal(t, "test-connection-uuid", mockSendFunc.lastConnUUID)
		require.NotNil(t, mockSendFunc.lastBody.Subscribe)
		require.Equal(t, channelName, mockSendFunc.lastBody.Subscribe.ChannelName)

		// Check that the correct response was written
		require.True(t, mockWriter.writeToArgoCDRedisSocketCalled)
		expectedResponse := fmt.Sprintf(">3\r\n$9\r\nsubscribe\r\n$%d\r\n%s\r\n:1\r\n", len([]byte(channelName)), channelName)
		require.Equal(t, expectedResponse, string(mockWriter.writtenBytes))

		// Check that ping routine was started
		require.True(t, connState.pingRoutineStarted[agentName])
	})
}
