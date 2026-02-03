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

package clusterregistration

import (
	"context"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	testNamespace         = "argocd"
	testResourceProxyAddr = "resource-proxy.argocd.svc:8443"
	testAgentName         = "test-agent"
)

func getClusterSecretName(agentName string) string {
	return "cluster-" + agentName
}

func createTestCASecret(t *testing.T, kubeclient kubernetes.Interface, namespace string) {
	t.Helper()
	caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
	require.NoError(t, err, "generate CA certificate")

	_, err = kubeclient.CoreV1().Secrets(namespace).Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.SecretNamePrincipalCA,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": []byte(caCertPEM),
			"tls.key": []byte(caKeyPEM),
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err, "create CA secret")
}

func Test_NewClusterRegistrationManager(t *testing.T) {
	t.Run("Create manager with self cluster registration enabled", func(t *testing.T) {
		kubeclient := kube.NewFakeKubeClient(testNamespace)
		mgr := NewClusterRegistrationManager(true, testNamespace, testResourceProxyAddr, kubeclient)

		require.NotNil(t, mgr)
		assert.True(t, mgr.selfClusterRegistrationEnabled)
		assert.Equal(t, testNamespace, mgr.namespace)
		assert.Equal(t, testResourceProxyAddr, mgr.resourceProxyAddress)
		assert.NotNil(t, mgr.kubeclient)
	})

	t.Run("Create manager with self cluster registration disabled", func(t *testing.T) {
		kubeclient := kube.NewFakeKubeClient(testNamespace)
		mgr := NewClusterRegistrationManager(false, testNamespace, testResourceProxyAddr, kubeclient)

		require.NotNil(t, mgr)
		assert.False(t, mgr.selfClusterRegistrationEnabled)
		assert.Equal(t, testNamespace, mgr.namespace)
		assert.Equal(t, testResourceProxyAddr, mgr.resourceProxyAddress)
	})
}

func Test_RegisterCluster(t *testing.T) {
	t.Run("Returns nil when self cluster registration is disabled", func(t *testing.T) {
		kubeclient := kube.NewFakeKubeClient(testNamespace)
		mgr := NewClusterRegistrationManager(false, testNamespace, testResourceProxyAddr, kubeclient)

		err := mgr.RegisterCluster(context.Background(), testAgentName)

		assert.NoError(t, err)

		// Verify no secret was created
		_, err = kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			getClusterSecretName(testAgentName),
			metav1.GetOptions{},
		)
		assert.Error(t, err) // Should not find the secret
	})

	t.Run("Skips registration when cluster secret already exists", func(t *testing.T) {

		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      getClusterSecretName(testAgentName),
				Namespace: testNamespace,
			},
			Data: map[string][]byte{
				"server": []byte("https://existing-server"),
			},
		}
		kubeclient := kube.NewFakeClientsetWithResources(existingSecret)
		mgr := NewClusterRegistrationManager(true, testNamespace, testResourceProxyAddr, kubeclient)

		err := mgr.RegisterCluster(context.Background(), testAgentName)

		assert.NoError(t, err)

		// Verify secret still has original data
		secret, err := kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			getClusterSecretName(testAgentName),
			metav1.GetOptions{},
		)
		require.NoError(t, err)
		assert.Equal(t, []byte("https://existing-server"), secret.Data["server"])
	})

	t.Run("Creates cluster secret when it does not exist", func(t *testing.T) {

		kubeclient := kube.NewFakeClientsetWithResources()
		createTestCASecret(t, kubeclient, testNamespace)
		mgr := NewClusterRegistrationManager(true, testNamespace, testResourceProxyAddr, kubeclient)

		err := mgr.RegisterCluster(context.Background(), testAgentName)

		require.NoError(t, err)

		// Verify the cluster secret was created
		secret, err := kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			getClusterSecretName(testAgentName),
			metav1.GetOptions{},
		)
		require.NoError(t, err)
		assert.NotNil(t, secret)
		assert.Equal(t, getClusterSecretName(testAgentName), secret.Name)
	})

	t.Run("Returns error when CA secret is missing", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		mgr := NewClusterRegistrationManager(true, testNamespace, testResourceProxyAddr, kubeclient)

		err := mgr.RegisterCluster(context.Background(), testAgentName)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to self register agent's cluster")
	})

	t.Run("Creates cluster secret with correct agent name label", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		createTestCASecret(t, kubeclient, testNamespace)
		mgr := NewClusterRegistrationManager(true, testNamespace, testResourceProxyAddr, kubeclient)

		agentName := "my-special-agent"
		err := mgr.RegisterCluster(context.Background(), agentName)

		require.NoError(t, err)

		// Verify the cluster secret has the correct agent name in its labels
		secret, err := kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			getClusterSecretName(agentName),
			metav1.GetOptions{},
		)
		require.NoError(t, err)
		assert.Equal(t, agentName, secret.Labels[cluster.LabelKeyClusterAgentMapping])
	})
}
