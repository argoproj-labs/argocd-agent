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
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/principal/tracker"
	"github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
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

func Test_New(t *testing.T) {
	sendSync := func(agentName, connectionUUID string, body event.RedisCommandBody) *event.RedisResponseBody {
		return nil
	}

	t.Run("stores the provided fields", func(t *testing.T) {
		customLogger := logging.New(logrus.New())
		rp := New("127.0.0.1:6379", "redis:6379", sendSync, customLogger)

		require.NotNil(t, rp)
		require.Equal(t, "127.0.0.1:6379", rp.listenAddress)
		require.Equal(t, "redis:6379", rp.principalRedisAddress)
		require.Same(t, customLogger, rp.logger)
		require.NotNil(t, rp.EventIDTracker)
		require.NotNil(t, rp.ConnectionIDTracker)
		require.NotNil(t, rp.sendSynchronousMessageToAgentFn)
	})

	t.Run("defaults the logger to the default one when nil", func(t *testing.T) {
		rp := New("addr", "redis", nil, nil)

		require.NotNil(t, rp.logger)
		require.Same(t, logging.GetDefaultLogger(), rp.logger)
	})
}

func Test_RedisProxy_createServerTLSConfig(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, rp *RedisProxy)
		wantErr string
		check   func(t *testing.T, cfg *tls.Config)
	}{
		{
			name:    "no certificate configured",
			setup:   func(t *testing.T, rp *RedisProxy) {},
			wantErr: "no TLS certificate configured",
		},
		{
			name: "in-memory certificate carries the protocol config",
			setup: func(t *testing.T, rp *RedisProxy) {
				certPEM, keyPEM := testcerts.CreateSelfSignedCert(t, "rsa", testcerts.DefaultCertTempl)
				pair, err := tls.X509KeyPair(certPEM, keyPEM)
				require.NoError(t, err)
				leaf, err := x509.ParseCertificate(pair.Certificate[0])
				require.NoError(t, err)

				rp.SetServerTLS(leaf, pair.PrivateKey)
				rp.SetServerTLSConfig(tls.VersionTLS12, tls.VersionTLS13,
					[]uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
					[]tls.CurveID{tls.CurveP256})
			},
			check: func(t *testing.T, cfg *tls.Config) {
				require.Len(t, cfg.Certificates, 1)
				require.NotNil(t, cfg.Certificates[0].Leaf)
				require.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
				require.Equal(t, uint16(tls.VersionTLS13), cfg.MaxVersion)
				require.Equal(t, []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}, cfg.CipherSuites)
				require.Equal(t, []tls.CurveID{tls.CurveP256}, cfg.CurvePreferences)
			},
		},
		{
			name: "certificate loaded from valid paths",
			setup: func(t *testing.T, rp *RedisProxy) {
				base := filepath.Join(t.TempDir(), "server")
				testcerts.WriteSelfSignedCert(t, "rsa", base, testcerts.DefaultCertTempl)
				rp.SetServerTLSFromPath(base+".crt", base+".key")
			},
			check: func(t *testing.T, cfg *tls.Config) {
				require.Len(t, cfg.Certificates, 1)
			},
		},
		{
			name: "certificate paths take precedence over the in-memory certificate",
			setup: func(t *testing.T, rp *RedisProxy) {
				memPEM, memKeyPEM := testcerts.CreateSelfSignedCert(t, "rsa", testcerts.DefaultCertTempl)
				memPair, err := tls.X509KeyPair(memPEM, memKeyPEM)
				require.NoError(t, err)
				memLeaf, err := x509.ParseCertificate(memPair.Certificate[0])
				require.NoError(t, err)
				rp.SetServerTLS(memLeaf, memPair.PrivateKey)

				pathTempl := testcerts.DefaultCertTempl
				pathTempl.SerialNumber = big.NewInt(42)
				base := filepath.Join(t.TempDir(), "server")
				testcerts.WriteSelfSignedCert(t, "rsa", base, pathTempl)
				rp.SetServerTLSFromPath(base+".crt", base+".key")
			},
			check: func(t *testing.T, cfg *tls.Config) {
				require.Len(t, cfg.Certificates, 1)
				leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
				require.NoError(t, err)
				require.Equal(t, int64(42), leaf.SerialNumber.Int64(), "expected the certificate loaded from the paths")
			},
		},
		{
			name: "a single certificate path falls back to the in-memory certificate",
			setup: func(t *testing.T, rp *RedisProxy) {
				memPEM, memKeyPEM := testcerts.CreateSelfSignedCert(t, "rsa", testcerts.DefaultCertTempl)
				memPair, err := tls.X509KeyPair(memPEM, memKeyPEM)
				require.NoError(t, err)
				memLeaf, err := x509.ParseCertificate(memPair.Certificate[0])
				require.NoError(t, err)
				rp.SetServerTLS(memLeaf, memPair.PrivateKey)

				rp.SetServerTLSFromPath(filepath.Join(t.TempDir(), "only.crt"), "")
			},
			check: func(t *testing.T, cfg *tls.Config) {
				require.Len(t, cfg.Certificates, 1)
				leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
				require.NoError(t, err)
				require.Equal(t, int64(1), leaf.SerialNumber.Int64(), "expected the in-memory certificate")
			},
		},
		{
			name: "missing certificate paths are reported",
			setup: func(t *testing.T, rp *RedisProxy) {
				dir := t.TempDir()
				rp.SetServerTLSFromPath(filepath.Join(dir, "missing.crt"), filepath.Join(dir, "missing.key"))
			},
			wantErr: "failed to load TLS certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := New("addr", "redis", nil, nil)
			tt.setup(t, rp)

			cfg, err := rp.createServerTLSConfig()

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Nil(t, cfg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func Test_parsedRedisCommand_generateParsedCommandDebugString(t *testing.T) {
	tests := []struct {
		name string
		cmd  parsedRedisCommand
		want string
	}{
		{
			name: "parse error is surfaced",
			cmd:  parsedRedisCommand{err: fmt.Errorf("boom")},
			want: "error occurred: boom",
		},
		{
			name: "internal message is labelled",
			cmd:  parsedRedisCommand{internalMsg: []string{"internal-notify", "chan"}},
			want: "internal message: [internal-notify chan]",
		},
		{
			name: "parsed ASCII command is shown verbatim",
			cmd:  parsedRedisCommand{parsedReceived: []string{"PING"}},
			want: "(a->r) 'PING' | ",
		},
		{
			name: "non-ASCII command is summarized by byte length",
			cmd:  parsedRedisCommand{parsedReceived: []string{"café"}},
			want: "(a->r) '( 5 bytes )' | ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.cmd.generateParsedCommandDebugString())
		})
	}
}

var errWriteFailed = errors.New("write failed")

// failingWriter is an io.Writer whose Write always fails, to exercise the
// write-error paths of the proxy's forwarding code.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errWriteFailed }

func Test_argoCDRedisWriterInternal_writeToArgoCDRedisSocket(t *testing.T) {
	logEntry := logging.GetDefaultLogger().ModuleLogger("RedisProxy")

	t.Run("writes and flushes the bytes to the socket", func(t *testing.T) {
		var out bytes.Buffer
		w := &argoCDRedisWriterInternal{
			fromArgoCDWrite: bufio.NewWriter(&out),
			argoCDWriteLock: &sync.Mutex{},
		}

		require.NoError(t, w.writeToArgoCDRedisSocket(logEntry, []byte("hello")))
		require.Equal(t, "hello", out.String())
	})

	t.Run("returns the error when the socket write fails", func(t *testing.T) {
		w := &argoCDRedisWriterInternal{
			fromArgoCDWrite: bufio.NewWriterSize(failingWriter{}, 1),
			argoCDWriteLock: &sync.Mutex{},
		}

		require.ErrorIs(t, w.writeToArgoCDRedisSocket(logEntry, []byte("hello")), errWriteFailed)
	})
}

func Test_forwardTrafficSimple(t *testing.T) {
	logEntry := logging.GetDefaultLogger().ModuleLogger("RedisProxy")

	t.Run("forwards all input and returns when the reader is exhausted", func(t *testing.T) {
		var out bytes.Buffer
		w := &argoCDRedisWriterInternal{
			fromArgoCDWrite: bufio.NewWriter(&out),
			argoCDWriteLock: &sync.Mutex{},
		}
		r := bufio.NewReader(strings.NewReader("+PONG\r\n"))

		err := forwardTrafficSimple("r->a", r, w, logEntry)

		require.ErrorIs(t, err, io.EOF)
		require.Equal(t, "+PONG\r\n", out.String())
	})

	t.Run("returns the write error when the destination fails", func(t *testing.T) {
		w := &argoCDRedisWriterInternal{
			fromArgoCDWrite: bufio.NewWriterSize(failingWriter{}, 1),
			argoCDWriteLock: &sync.Mutex{},
		}
		r := bufio.NewReader(strings.NewReader("data"))

		err := forwardTrafficSimple("r->a", r, w, logEntry)

		require.ErrorIs(t, err, errWriteFailed)
	})
}

// fakeUpstreamListener starts a TCP listener on a loopback ephemeral port that
// accepts and holds connections, so a dial to it succeeds. The listener and any
// accepted connections are closed when the test ends.
func fakeUpstreamListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	})

	return l
}

func Test_RedisProxy_establishConnectionToPrincipalRedis(t *testing.T) {
	logEntry := logging.GetDefaultLogger().ModuleLogger("RedisProxy")

	t.Run("resolve error for an address without a port", func(t *testing.T) {
		rp := New("addr", "missing-port", nil, nil)

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.Nil(t, conn)
		require.ErrorContains(t, err, "unable to resolve address")
	})

	t.Run("connection error when nothing is listening", func(t *testing.T) {
		// Reserve then release a loopback port so the dial is refused deterministically.
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := l.Addr().String()
		require.NoError(t, l.Close())

		rp := New("addr", addr, nil, nil)

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.Nil(t, conn)
		require.ErrorContains(t, err, "unable to connect to redis")
	})

	t.Run("returns the connection when the upstream accepts without TLS", func(t *testing.T) {
		l := fakeUpstreamListener(t)
		rp := New("addr", l.Addr().String(), nil, nil)

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.NoError(t, err)
		require.NotNil(t, conn)
		_ = conn.Close()
	})

	t.Run("reports a failure to load the upstream CA certificate", func(t *testing.T) {
		l := fakeUpstreamListener(t)
		rp := New("addr", l.Addr().String(), nil, nil)
		rp.SetUpstreamTLSCAPath(filepath.Join(t.TempDir(), "missing-ca.pem"))

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.Nil(t, conn)
		require.ErrorContains(t, err, "failed to load CA certificate")
	})

	t.Run("returns a plain connection and warns when server TLS is on but upstream TLS is off", func(t *testing.T) {
		l := fakeUpstreamListener(t)
		rp := New("addr", l.Addr().String(), nil, nil)
		rp.SetTLSEnabled(true) // server TLS on, no upstream TLS configured

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.NoError(t, err)
		require.NotNil(t, conn)
		_ = conn.Close()
	})

	t.Run("a handshake failure is reported as such", func(t *testing.T) {
		l := closingUpstreamListener(t)
		rp := New("addr", l.Addr().String(), nil, nil)
		rp.SetUpstreamTLSInsecure(true)

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.Nil(t, conn)
		require.ErrorContains(t, err, "TLS handshake failed")
	})

	t.Run("insecure upstream TLS accepts a certificate that no configured CA trusts", func(t *testing.T) {
		_, serverCert := upstreamTLSServerCert(t)
		l := tlsUpstreamServer(t, serverCert)

		rp := New("addr", l.Addr().String(), nil, nil)
		rp.SetUpstreamTLSInsecure(true)

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.NoError(t, err)
		require.NotNil(t, conn)
		require.IsType(t, &tls.Conn{}, conn)
		_ = conn.Close()
	})

	t.Run("rejects a server certificate that the configured CA does not trust", func(t *testing.T) {
		_, serverCert := upstreamTLSServerCert(t)
		l := tlsUpstreamServer(t, serverCert)

		otherCAPEM, _ := upstreamTLSServerCert(t)
		pool := x509.NewCertPool()
		require.True(t, pool.AppendCertsFromPEM(otherCAPEM))

		rp := New("addr", l.Addr().String(), nil, nil)
		rp.SetUpstreamTLSCA(pool)

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.Nil(t, conn)
		require.ErrorContains(t, err, "TLS handshake failed")
		var verifyErr *tls.CertificateVerificationError
		require.ErrorAs(t, err, &verifyErr)
	})

	t.Run("establishes a verified TLS connection with a CA pool", func(t *testing.T) {
		certPEM, serverCert := upstreamTLSServerCert(t)
		l := tlsUpstreamServer(t, serverCert)

		pool := x509.NewCertPool()
		require.True(t, pool.AppendCertsFromPEM(certPEM))

		rp := New("addr", l.Addr().String(), nil, nil)
		rp.SetUpstreamTLSCA(pool)

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.NoError(t, err)
		require.NotNil(t, conn)
		require.IsType(t, &tls.Conn{}, conn)
		_ = conn.Close()
	})

	t.Run("establishes a verified TLS connection with a CA from a file", func(t *testing.T) {
		certPEM, serverCert := upstreamTLSServerCert(t)
		l := tlsUpstreamServer(t, serverCert)

		caPath := filepath.Join(t.TempDir(), "ca.pem")
		require.NoError(t, os.WriteFile(caPath, certPEM, 0o600))

		rp := New("addr", l.Addr().String(), nil, nil)
		rp.SetUpstreamTLSCAPath(caPath)

		conn, err := rp.establishConnectionToPrincipalRedis(logEntry)

		require.NoError(t, err)
		require.NotNil(t, conn)
		require.IsType(t, &tls.Conn{}, conn)
		_ = conn.Close()
	})
}

// closingUpstreamListener accepts connections and immediately closes them, so a
// TLS handshake against it fails fast (EOF) instead of blocking on the dial
// deadline.
func closingUpstreamListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	t.Cleanup(func() { _ = l.Close() })
	return l
}

// upstreamTLSServerCert returns a self-signed certificate (PEM) valid for
// 127.0.0.1 and the matching tls.Certificate, for use as a trusted upstream
// Redis TLS server.
func upstreamTLSServerCert(t *testing.T) (certPEM []byte, serverCert tls.Certificate) {
	t.Helper()
	templ := testcerts.DefaultCertTempl
	templ.IsCA = true
	templ.KeyUsage |= x509.KeyUsageCertSign
	templ.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}

	certPEM, keyPEM := testcerts.CreateSelfSignedCert(t, "rsa", templ)
	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	return certPEM, serverCert
}

// tlsUpstreamServer starts a TLS listener on a loopback ephemeral port that
// drives the server side of the handshake and holds accepted connections until
// the test ends.
func tlsUpstreamServer(t *testing.T, serverCert tls.Certificate) net.Listener {
	t.Helper()
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{serverCert}})
	require.NoError(t, err)

	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			// Drive the server side of the handshake so the client's completes.
			if tc, ok := c.(*tls.Conn); ok {
				go func() { _ = tc.Handshake() }()
			}
		}
	}()

	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	})

	return l
}
