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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/principal/tracker"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func Test_extractAgentNameFromRedisCommandKey(t *testing.T) {
	logEntry := logging.GetDefaultLogger().ModuleLogger("RedisProxy")

	type testEntry struct {
		name               string
		key                string
		value              string
		errorExpected      bool
		agentFn            AgentLookupFunc
		principalNamespace string
	}

	testEntries := []testEntry{
		{
			name:          "'app|managed-resources' with name/namespace",
			key:           "app|managed-resources|agent-managed_my-app|1.8.3",
			value:         "agent-managed",
			errorExpected: false,
		},
		{
			name:          "'app|managed-resources' with name/namespace and custom agent lookup function",
			key:           "app|managed-resources|agent-managed_my-app|1.8.3",
			value:         "custom-agent-name",
			errorExpected: false,
			agentFn: func(namespace, name string) string {
				return "custom-agent-name"
			},
		},
		{
			name:          "'app|resources-tree' with name/namespace",
			key:           "app|resources-tree|agent-managed_my-app|1.8.3.gz",
			value:         "agent-managed",
			errorExpected: false,
		},
		{
			name:               "'app|managed-resources' local app in principal namespace (no underscore), no agentFn",
			key:                "app|managed-resources|my-app|1.8.3",
			value:              "",
			errorExpected:      false,
			principalNamespace: "argocd",
		},
		{
			name:               "'app|managed-resources' app in principal namespace with agentFn returning agent",
			key:                "app|managed-resources|guestbook|1.8.3",
			value:              "my-agent",
			errorExpected:      false,
			principalNamespace: "argocd",
			agentFn: func(namespace, name string) string {
				if namespace == "argocd" && name == "guestbook" {
					return "my-agent"
				}
				return ""
			},
		},
		{
			name:          "'app|managed-resources' no underscore without agentFn (namespace-based mapping) should return empty string for fallback",
			key:           "app|managed-resources|my-app|1.8.3",
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
			name:          "gitfiles| key",
			key:           "gitfiles|https://github.com/argoproj/argocd-example-apps|d9dec7aed84f20b6b25905fe24ab0844bfa38a8e|apps/config.yaml|1.8.3.gz",
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
			rp := &RedisProxy{}
			rp.agentLookupFn = testEntry.agentFn
			rp.principalNamespace = testEntry.principalNamespace
			res, err := rp.extractAgentNameFromRedisCommandKey(testEntry.key, logEntry)

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

	t.Run("nil response from agent is reported as a subscribe failure", func(t *testing.T) {
		mockWriter := &mockArgoCDRedisWriter{}
		mockSendFunc := &mockSendSynchronousMessageToAgentFunc{responseToReturn: nil}

		rp := &RedisProxy{
			sendSynchronousMessageToAgentFn: mockSendFunc.call,
			ConnectionIDTracker:             nil,
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		connState := &connectionState{
			connectionCtx:               ctx,
			connectionCancelFn:          cancel,
			connUUID:                    uuid.New().String(),
			connectionUUIDEventsChannel: make(chan *cloudevents.Event, 1),
			pingRoutineStarted:          make(map[string]bool),
		}

		err := rp.handleAgentSubscribe(connState, "app|managed-resources|test-agent_my-app|1.8.3", "test-agent", mockWriter, rp.log())

		require.Error(t, err)
		require.Contains(t, err.Error(), "subscribe response")
		require.NotContains(t, err.Error(), "get response")
		require.False(t, mockWriter.writeToArgoCDRedisSocketCalled)
	})
}

func Test_handleAgentSubscribe_forwarderStopsWhenConnectionCloses(t *testing.T) {
	mockSendFunc := &mockSendSynchronousMessageToAgentFunc{
		responseToReturn: &event.RedisResponseBody{
			SubscribeResponse: &event.RedisResponseBodySubscribeResponse{},
		},
	}
	rp := &RedisProxy{
		sendSynchronousMessageToAgentFn: mockSendFunc.call,
		ConnectionIDTracker:             tracker.New(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connState := &connectionState{
		connectionCtx:      ctx,
		connectionCancelFn: cancel,
		connUUID:           uuid.New().String(),
		pingRoutineStarted: make(map[string]bool),
	}
	err := rp.handleAgentSubscribe(connState, "app|managed-resources|test-agent_my-app|1.8.3", "test-agent", &mockArgoCDRedisWriter{}, rp.log())
	require.NoError(t, err)

	// A notification from the agent arrives, but nobody reads the connection's channel anymore
	// (the connection's message loop has exited), so the forwarder sits on the send...
	_, evCh := rp.ConnectionIDTracker.Tracked(connState.connUUID)
	require.NotNil(t, evCh)
	ev := cloudevents.NewEvent()
	evCh <- &ev
	require.Eventually(t, func() bool { return len(evCh) == 0 }, time.Second, 5*time.Millisecond, "forwarder did not pick up the event")

	// ...and then the connection goes away
	cancel()

	// The forwarder must give up on the send, exit, and stop tracking the connection
	require.Eventually(t, func() bool {
		_, ch := rp.ConnectionIDTracker.Tracked(connState.connUUID)
		return ch == nil
	}, time.Second, 10*time.Millisecond, "forwarder did not stop tracking the closed connection")
}

func Test_readNextMessageFromArgoCDOrInternal_closedInternalChannel(t *testing.T) {
	rp := &RedisProxy{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connState := &connectionState{
		connectionCtx:               ctx,
		connectionCancelFn:          cancel,
		connUUID:                    uuid.New().String(),
		connectionUUIDEventsChannel: make(chan *cloudevents.Event),
		pingRoutineStarted:          make(map[string]bool),
	}

	// The forwarder goroutine closes this channel when it exits
	close(connState.connectionUUIDEventsChannel)

	// Must not panic, and must tell the message loop to stop
	result := readNextMessageFromArgoCDOrInternal(connState, make(chan parsedRedisCommand), rp.log())
	require.True(t, result.connContextCancelled)
}

// newTestConnection returns a connection state as handleConnection would create it
func newTestConnection(t *testing.T) *connectionState {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &connectionState{
		connectionCtx:      ctx,
		connectionCancelFn: cancel,
		connUUID:           uuid.New().String(),
		pingRoutineStarted: make(map[string]bool),
	}
}

// subscribeOnTestConnection runs a SUBSCRIBE for agentName on connState through rp
func subscribeOnTestConnection(rp *RedisProxy, connState *connectionState, agentName string) error {
	channelName := fmt.Sprintf("app|managed-resources|%s_my-app|1.8.3", agentName)
	return rp.handleAgentSubscribe(connState, channelName, agentName, &mockArgoCDRedisWriter{}, rp.log())
}

// subscribeTestConnection runs a successful SUBSCRIBE for agentName through rp and returns the connection state
func subscribeTestConnection(t *testing.T, rp *RedisProxy, agentName string) *connectionState {
	t.Helper()
	connState := newTestConnection(t)
	require.NoError(t, subscribeOnTestConnection(rp, connState, agentName))
	return connState
}

// requireCancelled fails the test unless connState's context is cancelled within a second
func requireCancelled(t *testing.T, connState *connectionState) {
	t.Helper()
	select {
	case <-connState.connectionCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("connection was not cancelled")
	}
}

func newTestRedisProxy(mockSendFunc *mockSendSynchronousMessageToAgentFunc) *RedisProxy {
	return &RedisProxy{
		sendSynchronousMessageToAgentFn: mockSendFunc.call,
		ConnectionIDTracker:             tracker.New(),
	}
}

func Test_CloseAgentConnections(t *testing.T) {
	subscribeOK := &event.RedisResponseBody{SubscribeResponse: &event.RedisResponseBodySubscribeResponse{}}

	t.Run("closes only the subscribed connections of the given agent", func(t *testing.T) {
		rp := newTestRedisProxy(&mockSendSynchronousMessageToAgentFunc{responseToReturn: subscribeOK})

		connA := subscribeTestConnection(t, rp, "agent-a")
		connB := subscribeTestConnection(t, rp, "agent-b")

		require.Equal(t, 1, rp.CloseAgentConnections("agent-a"))
		requireCancelled(t, connA)
		require.NoError(t, connB.connectionCtx.Err())

		// A closed connection is forgotten, so a second call has nothing to do
		require.Eventually(t, func() bool { return rp.CloseAgentConnections("agent-a") == 0 }, time.Second, 10*time.Millisecond)

		// An agent without subscribed connections is a no-op
		require.Equal(t, 0, rp.CloseAgentConnections("agent-unknown"))
	})

	t.Run("a connection subscribed to several agents is closed when any of them reconnects", func(t *testing.T) {
		rp := newTestRedisProxy(&mockSendSynchronousMessageToAgentFunc{responseToReturn: subscribeOK})

		connState := newTestConnection(t)
		require.NoError(t, subscribeOnTestConnection(rp, connState, "agent-a"))
		require.NoError(t, subscribeOnTestConnection(rp, connState, "agent-b"))

		require.Equal(t, 1, rp.CloseAgentConnections("agent-b"))
		requireCancelled(t, connState)
	})

	t.Run("a connection whose subscribe failed is not tracked", func(t *testing.T) {
		rp := newTestRedisProxy(&mockSendSynchronousMessageToAgentFunc{responseToReturn: nil})

		connState := newTestConnection(t)
		require.Error(t, subscribeOnTestConnection(rp, connState, "agent-a"))

		require.Equal(t, 0, rp.CloseAgentConnections("agent-a"))
		require.NoError(t, connState.connectionCtx.Err())
	})

	t.Run("an agent reconnecting while the subscribe is in flight closes the connection", func(t *testing.T) {
		// The agent answers the subscribe and restarts right away, so the response describes a subscription
		// the new agent does not have. The connection must already be tracked when the reconnect is handled.
		rp := &RedisProxy{ConnectionIDTracker: tracker.New()}
		rp.sendSynchronousMessageToAgentFn = func(agentName, connUUID string, body event.RedisCommandBody) *event.RedisResponseBody {
			require.Equal(t, 1, rp.CloseAgentConnections(agentName), "connection must be tracked before the agent answers")
			return subscribeOK
		}

		connState := newTestConnection(t)
		_ = subscribeOnTestConnection(rp, connState, "agent-a")
		requireCancelled(t, connState)
	})

	t.Run("a failed subscribe on a connection subscribed to another agent keeps that agent tracked", func(t *testing.T) {
		rp := newTestRedisProxy(&mockSendSynchronousMessageToAgentFunc{responseToReturn: subscribeOK})
		connState := newTestConnection(t)
		require.NoError(t, subscribeOnTestConnection(rp, connState, "agent-a"))

		rp.sendSynchronousMessageToAgentFn = (&mockSendSynchronousMessageToAgentFunc{responseToReturn: nil}).call
		require.Error(t, subscribeOnTestConnection(rp, connState, "agent-b"))

		require.Equal(t, 0, rp.CloseAgentConnections("agent-b"))
		require.Equal(t, 1, rp.CloseAgentConnections("agent-a"))
		requireCancelled(t, connState)
	})
}

func Test_rewriteResourcesTreeNamespace(t *testing.T) {
	t.Run("rewrites namespace for Application- and ApplicationSet-kind nodes, gzip'd", func(t *testing.T) {
		tree := &v1alpha1.ApplicationTree{
			Nodes: []v1alpha1.ResourceNode{
				{
					ResourceRef: v1alpha1.ResourceRef{
						Group: "argoproj.io", Kind: "Application", Namespace: "argocd", Name: "child-app",
					},
				},
				{
					ResourceRef: v1alpha1.ResourceRef{
						Group: "argoproj.io", Kind: "ApplicationSet", Namespace: "argocd", Name: "generator",
					},
				},
				{
					ResourceRef: v1alpha1.ResourceRef{
						Group: "apps", Kind: "Deployment", Namespace: "default", Name: "some-deployment",
					},
				},
			},
		}
		raw, err := json.Marshal(tree)
		require.NoError(t, err)

		buf := &bytes.Buffer{}
		gzipWriter := gzip.NewWriter(buf)
		_, err = gzipWriter.Write(raw)
		require.NoError(t, err)
		require.NoError(t, gzipWriter.Close())

		rewritten, err := rewriteResourcesTreeNamespace(buf.Bytes(), "agent-staging")
		require.NoError(t, err)

		gzipReader, err := gzip.NewReader(bytes.NewReader(rewritten))
		require.NoError(t, err)
		decompressed, err := io.ReadAll(gzipReader)
		require.NoError(t, err)

		var got v1alpha1.ApplicationTree
		require.NoError(t, json.Unmarshal(decompressed, &got))

		require.Equal(t, "agent-staging", got.Nodes[0].Namespace, "Application node namespace should be rewritten")
		require.Equal(t, "agent-staging", got.Nodes[1].Namespace, "ApplicationSet node namespace should be rewritten")
		require.Equal(t, "default", got.Nodes[2].Namespace, "non-argoproj.io node namespace should be untouched")
	})

	t.Run("rewrites namespace for Application-kind nodes, uncompressed", func(t *testing.T) {
		tree := &v1alpha1.ApplicationTree{
			Nodes: []v1alpha1.ResourceNode{
				{
					ResourceRef: v1alpha1.ResourceRef{
						Group: "argoproj.io", Kind: "Application", Namespace: "argocd", Name: "child-app",
					},
				},
			},
		}
		raw, err := json.Marshal(tree)
		require.NoError(t, err)

		rewritten, err := rewriteResourcesTreeNamespace(raw, "agent-staging")
		require.NoError(t, err)

		var got v1alpha1.ApplicationTree
		require.NoError(t, json.Unmarshal(rewritten, &got))
		require.Equal(t, "agent-staging", got.Nodes[0].Namespace)
	})

	t.Run("ApplicationSet-generated child ends up with the same namespace as its ApplicationSet parent", func(t *testing.T) {
		tree := &v1alpha1.ApplicationTree{
			Nodes: []v1alpha1.ResourceNode{
				{
					ResourceRef: v1alpha1.ResourceRef{
						Group: "argoproj.io", Kind: "ApplicationSet", Namespace: "argocd", Name: "generator", UID: "appset-uid",
					},
				},
				{
					ResourceRef: v1alpha1.ResourceRef{
						Group: "argoproj.io", Kind: "Application", Namespace: "argocd", Name: "generated-app",
					},
					ParentRefs: []v1alpha1.ResourceRef{
						{Group: "argoproj.io", Kind: "ApplicationSet", Namespace: "argocd", Name: "generator", UID: "appset-uid"},
					},
				},
			},
		}
		raw, err := json.Marshal(tree)
		require.NoError(t, err)

		rewritten, err := rewriteResourcesTreeNamespace(raw, "agent-staging")
		require.NoError(t, err)

		var got v1alpha1.ApplicationTree
		require.NoError(t, json.Unmarshal(rewritten, &got))
		require.Equal(t, got.Nodes[0].Namespace, got.Nodes[1].Namespace, "ApplicationSet and its generated child must end up with matching namespaces so the UI draws the connecting edge between them")
	})

	t.Run("rewrites namespace for orphaned Application/ApplicationSet nodes", func(t *testing.T) {
		tree := &v1alpha1.ApplicationTree{
			OrphanedNodes: []v1alpha1.ResourceNode{
				{
					ResourceRef: v1alpha1.ResourceRef{
						Group: "argoproj.io", Kind: "Application", Namespace: "argocd", Name: "orphaned-app",
					},
				},
				{
					ResourceRef: v1alpha1.ResourceRef{
						Group: "apps", Kind: "Deployment", Namespace: "default", Name: "orphaned-deployment",
					},
				},
			},
		}
		raw, err := json.Marshal(tree)
		require.NoError(t, err)

		rewritten, err := rewriteResourcesTreeNamespace(raw, "agent-staging")
		require.NoError(t, err)

		var got v1alpha1.ApplicationTree
		require.NoError(t, json.Unmarshal(rewritten, &got))
		require.Equal(t, "agent-staging", got.OrphanedNodes[0].Namespace, "orphaned Application node namespace should be rewritten")
		require.Equal(t, "default", got.OrphanedNodes[1].Namespace, "non-argoproj.io orphaned node namespace should be untouched")
	})

	t.Run("no Application/ApplicationSet nodes: returns original bytes untouched", func(t *testing.T) {
		tree := &v1alpha1.ApplicationTree{
			Nodes: []v1alpha1.ResourceNode{
				{
					ResourceRef: v1alpha1.ResourceRef{
						Group: "apps", Kind: "Deployment", Namespace: "default", Name: "some-deployment",
					},
				},
			},
		}
		raw, err := json.Marshal(tree)
		require.NoError(t, err)

		rewritten, err := rewriteResourcesTreeNamespace(raw, "agent-staging")
		require.NoError(t, err)
		require.Equal(t, raw, rewritten)
	})

	t.Run("invalid data: returns an error", func(t *testing.T) {
		_, err := rewriteResourcesTreeNamespace([]byte("not json"), "agent-staging")
		require.Error(t, err)
	})
}
