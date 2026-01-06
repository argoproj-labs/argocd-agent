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

	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
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

func Test_parseSecretRef(t *testing.T) {
	testCases := []struct {
		name              string
		secretRef         string
		defaultNamespace  string
		expectedNamespace string
		expectedName      string
	}{
		{
			name:              "simple name without namespace",
			secretRef:         "my-secret",
			defaultNamespace:  "default-ns",
			expectedNamespace: "default-ns",
			expectedName:      "my-secret",
		},
		{
			name:              "name with namespace",
			secretRef:         "custom-ns/my-secret",
			defaultNamespace:  "default-ns",
			expectedNamespace: "custom-ns",
			expectedName:      "my-secret",
		},
		{
			name:              "name with namespace containing slashes in name",
			secretRef:         "ns/secret/with/slashes",
			defaultNamespace:  "default-ns",
			expectedNamespace: "ns",
			expectedName:      "secret/with/slashes",
		},
		{
			name:              "empty namespace prefix",
			secretRef:         "/my-secret",
			defaultNamespace:  "default-ns",
			expectedNamespace: "default-ns",
			expectedName:      "my-secret",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			namespace, name := parseSecretRef(tc.secretRef, tc.defaultNamespace)
			assert.Equal(t, tc.expectedNamespace, namespace)
			assert.Equal(t, tc.expectedName, name)
		})
	}
}

func Test_readTLSFromExistingSecrets(t *testing.T) {
	// Load test certificate data
	certPem := testutil.MustReadFile("testdata/001_test_cert.pem")
	keyPem := testutil.MustReadFile("testdata/001_test_key.pem")

	// Save original globalOpts and restore after test
	originalNamespace := globalOpts.principalNamespace
	defer func() {
		globalOpts.principalNamespace = originalNamespace
	}()
	globalOpts.principalNamespace = "argocd"

	t.Run("Valid TLS and CA secrets in default namespace", func(t *testing.T) {
		tlsSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-tls-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"tls.crt": certPem,
				"tls.key": keyPem,
			},
		}
		caSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-ca-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"ca.crt": certPem, // Use cert as CA for testing
			},
		}

		clientset := kubefake.NewSimpleClientset(tlsSecret, caSecret)
		clt := &kube.KubernetesClient{Clientset: clientset}

		clientCert, clientKey, caData, err := readTLSFromExistingSecrets(context.TODO(), clt, "my-tls-secret", "my-ca-secret")
		require.NoError(t, err)
		assert.Equal(t, string(certPem), clientCert)
		assert.Equal(t, string(keyPem), clientKey)
		assert.Equal(t, string(certPem), caData)
	})

	t.Run("Valid TLS and CA secrets with explicit namespace", func(t *testing.T) {
		tlsSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-tls-secret",
				Namespace: "custom-ns",
			},
			Data: map[string][]byte{
				"tls.crt": certPem,
				"tls.key": keyPem,
			},
		}
		caSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-ca-secret",
				Namespace: "other-ns",
			},
			Data: map[string][]byte{
				"ca.crt": certPem,
			},
		}

		clientset := kubefake.NewSimpleClientset(tlsSecret, caSecret)
		clt := &kube.KubernetesClient{Clientset: clientset}

		clientCert, clientKey, caData, err := readTLSFromExistingSecrets(context.TODO(), clt, "custom-ns/my-tls-secret", "other-ns/my-ca-secret")
		require.NoError(t, err)
		assert.Equal(t, string(certPem), clientCert)
		assert.Equal(t, string(keyPem), clientKey)
		assert.Equal(t, string(certPem), caData)
	})

	t.Run("TLS secret not found", func(t *testing.T) {
		caSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-ca-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"ca.crt": certPem,
			},
		}

		clientset := kubefake.NewSimpleClientset(caSecret)
		clt := &kube.KubernetesClient{Clientset: clientset}

		_, _, _, err := readTLSFromExistingSecrets(context.TODO(), clt, "my-tls-secret", "my-ca-secret")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not read TLS secret argocd/my-tls-secret")
	})

	t.Run("CA secret not found", func(t *testing.T) {
		tlsSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-tls-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"tls.crt": certPem,
				"tls.key": keyPem,
			},
		}

		clientset := kubefake.NewSimpleClientset(tlsSecret)
		clt := &kube.KubernetesClient{Clientset: clientset}

		_, _, _, err := readTLSFromExistingSecrets(context.TODO(), clt, "my-tls-secret", "my-ca-secret")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not read CA secret argocd/my-ca-secret")
	})

	t.Run("TLS secret missing tls.crt key", func(t *testing.T) {
		tlsSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-tls-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"tls.key": keyPem,
			},
		}
		caSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-ca-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"ca.crt": certPem,
			},
		}

		clientset := kubefake.NewSimpleClientset(tlsSecret, caSecret)
		clt := &kube.KubernetesClient{Clientset: clientset}

		_, _, _, err := readTLSFromExistingSecrets(context.TODO(), clt, "my-tls-secret", "my-ca-secret")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain key 'tls.crt'")
	})

	t.Run("TLS secret missing tls.key key", func(t *testing.T) {
		tlsSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-tls-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"tls.crt": certPem,
			},
		}
		caSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-ca-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"ca.crt": certPem,
			},
		}

		clientset := kubefake.NewSimpleClientset(tlsSecret, caSecret)
		clt := &kube.KubernetesClient{Clientset: clientset}

		_, _, _, err := readTLSFromExistingSecrets(context.TODO(), clt, "my-tls-secret", "my-ca-secret")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain key 'tls.key'")
	})

	t.Run("CA secret missing ca.crt key", func(t *testing.T) {
		tlsSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-tls-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"tls.crt": certPem,
				"tls.key": keyPem,
			},
		}
		caSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-ca-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"other-key": certPem,
			},
		}

		clientset := kubefake.NewSimpleClientset(tlsSecret, caSecret)
		clt := &kube.KubernetesClient{Clientset: clientset}

		_, _, _, err := readTLSFromExistingSecrets(context.TODO(), clt, "my-tls-secret", "my-ca-secret")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain key 'ca.crt'")
	})

	t.Run("Invalid TLS certificate/key pair", func(t *testing.T) {
		tlsSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-tls-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"tls.crt": []byte("invalid cert"),
				"tls.key": []byte("invalid key"),
			},
		}
		caSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-ca-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"ca.crt": certPem,
			},
		}

		clientset := kubefake.NewSimpleClientset(tlsSecret, caSecret)
		clt := &kube.KubernetesClient{Clientset: clientset}

		_, _, _, err := readTLSFromExistingSecrets(context.TODO(), clt, "my-tls-secret", "my-ca-secret")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid TLS certificate/key pair")
	})

	t.Run("Secrets in different namespaces", func(t *testing.T) {
		tlsSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tls-secret",
				Namespace: "tls-namespace",
			},
			Data: map[string][]byte{
				"tls.crt": certPem,
				"tls.key": keyPem,
			},
		}
		caSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ca-secret",
				Namespace: "ca-namespace",
			},
			Data: map[string][]byte{
				"ca.crt": certPem,
			},
		}

		clientset := kubefake.NewSimpleClientset(tlsSecret, caSecret)
		clt := &kube.KubernetesClient{Clientset: clientset}

		clientCert, clientKey, caData, err := readTLSFromExistingSecrets(context.TODO(), clt, "tls-namespace/tls-secret", "ca-namespace/ca-secret")
		require.NoError(t, err)
		assert.Equal(t, string(certPem), clientCert)
		assert.Equal(t, string(keyPem), clientKey)
		assert.Equal(t, string(certPem), caData)
	})
}
