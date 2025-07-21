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
	"errors"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	rediscache "github.com/go-redis/cache/v9"
	"github.com/go-redis/redismock/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func Test_stripNamespaceFromKeyForAutonomousAgent(t *testing.T) {

	logCtx := log()

	type testEntry struct {
		name          string
		key           string
		expectedValue string
		errorExpected bool
	}

	testEntries := []testEntry{
		{
			name:          "'app|managed-resources' is correctly stripped",
			key:           "app|managed-resources|agent-autonomous_my-app|1.8.3",
			expectedValue: "app|managed-resources|my-app|1.8.3",
			errorExpected: false,
		},
		{
			name:          "'app|resources-tree' is correctly stripped",
			key:           "app|resources-tree|agent-autonomous_my-app|1.8.3.gz",
			expectedValue: "app|resources-tree|my-app|1.8.3.gz",
			errorExpected: false,
		},
		{
			name:          "unexpected key format with an unexpected prefix",
			key:           "unexpected-key-format",
			expectedValue: "",
			errorExpected: true,
		},
		{
			name:          "expected prefix, but unexpected format",
			key:           "app|managed-resources|agent-autonomous_my-app|UNEXPECTED-FORMAT|1.8.3",
			expectedValue: "",
			errorExpected: true,
		},
		{
			name:          "expected prefix, expecteded format, but no underscore",
			key:           "app|managed-resources|agent-autonomous!my-app|1.8.3",
			expectedValue: "",
			errorExpected: true,
		},
	}

	for _, testEntry := range testEntries {

		t.Run(testEntry.name, func(t *testing.T) {

			res, err := stripNamespaceFromRedisKey(testEntry.key, logCtx)

			require.Equal(t, testEntry.expectedValue, res)
			require.Equal(t, testEntry.errorExpected, err != nil, "error: %v", err)

		})
	}
}

func TestAgent_handleRedisGetMessage(t *testing.T) {
	logCtx := logrus.NewEntry(logrus.New())

	tests := []struct {
		name             string
		redisKey         string
		mockSetup        func(mock redismock.ClientMock, agentMode types.AgentMode)
		expectedResult   *event.RedisResponseBody
		expectedError    bool
		expectedErrorMsg string
	}{
		{
			name:     "successful cache hit",
			redisKey: "app|managed-resources|agent_my-app|1.8.3",
			mockSetup: func(mock redismock.ClientMock, agentMode types.AgentMode) {
				testData := []byte("test-data")
				if agentMode == types.AgentModeAutonomous {
					// Autonomous agent will strip the agent prefix from the key
					mock.ExpectGet("app|managed-resources|my-app|1.8.3").SetVal(string(testData))
				} else {
					mock.ExpectGet("app|managed-resources|my-app|1.8.3").SetVal(string(testData))
				}
			},
			expectedResult: &event.RedisResponseBody{
				Get: &event.RedisResponseBodyGet{
					Bytes:    []byte("test-data"),
					CacheHit: true,
					Error:    "",
				},
			},
			expectedError: false,
		},
		{
			name:     "cache miss",
			redisKey: "app|managed-resources|agent_missing-app|1.8.3",
			mockSetup: func(mock redismock.ClientMock, agentMode types.AgentMode) {
				if agentMode == types.AgentModeAutonomous {
					// Autonomous agent will strip the agent prefix from the key
					mock.ExpectGet("app|managed-resources|missing-app|1.8.3").RedisNil()
				} else {
					mock.ExpectGet("app|managed-resources|missing-app|1.8.3").RedisNil()
				}
			},
			expectedResult: &event.RedisResponseBody{
				Get: &event.RedisResponseBodyGet{
					Bytes:    nil,
					CacheHit: false,
					Error:    "",
				},
			},
			expectedError: false,
		},
		{
			name:     "redis error in managed mode",
			redisKey: "app|managed-resources|agent_error-app|1.8.3",
			mockSetup: func(mock redismock.ClientMock, agentMode types.AgentMode) {
				if agentMode == types.AgentModeAutonomous {
					// Autonomous agent will strip the agent prefix from the key
					mock.ExpectGet("app|managed-resources|error-app|1.8.3").SetErr(errors.New("redis connection failed"))
				} else {
					mock.ExpectGet("app|managed-resources|error-app|1.8.3").SetErr(errors.New("redis connection failed"))
				}
			},
			expectedResult: &event.RedisResponseBody{
				Get: &event.RedisResponseBodyGet{
					Bytes:    nil,
					CacheHit: false,
					Error:    "redis connection failed",
				},
			},
			expectedError: false,
		},
	}

	agentModes := []types.AgentMode{types.AgentModeManaged, types.AgentModeAutonomous}

	for _, agentMode := range agentModes {

		for _, tt := range tests {
			t.Run(tt.name+"_"+string(agentMode), func(t *testing.T) {
				// Create mock Redis client
				mockClient, mock := redismock.NewClientMock()

				// Setup mock expectations
				tt.mockSetup(mock, agentMode)

				// Create test agent
				agent := &Agent{
					mode: agentMode,
					redisProxyMsgHandler: &redisProxyMsgHandler{
						argoCDRedisCache: rediscache.New(&rediscache.Options{Redis: mockClient}),
					},
				}

				// Create test request
				request := &event.RedisRequest{
					UUID:           "test-uuid",
					ConnectionUUID: "test-connection-uuid",
					Body: event.RedisCommandBody{
						Get: &event.RedisCommandBodyGet{
							Key: tt.redisKey,
						},
					},
				}

				// Call the function under test
				result, err := agent.handleRedisGetMessage(logCtx, request)

				// Check error expectations
				if tt.expectedError {
					require.Error(t, err)
					if tt.expectedErrorMsg != "" {
						require.Contains(t, err.Error(), tt.expectedErrorMsg)
					}
					require.Nil(t, result)
				} else {
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, tt.expectedResult, result)
				}

				// Verify all mock expectations were met
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}

	}
}

func TestAgent_handleRedisSubscribeMessage_ManagedMode(t *testing.T) {
	logCtx := logrus.NewEntry(logrus.New())

	tests := []struct {
		name             string
		channelName      string
		expectedResult   *event.RedisResponseBody
		expectedError    bool
		expectedErrorMsg string
	}{
		{
			name:        "successful subscription",
			channelName: "app|managed-resources|agent_my-app|1.8.3",
			expectedResult: &event.RedisResponseBody{
				SubscribeResponse: &event.RedisResponseBodySubscribeResponse{
					Error: "",
				},
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock Redis client - Note: We cannot mock Subscribe as it's not supported by redismock
			// So we will test the parts of the function we can test without actually calling Subscribe
			mockClient, _ := redismock.NewClientMock()

			// Create connection entries map
			connections := &connectionEntries{
				connMap: make(map[string]connectionEntry),
			}

			// Create test agent in managed mode
			agent := &Agent{
				mode: types.AgentModeManaged,
				redisProxyMsgHandler: &redisProxyMsgHandler{
					argoCDRedisClient: mockClient,
					connections:       connections,
				},
			}

			// Create test request
			connectionUUID := "test-connection-uuid"
			request := &event.RedisRequest{
				UUID:           "test-uuid",
				ConnectionUUID: connectionUUID,
				Body: event.RedisCommandBody{
					Subscribe: &event.RedisCommandBodySubscribe{
						ChannelName: tt.channelName,
					},
				},
			}

			// Call the function under test
			result, err := agent.handleRedisSubscribeMessage(logCtx, request)

			// Check error expectations
			if tt.expectedError {
				require.Error(t, err)
				if tt.expectedErrorMsg != "" {
					require.Contains(t, err.Error(), tt.expectedErrorMsg)
				}
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, tt.expectedResult, result)

				// Verify connection lifecycle was updated
				connections.lock.RLock()
				connEntry, exists := connections.connMap[connectionUUID]
				connections.lock.RUnlock()

				require.True(t, exists, "Connection entry should exist")
				require.WithinDuration(t, time.Now(), connEntry.lastPing, 5*time.Second, "lastPing should be recent")
			}

			// Note: We skip mock expectations verification since Subscribe is not supported in redismock
		})
	}
}
