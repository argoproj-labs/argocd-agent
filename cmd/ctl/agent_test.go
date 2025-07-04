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

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_serverURL(t *testing.T) {
	testCases := []struct {
		name        string
		address     string
		agentName   string
		expected    string
		expectError bool
		errorMsg    string
	}{
		// Valid IP:port addresses
		{
			name:      "valid IPv4 address with port",
			address:   "192.168.1.100:8080",
			agentName: "test-agent",
			expected:  "https://192.168.1.100:8080?agentName=test-agent",
		},
		{
			name:      "valid IPv6 address with port",
			address:   "[::1]:8080",
			agentName: "test-agent",
			expected:  "https://[::1]:8080?agentName=test-agent",
		},
		{
			name:      "valid localhost IP with port",
			address:   "127.0.0.1:9090",
			agentName: "my-agent",
			expected:  "https://127.0.0.1:9090?agentName=my-agent",
		},

		// Valid DNS names with ports
		{
			name:      "valid DNS name with port",
			address:   "example.com:8080",
			agentName: "test-agent",
			expected:  "https://example.com:8080?agentName=test-agent",
		},
		{
			name:      "valid subdomain with port",
			address:   "api.example.com:443",
			agentName: "prod-agent",
			expected:  "https://api.example.com:443?agentName=prod-agent",
		},
		{
			name:      "valid localhost DNS with port",
			address:   "localhost:8080",
			agentName: "dev-agent",
			expected:  "https://localhost:8080?agentName=dev-agent",
		},

		// Valid agent names
		{
			name:      "agent name with hyphens",
			address:   "192.168.1.100:8080",
			agentName: "test-agent-123",
			expected:  "https://192.168.1.100:8080?agentName=test-agent-123",
		},
		{
			name:      "agent name with dots",
			address:   "192.168.1.100:8080",
			agentName: "test.agent.name",
			expected:  "https://192.168.1.100:8080?agentName=test.agent.name",
		},
		{
			name:      "numeric agent name",
			address:   "192.168.1.100:8080",
			agentName: "123",
			expected:  "https://192.168.1.100:8080?agentName=123",
		},

		// Invalid addresses - missing port
		{
			name:        "address without port",
			address:     "192.168.1.100",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid address: 192.168.1.100",
		},
		{
			name:        "DNS name without port",
			address:     "example.com",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid address: example.com",
		},

		// Invalid addresses - invalid port
		{
			name:        "invalid port - too large",
			address:     "192.168.1.100:70000",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid port: 70000",
		},
		{
			name:        "invalid port - negative",
			address:     "192.168.1.100:-1",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid port: -1",
		},
		{
			name:        "invalid port - non-numeric",
			address:     "192.168.1.100:abc",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid port: abc",
		},
		{
			name:      "valid port zero",
			address:   "192.168.1.100:0",
			agentName: "test-agent",
			expected:  "https://192.168.1.100:0?agentName=test-agent",
		},

		// Invalid addresses - malformed DNS
		{
			name:        "invalid DNS with underscore",
			address:     "invalid_host:8080",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid address: invalid_host:8080",
		},
		{
			name:        "invalid DNS with spaces",
			address:     "invalid host:8080",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid address: invalid host:8080",
		},
		{
			name:        "empty address",
			address:     "",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid address: ",
		},

		// Invalid agent names
		{
			name:        "empty agent name",
			address:     "192.168.1.100:8080",
			agentName:   "",
			expectError: true,
			errorMsg:    "invalid agent name",
		},
		{
			name:        "agent name with underscores",
			address:     "192.168.1.100:8080",
			agentName:   "test_agent",
			expectError: true,
			errorMsg:    "invalid agent name",
		},
		{
			name:        "agent name with spaces",
			address:     "192.168.1.100:8080",
			agentName:   "test agent",
			expectError: true,
			errorMsg:    "invalid agent name",
		},
		{
			name:        "agent name with uppercase",
			address:     "192.168.1.100:8080",
			agentName:   "TestAgent",
			expectError: true,
			errorMsg:    "invalid agent name",
		},
		{
			name:        "agent name starting with hyphen",
			address:     "192.168.1.100:8080",
			agentName:   "-test-agent",
			expectError: true,
			errorMsg:    "invalid agent name",
		},
		{
			name:        "agent name ending with hyphen",
			address:     "192.168.1.100:8080",
			agentName:   "test-agent-",
			expectError: true,
			errorMsg:    "invalid agent name",
		},
		{
			name:        "agent name too long",
			address:     "192.168.1.100:8080",
			agentName:   "a" + string(make([]byte, 255)), // 256 characters
			expectError: true,
			errorMsg:    "invalid agent name",
		},

		// Edge cases
		{
			name:        "colon in address without port",
			address:     "192.168.1.100:",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid port: ",
		},
		{
			name:        "multiple colons in address",
			address:     "192.168.1.100:8080:extra",
			agentName:   "test-agent",
			expectError: true,
			errorMsg:    "invalid port: 8080:extra",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := serverURL(tc.address, tc.agentName)

			if tc.expectError {
				require.Error(t, err, "Expected error for test case: %s", tc.name)
				assert.Contains(t, err.Error(), tc.errorMsg, "Error message should contain expected text")
				assert.Empty(t, result, "Result should be empty when error occurs")
			} else {
				require.NoError(t, err, "Expected no error for test case: %s", tc.name)
				assert.Equal(t, tc.expected, result, "Result should match expected URL")
			}
		})
	}
}
