// Copyright 2026 The argocd-agent Authors
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

package registration

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	issuermocks "github.com/argoproj-labs/argocd-agent/internal/issuer/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	testNamespace            = "argocd"
	testResourceProxyAddr    = "resource-proxy.argocd.svc:8443"
	testAgentName            = "test-agent"
	testClientCertSecretName = "test-client-cert"
)

func createTestClientCertSecret(t *testing.T, kubeclient kubernetes.Interface) string {
	t.Helper()

	// Generate CA certificate
	caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
	require.NoError(t, err, "generate CA certificate")

	// Parse CA cert PEM for signing
	certBlock, _ := pem.Decode([]byte(caCertPEM))
	require.NotNil(t, certBlock, "decode CA cert PEM")
	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	require.NoError(t, err, "parse CA certificate")

	keyBlock, _ := pem.Decode([]byte(caKeyPEM))
	require.NotNil(t, keyBlock, "decode CA key PEM")
	caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	require.NoError(t, err, "parse CA private key")

	// Generate client certificate signed by CA
	clientCertPEM, clientKeyPEM, err := tlsutil.GenerateClientCertificate("shared-client", caCert, caKey)
	require.NoError(t, err, "generate client certificate")

	// Create secret with tls.crt, tls.key, and ca.crt
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClientCertSecretName,
			Namespace: testNamespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": []byte(clientCertPEM),
			"tls.key": []byte(clientKeyPEM),
			"ca.crt":  []byte(caCertPEM),
		},
	}

	_, err = kubeclient.CoreV1().Secrets(testNamespace).Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err, "create client cert secret")

	return testClientCertSecretName
}

func createMockIssuer(t *testing.T) *issuermocks.Issuer {
	t.Helper()
	iss := issuermocks.NewIssuer(t)

	iss.On("IssueResourceProxyToken", mock.Anything).Return("test-token", nil).Maybe()

	mockClaims := issuermocks.NewClaims(t)
	mockClaims.On("GetSubject").Return("test-agent", nil).Maybe()
	iss.On("ValidateResourceProxyToken", mock.Anything).Return(mockClaims, nil).Maybe()
	return iss
}

func Test_NewAgentRegistrationManager(t *testing.T) {
	t.Run("Create manager with self agent registration enabled", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		iss := createMockIssuer(t)
		clientCertSecretName := createTestClientCertSecret(t, kubeclient)
		mgr := NewAgentRegistrationManager(true, testNamespace, testResourceProxyAddr, clientCertSecretName, kubeclient, iss)

		require.NotNil(t, mgr)
		assert.True(t, mgr.selfAgentRegistrationEnabled)
		assert.Equal(t, testNamespace, mgr.namespace)
		assert.Equal(t, testResourceProxyAddr, mgr.resourceProxyAddress)
		assert.NotNil(t, mgr.kubeclient)
	})

	t.Run("Create manager with self agent registration disabled", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		iss := createMockIssuer(t)
		mgr := NewAgentRegistrationManager(false, testNamespace, testResourceProxyAddr, "", kubeclient, iss)

		require.NotNil(t, mgr)
		assert.False(t, mgr.selfAgentRegistrationEnabled)
		assert.Equal(t, testNamespace, mgr.namespace)
		assert.Equal(t, testResourceProxyAddr, mgr.resourceProxyAddress)
	})
}

func Test_RegisterAgent(t *testing.T) {
	t.Run("Returns nil when self agent registration is disabled", func(t *testing.T) {
		kubeclient := kube.NewFakeKubeClient(testNamespace)
		iss := createMockIssuer(t)
		mgr := NewAgentRegistrationManager(false, testNamespace, testResourceProxyAddr, "", kubeclient, iss)

		err := mgr.RegisterAgent(context.Background(), testAgentName)

		assert.NoError(t, err)

		// Verify no secret was created
		_, err = kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			cluster.GetClusterSecretName(testAgentName),
			metav1.GetOptions{},
		)
		assert.Error(t, err)
	})

	t.Run("Creates cluster secret when it does not exist", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		clientCertSecretName := createTestClientCertSecret(t, kubeclient)

		iss := issuermocks.NewIssuer(t)
		iss.On("IssueResourceProxyToken", testAgentName).Return("test-bearer-token", nil)

		mgr := NewAgentRegistrationManager(true, testNamespace, testResourceProxyAddr, clientCertSecretName, kubeclient, iss)

		err := mgr.RegisterAgent(context.Background(), testAgentName)

		require.NoError(t, err)

		// Verify the cluster secret was created
		secret, err := kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			cluster.GetClusterSecretName(testAgentName),
			metav1.GetOptions{},
		)
		require.NoError(t, err)
		assert.NotNil(t, secret)
		assert.Equal(t, cluster.GetClusterSecretName(testAgentName), secret.Name)
	})

	t.Run("Returns error when client cert secret is missing", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()

		iss := issuermocks.NewIssuer(t)
		iss.On("IssueResourceProxyToken", testAgentName).Return("test-bearer-token", nil)

		mgr := NewAgentRegistrationManager(true, testNamespace, testResourceProxyAddr, "nonexistent-secret", kubeclient, iss)

		err := mgr.RegisterAgent(context.Background(), testAgentName)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create self-registered cluster secret")
	})

	t.Run("Creates cluster secret with correct agent name label", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		clientCertSecretName := createTestClientCertSecret(t, kubeclient)

		agentName := "my-special-agent"
		iss := issuermocks.NewIssuer(t)
		iss.On("IssueResourceProxyToken", agentName).Return("test-bearer-token", nil)

		mgr := NewAgentRegistrationManager(true, testNamespace, testResourceProxyAddr, clientCertSecretName, kubeclient, iss)

		err := mgr.RegisterAgent(context.Background(), agentName)

		require.NoError(t, err)

		// Verify the cluster secret has the correct agent name in its labels
		secret, err := kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			cluster.GetClusterSecretName(agentName),
			metav1.GetOptions{},
		)
		require.NoError(t, err)
		assert.Equal(t, agentName, secret.Labels[cluster.LabelKeyClusterAgentMapping])
	})

	t.Run("Does not modify manually created cluster secret", func(t *testing.T) {
		// Create a manual secret without self-registered label
		manualSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.GetClusterSecretName(testAgentName),
				Namespace: testNamespace,
				Labels: map[string]string{
					cluster.LabelKeyClusterAgentMapping: testAgentName,
				},
			},
			Data: map[string][]byte{
				"config": []byte(`{"tlsClientConfig":{"caData":"Y2EtZGF0YQ==","certData":"Y2VydC1kYXRh","keyData":"a2V5LWRhdGE="}}`),
			},
		}
		kubeclient := kube.NewFakeClientsetWithResources(manualSecret)
		clientCertSecretName := createTestClientCertSecret(t, kubeclient)

		iss := issuermocks.NewIssuer(t)

		mgr := NewAgentRegistrationManager(true, testNamespace, testResourceProxyAddr, clientCertSecretName, kubeclient, iss)

		err := mgr.RegisterAgent(context.Background(), testAgentName)

		require.NoError(t, err)

		// Verify the secret was not modified (no bearer token added)
		secret, err := kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			cluster.GetClusterSecretName(testAgentName),
			metav1.GetOptions{},
		)
		require.NoError(t, err)
		assert.Empty(t, secret.Labels[cluster.LabelKeySelfRegisteredCluster])
	})
}

func Test_AgentRegistrationManager_IsSelfAgentRegistrationEnabled(t *testing.T) {
	t.Run("Returns true when enabled", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		iss := createMockIssuer(t)
		clientCertSecretName := createTestClientCertSecret(t, kubeclient)
		mgr := NewAgentRegistrationManager(true, testNamespace, testResourceProxyAddr, clientCertSecretName, kubeclient, iss)
		assert.True(t, mgr.IsSelfAgentRegistrationEnabled())
	})

	t.Run("Returns false when disabled", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		iss := createMockIssuer(t)
		mgr := NewAgentRegistrationManager(false, testNamespace, testResourceProxyAddr, "", kubeclient, iss)
		assert.False(t, mgr.IsSelfAgentRegistrationEnabled())
	})
}

func Test_RegisterCluster_TokenValidation(t *testing.T) {
	t.Run("Skips registration when cluster secret exists with valid token", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		clientCertSecretName := createTestClientCertSecret(t, kubeclient)

		// Create mock issuer that returns a valid token
		iss := issuermocks.NewIssuer(t)
		iss.On("IssueResourceProxyToken", testAgentName).Return("valid-token", nil)

		// Create mock claims for token validation
		mockClaims := issuermocks.NewClaims(t)
		mockClaims.On("GetSubject").Return(testAgentName, nil)
		iss.On("ValidateResourceProxyToken", "valid-token").Return(mockClaims, nil)

		mgr := NewAgentRegistrationManager(true, testNamespace, testResourceProxyAddr, clientCertSecretName, kubeclient, iss)

		// First registration - creates secret
		err := mgr.RegisterAgent(context.Background(), testAgentName)
		require.NoError(t, err)

		// Second registration - should skip because token is valid
		err = mgr.RegisterAgent(context.Background(), testAgentName)
		require.NoError(t, err)
	})

	t.Run("Refreshes token when existing token is invalid", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		clientCertSecretName := createTestClientCertSecret(t, kubeclient)

		// Create mock issuer
		iss := issuermocks.NewIssuer(t)
		iss.On("IssueResourceProxyToken", testAgentName).Return("new-token", nil)

		// First call creates secret, subsequent calls validate/refresh
		iss.On("ValidateResourceProxyToken", "new-token").Return(nil, fmt.Errorf("token validation failed"))

		mgr := NewAgentRegistrationManager(true, testNamespace, testResourceProxyAddr, clientCertSecretName, kubeclient, iss)

		// First registration - creates secret
		err := mgr.RegisterAgent(context.Background(), testAgentName)
		require.NoError(t, err)

		// Second registration - token validation fails, should refresh
		err = mgr.RegisterAgent(context.Background(), testAgentName)
		require.NoError(t, err)
	})

	t.Run("Refreshes token when subject does not match agent name", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		clientCertSecretName := createTestClientCertSecret(t, kubeclient)

		// Create mock issuer
		iss := issuermocks.NewIssuer(t)
		iss.On("IssueResourceProxyToken", testAgentName).Return("token-for-agent", nil)

		// Mock claims that return wrong subject
		mockClaims := issuermocks.NewClaims(t)
		mockClaims.On("GetSubject").Return("different-agent", nil)
		iss.On("ValidateResourceProxyToken", "token-for-agent").Return(mockClaims, nil)

		mgr := NewAgentRegistrationManager(true, testNamespace, testResourceProxyAddr, clientCertSecretName, kubeclient, iss)

		// First registration - creates secret
		err := mgr.RegisterAgent(context.Background(), testAgentName)
		require.NoError(t, err)

		// Second registration - subject mismatch, should refresh
		err = mgr.RegisterAgent(context.Background(), testAgentName)
		require.NoError(t, err)
	})
}
