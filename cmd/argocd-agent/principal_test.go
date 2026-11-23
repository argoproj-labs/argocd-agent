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
	"context"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/pkg/ha"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAuthTLSPairing(t *testing.T) {
	tests := []struct {
		name              string
		authMethod        string
		insecurePlaintext bool
		expectError       bool
		errorContains     string
	}{
		{
			name:              "valid: header auth with plaintext mode",
			authMethod:        auth.MethodHeader,
			insecurePlaintext: true,
			expectError:       false,
		},
		{
			name:              "valid: mtls auth with TLS mode",
			authMethod:        auth.MethodMTLS,
			insecurePlaintext: false,
			expectError:       false,
		},
		{
			name:              "valid: userpass auth with TLS mode",
			authMethod:        auth.MethodUserPass,
			insecurePlaintext: false,
			expectError:       false,
		},
		{
			name:              "valid: userpass auth with plaintext mode",
			authMethod:        auth.MethodUserPass,
			insecurePlaintext: true,
			expectError:       false,
		},
		{
			name:              "invalid: header auth without plaintext mode",
			authMethod:        auth.MethodHeader,
			insecurePlaintext: false,
			expectError:       true,
			errorContains:     "header-based authentication requires --insecure-plaintext=true",
		},
		{
			name:              "invalid: mtls auth with plaintext mode",
			authMethod:        auth.MethodMTLS,
			insecurePlaintext: true,
			expectError:       true,
			errorContains:     "mtls authentication cannot be used with --insecure-plaintext",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAuthTLSPairing(tt.authMethod, tt.insecurePlaintext)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					require.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHAAllowedReplicationClientsFlags(t *testing.T) {
	tests := []struct {
		name     string
		envVal   *string
		args     []string
		expected []string
	}{
		{
			name:     "unset CLI flag and unset env var produce empty allowlist",
			envVal:   nil,
			args:     nil,
			expected: nil,
		},
		{
			name:     "empty env var produces empty allowlist",
			envVal:   ptr(""),
			args:     nil,
			expected: nil,
		},
		{
			name:     "whitespace and comma env var produces empty allowlist",
			envVal:   ptr(" ,  "),
			args:     nil,
			expected: nil,
		},
		{
			name:     "empty CLI flag produces empty allowlist",
			envVal:   nil,
			args:     []string{"--ha-allowed-replication-clients="},
			expected: nil,
		},
		{
			name:     "whitespace and comma CLI flag produces empty allowlist",
			envVal:   nil,
			args:     []string{"--ha-allowed-replication-clients", " , "},
			expected: nil,
		},
		{
			name:     "empty CLI flag overrides non-empty env var to empty allowlist",
			envVal:   ptr("ha-replica"),
			args:     []string{"--ha-allowed-replication-clients="},
			expected: nil,
		},
		{
			name:     "env var populates explicit allowlist",
			envVal:   ptr("ha-replica, peer-2"),
			args:     nil,
			expected: []string{"ha-replica", "peer-2"},
		},
		{
			name:     "CLI flag populates explicit allowlist and drops blank items",
			envVal:   nil,
			args:     []string{"--ha-allowed-replication-clients", "ha-replica, , peer-2"},
			expected: []string{"ha-replica", "peer-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal != nil {
				t.Setenv("ARGOCD_PRINCIPAL_HA_ALLOWED_REPLICATION_CLIENTS", *tt.envVal)
			}

			cmd := NewPrincipalRunCommand()
			err := cmd.Flags().Parse(tt.args)
			require.NoError(t, err)

			clients, err := cmd.Flags().GetStringSlice("ha-allowed-replication-clients")
			require.NoError(t, err)

			haOpts := buildHAOptions(haFlagOptions{
				enabled:                   true,
				preferredRole:             "primary",
				allowedReplicationClients: clients,
			})
			ctrl, err := ha.NewController(context.Background(), haOpts...)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, ctrl.Options().AllowedReplicationClients)
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}
