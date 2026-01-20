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

package header

import (
	"context"
	"regexp"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestHeaderAuthentication_Authenticate(t *testing.T) {
	tests := []struct {
		name            string
		headerName      string
		extractionRegex string
		headers         map[string]string
		expectedAgentID string
		expectError     bool
		errorContains   string
	}{
		{
			name:            "XFCC header with SPIFFE URI - extract service account",
			headerName:      "x-forwarded-client-cert",
			extractionRegex: `^.*URI=spiffe://[^/]+/ns/[^/]+/sa/([^,;]+)`,
			headers: map[string]string{
				"x-forwarded-client-cert": "By=spiffe://cluster.local/ns/argocd/sa/principal;Hash=abc;URI=spiffe://cluster.local/ns/agent-ns/sa/my-agent",
			},
			expectedAgentID: "my-agent",
			expectError:     false,
		},
		{
			name:            "XFCC header with SPIFFE URI - extract namespace",
			headerName:      "x-forwarded-client-cert",
			extractionRegex: `^.*URI=spiffe://[^/]+/ns/([^/]+)/sa/`,
			headers: map[string]string{
				"x-forwarded-client-cert": "By=spiffe://cluster.local/ns/argocd/sa/principal;Hash=abc;URI=spiffe://cluster.local/ns/agent-namespace/sa/argocd-agent",
			},
			expectedAgentID: "agent-namespace",
			expectError:     false,
		},
		{
			name:            "Simple identity header",
			headerName:      "x-client-id",
			extractionRegex: `^(.+)$`,
			headers: map[string]string{
				"x-client-id": "my-agent-001",
			},
			expectedAgentID: "my-agent-001",
			expectError:     false,
		},
		{
			name:            "Custom header with prefix extraction",
			headerName:      "x-authenticated-user",
			extractionRegex: `^user-([^@]+)@`,
			headers: map[string]string{
				"x-authenticated-user": "user-agent123@example.com",
			},
			expectedAgentID: "agent123",
			expectError:     false,
		},
		{
			name:            "Header name is case-insensitive",
			headerName:      "X-Client-ID",
			extractionRegex: `^(.+)$`,
			headers: map[string]string{
				"x-client-id": "my-agent",
			},
			expectedAgentID: "my-agent",
			expectError:     false,
		},
		{
			name:            "Missing header",
			headerName:      "x-client-id",
			extractionRegex: `^(.+)$`,
			headers:         map[string]string{},
			expectError:     true,
			errorContains:   "not found",
		},
		{
			name:            "Header value doesn't match regex",
			headerName:      "x-client-id",
			extractionRegex: `^agent-([a-z]+)$`,
			headers: map[string]string{
				"x-client-id": "invalid-format-123",
			},
			expectError:   true,
			errorContains: "does not match",
		},
		{
			name:            "Empty capture group",
			headerName:      "x-client-id",
			extractionRegex: `^agent-()$`,
			headers: map[string]string{
				"x-client-id": "agent-",
			},
			expectError:   true,
			errorContains: "empty",
		},
		{
			name:            "Invalid agent ID format (not DNS label)",
			headerName:      "x-client-id",
			extractionRegex: `^(.+)$`,
			headers: map[string]string{
				"x-client-id": "Invalid_Agent_Name!",
			},
			expectError:   true,
			errorContains: "invalid agent ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			regex := regexp.MustCompile(tt.extractionRegex)
			h := NewHeaderAuthentication(tt.headerName, regex)

			// Create context with metadata
			md := metadata.New(tt.headers)
			ctx := metadata.NewIncomingContext(context.Background(), md)

			agentID, err := h.Authenticate(ctx, auth.Credentials{})

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedAgentID, agentID)
			}
		})
	}
}

func TestHeaderAuthentication_Init(t *testing.T) {
	tests := []struct {
		name        string
		headerName  string
		regex       *regexp.Regexp
		expectError bool
	}{
		{
			name:        "Valid configuration",
			headerName:  "x-client-id",
			regex:       regexp.MustCompile(`^(.+)$`),
			expectError: false,
		},
		{
			name:        "Empty header name",
			headerName:  "",
			regex:       regexp.MustCompile(`^(.+)$`),
			expectError: true,
		},
		{
			name:        "Nil regex",
			headerName:  "x-client-id",
			regex:       nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HeaderAuthentication{
				HeaderName:      tt.headerName,
				ExtractionRegex: tt.regex,
			}

			err := h.Init()

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHeaderAuthentication_NoMetadata(t *testing.T) {
	regex := regexp.MustCompile(`^(.+)$`)
	h := NewHeaderAuthentication("x-client-id", regex)

	// Create context without metadata
	ctx := context.Background()

	_, err := h.Authenticate(ctx, auth.Credentials{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no metadata")
}
