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

package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func setup(t *testing.T, redisAddress string) (string, *Manager) {
	t.Helper()
	agentName, clusterName := "agent-test", "cluster"

	m, err := NewManager(context.Background(), "default", redisAddress, "", cacheutil.RedisCompressionNone,
		kube.NewFakeKubeClient("default"))
	require.NoError(t, err)

	// map cluster with agent
	err = m.MapCluster(agentName, &appv1.Cluster{Name: clusterName, Server: "https://test-cluster"})
	require.NoError(t, err)

	return agentName, m
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

func Test_UpdateClusterInfo(t *testing.T) {
	miniRedis, err := miniredis.Run()
	require.NoError(t, err)
	require.NotNil(t, miniRedis)
	defer miniRedis.Close()

	agentName, m := setup(t, miniRedis.Addr())

	t.Run("Update cluster info when agent is connected", func(t *testing.T) {
		// update cluster info
		m.SetAgentConnectionStatus(agentName, appv1.ConnectionStatusSuccessful, time.Now())

		// verify cluster info is updated
		clusterInfo := &appv1.ClusterInfo{}
		err := m.clusterCache.GetClusterInfo(m.mapping(agentName).Server, clusterInfo)
		require.NoError(t, err)
		require.Equal(t, clusterInfo.ConnectionState.Status, appv1.ConnectionStatusSuccessful)
		require.Equal(t, clusterInfo.ConnectionState.Message, fmt.Sprintf("Agent: '%s' is connected with principal", agentName))
		require.True(t, clusterInfo.ConnectionState.ModifiedAt.After(time.Now().Add(-2*time.Second)))

		time.Sleep(3 * time.Second)

		// re-save info
		m.refreshClusterInfo()

		// verify cluster info is same
		clusterInfoNew := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo(m.mapping(agentName).Server, clusterInfoNew)
		require.NoError(t, err)
		require.Equal(t, clusterInfo.ConnectionState.Status, clusterInfoNew.ConnectionState.Status)
		require.Equal(t, clusterInfo.ConnectionState.Message, clusterInfoNew.ConnectionState.Message)
	})

	t.Run("Update cluster info when agent is disconnected", func(t *testing.T) {
		// update cluster info
		m.SetAgentConnectionStatus(agentName, appv1.ConnectionStatusFailed, time.Now())

		// verify cluster info is updated
		clusterInfo := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo(m.mapping(agentName).Server, clusterInfo)
		require.NoError(t, err)
		require.Equal(t, clusterInfo.ConnectionState.Status, appv1.ConnectionStatusFailed)
		require.Equal(t, clusterInfo.ConnectionState.Message, fmt.Sprintf("Agent: '%s' is disconnected with principal", agentName))
		require.True(t, clusterInfo.ConnectionState.ModifiedAt.After(time.Now().Add(-2*time.Second)))

		time.Sleep(3 * time.Second)

		// re-save info
		m.refreshClusterInfo()

		// verify cluster info is same
		clusterInfoNew := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo(m.mapping(agentName).Server, clusterInfoNew)
		require.NoError(t, err)
		require.Equal(t, clusterInfo.ConnectionState.Status, clusterInfoNew.ConnectionState.Status)
		require.Equal(t, clusterInfo.ConnectionState.Message, clusterInfoNew.ConnectionState.Message)
	})
}

func Test_SetClusterCacheStats(t *testing.T) {
	miniRedis, err := miniredis.Run()
	require.NoError(t, err)
	require.NotNil(t, miniRedis)
	defer miniRedis.Close()

	agentName, m := setup(t, miniRedis.Addr())

	t.Run("Update cluster cache stats successfully", func(t *testing.T) {
		// update cluster cache stats
		m.SetClusterCacheStats(&event.ClusterCacheInfo{
			ApplicationsCount: 5,
			APIsCount:         10,
			ResourcesCount:    100,
		}, agentName)

		// verify cluster cache stats are updated
		clusterInfo := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo(m.mapping(agentName).Server, clusterInfo)
		require.NoError(t, err)
		require.Equal(t, int64(5), clusterInfo.ApplicationsCount)
		require.Equal(t, int64(10), clusterInfo.CacheInfo.APIsCount)
		require.Equal(t, int64(100), clusterInfo.CacheInfo.ResourcesCount)
	})

	t.Run("Update cluster cache info with unmapped agent", func(t *testing.T) {
		clusterCacheInfo := &event.ClusterCacheInfo{
			ApplicationsCount: 3,
			APIsCount:         5,
			ResourcesCount:    50,
		}

		// This should not panic
		require.NotPanics(t, func() {
			m.SetClusterCacheStats(clusterCacheInfo, "unmapped-agent")
		})
	})

	t.Run("Update cluster cache stats preserves connection status", func(t *testing.T) {
		// First set connection status to successful
		connectionTime := time.Now()
		m.SetAgentConnectionStatus(agentName, appv1.ConnectionStatusSuccessful, connectionTime)

		// Verify connection status is set
		cluster := m.mapping(agentName)
		require.NotNil(t, cluster)

		initialInfo := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo(cluster.Server, initialInfo)
		require.NoError(t, err)
		require.Equal(t, appv1.ConnectionStatusSuccessful, initialInfo.ConnectionState.Status)

		// Now update cache stats
		m.SetClusterCacheStats(&event.ClusterCacheInfo{
			ApplicationsCount: 8,
			APIsCount:         20,
			ResourcesCount:    200,
		}, agentName)

		// Verify that cache stats are updated but connection status is preserved
		updatedInfo := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo(cluster.Server, updatedInfo)
		require.NoError(t, err)
		require.Equal(t, int64(8), updatedInfo.ApplicationsCount)
		require.Equal(t, int64(20), updatedInfo.CacheInfo.APIsCount)
		require.Equal(t, int64(200), updatedInfo.CacheInfo.ResourcesCount)
		require.Equal(t, appv1.ConnectionStatusSuccessful, updatedInfo.ConnectionState.Status)
		require.Equal(t, initialInfo.ConnectionState.Message, updatedInfo.ConnectionState.Message)
	})
}

func Test_GetClusterInfo(t *testing.T) {
	miniRedis, err := miniredis.Run()
	require.NoError(t, err)
	require.NotNil(t, miniRedis)
	defer miniRedis.Close()

	agentName, m := setup(t, miniRedis.Addr())

	t.Run("Get cluster info successfully", func(t *testing.T) {
		// First set some cache stats
		m.SetClusterCacheStats(&event.ClusterCacheInfo{
			ApplicationsCount: 7,
			APIsCount:         15,
			ResourcesCount:    150,
		}, agentName)

		// Get cluster info from cache.
		cluster := m.mapping(agentName)
		require.NotNil(t, cluster)

		retrievedInfo := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo(cluster.Server, retrievedInfo)
		require.NoError(t, err)
		require.NotNil(t, retrievedInfo)
		require.Equal(t, int64(7), retrievedInfo.ApplicationsCount)
		require.Equal(t, int64(15), retrievedInfo.CacheInfo.APIsCount)
		require.Equal(t, int64(150), retrievedInfo.CacheInfo.ResourcesCount)
	})

	t.Run("Get cluster cache info for non-existent cluster", func(t *testing.T) {
		// Try to get info for a cluster that doesn't exist
		retrievedInfo := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo("https://non-existent-cluster", retrievedInfo)
		require.Error(t, err)
	})
}

func Test_SetAgentConnectionStatus(t *testing.T) {
	miniRedis, err := miniredis.Run()
	require.NoError(t, err)
	require.NotNil(t, miniRedis)

	defer miniRedis.Close()

	agentName, m := setup(t, miniRedis.Addr())

	t.Run("SetAgentConnectionStatus with invalid redis address", func(t *testing.T) {
		// Create a manager with invalid redis address
		invalidM, err := NewManager(context.Background(), "default", "invalid:redis:address", "",
			cacheutil.RedisCompressionNone, kube.NewFakeKubeClient("default"))
		require.NoError(t, err)

		// Map cluster with agent
		err = invalidM.MapCluster(agentName, &appv1.Cluster{Name: "cluster", Server: "https://test-cluster"})
		require.NoError(t, err)

		// This should not panic
		require.NotPanics(t, func() {
			invalidM.SetAgentConnectionStatus(agentName, appv1.ConnectionStatusSuccessful, time.Now())
		})
	})

	t.Run("SetAgentConnectionStatus with unmapped agent", func(t *testing.T) {
		// This should not panic
		require.NotPanics(t, func() {
			m.SetAgentConnectionStatus("non-existent-agent", appv1.ConnectionStatusSuccessful, time.Now())
		})
	})

	t.Run("SetAgentConnectionStatus resets cache information on failure", func(t *testing.T) {
		// First set dummy cache stats
		m.SetClusterCacheStats(&event.ClusterCacheInfo{
			ApplicationsCount: 9,
			APIsCount:         19,
			ResourcesCount:    190,
		}, agentName)

		// Verify that stats are set
		cluster := m.mapping(agentName)
		require.NotNil(t, cluster)
		preInfo := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo(cluster.Server, preInfo)
		require.NoError(t, err)
		require.Equal(t, int64(9), preInfo.ApplicationsCount)
		require.Equal(t, int64(19), preInfo.CacheInfo.APIsCount)
		require.Equal(t, int64(190), preInfo.CacheInfo.ResourcesCount)

		// Now mark connection as failed (i.e. agent is disconnected)
		m.SetAgentConnectionStatus(agentName, appv1.ConnectionStatusFailed, time.Now())

		// Verify counts are reset to defaults
		postInfo := &appv1.ClusterInfo{}
		err = m.clusterCache.GetClusterInfo(cluster.Server, postInfo)
		require.NoError(t, err)
		require.Equal(t, int64(0), postInfo.ApplicationsCount)
		require.Equal(t, int64(0), postInfo.CacheInfo.APIsCount)
		require.Equal(t, int64(0), postInfo.CacheInfo.ResourcesCount)
	})
}

func Test_RefreshClusterInfo(t *testing.T) {
	miniRedis, err := miniredis.Run()
	require.NoError(t, err)
	require.NotNil(t, miniRedis)
	defer miniRedis.Close()

	agentName, m := setup(t, miniRedis.Addr())

	t.Run("RefreshClusterInfo with no existing cache", func(t *testing.T) {
		// This should not panic
		require.NotPanics(t, func() {
			m.refreshClusterInfo()
		})
	})

	t.Run("RefreshClusterInfo with existing cache", func(t *testing.T) {
		// First set some info
		m.SetAgentConnectionStatus(agentName, appv1.ConnectionStatusSuccessful, time.Now())

		// Now refresh should work without error
		require.NotPanics(t, func() {
			m.refreshClusterInfo()
		})
	})

	t.Run("RefreshClusterInfo with invalid redis", func(t *testing.T) {
		// Create manager with invalid redis
		invalidM, err := NewManager(context.Background(), "default", "invalid:redis", "",
			cacheutil.RedisCompressionNone, kube.NewFakeKubeClient("default"))
		require.NoError(t, err)

		err = invalidM.MapCluster(agentName, &appv1.Cluster{Name: "cluster", Server: "https://test-cluster"})
		require.NoError(t, err)

		// This should not panic
		require.NotPanics(t, func() {
			invalidM.refreshClusterInfo()
		})
	})
}

func Test_CreateCluster(t *testing.T) {
	const testNamespace = "argocd"
	const testResourceProxyAddr = "resource-proxy:8443"

	t.Run("Returns error when CA secret is missing", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()

		err := CreateCluster(context.Background(), kubeclient, testNamespace, "test-agent", testResourceProxyAddr, "")

		require.Error(t, err)
		require.Contains(t, err.Error(), "could not generate client certificate")
	})

	t.Run("Creates cluster secret successfully with valid CA", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()
		createTestCASecret(t, kubeclient, testNamespace)

		agentName := "test-agent"
		err := CreateCluster(context.Background(), kubeclient, testNamespace, agentName, testResourceProxyAddr, "")

		require.NoError(t, err)

		secret, err := kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			getClusterSecretName(agentName),
			metav1.GetOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, secret)
		require.Equal(t, getClusterSecretName(agentName), secret.Name)
		require.Equal(t, agentName, secret.Labels[LabelKeyClusterAgentMapping])
		require.Equal(t, "true", secret.Labels[LabelKeySelfRegisteredCluster])
	})

	t.Run("Creates cluster secret with shared client cert", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()

		// Create the shared client cert secret
		sharedSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "shared-client-cert",
				Namespace: testNamespace,
			},
			Data: map[string][]byte{
				"tls.crt": []byte("shared-cert-data"),
				"tls.key": []byte("shared-key-data"),
				"ca.crt":  []byte("shared-ca-data"),
			},
		}
		_, err := kubeclient.CoreV1().Secrets(testNamespace).Create(context.Background(), sharedSecret, metav1.CreateOptions{})
		require.NoError(t, err)

		agentName := "test-agent-shared"
		err = CreateCluster(context.Background(), kubeclient, testNamespace, agentName, testResourceProxyAddr, "shared-client-cert")

		require.NoError(t, err)

		// Verify the cluster secret was created with shared cert data
		secret, err := kubeclient.CoreV1().Secrets(testNamespace).Get(
			context.Background(),
			getClusterSecretName(agentName),
			metav1.GetOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, secret)

		// Verify the cluster secret contains the shared cert data (base64 encoded in JSON)
		clusterData, ok := secret.Data["config"]
		require.True(t, ok)
		require.Contains(t, string(clusterData), "c2hhcmVkLWNlcnQtZGF0YQ==")
		require.Contains(t, string(clusterData), "c2hhcmVkLWtleS1kYXRh")
		require.Contains(t, string(clusterData), "c2hhcmVkLWNhLWRhdGE=")
	})

	t.Run("Returns error when shared client cert secret does not exist", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()

		err := CreateCluster(context.Background(), kubeclient, testNamespace, "test-agent", testResourceProxyAddr, "non-existent-secret")

		require.Error(t, err)
		require.Contains(t, err.Error(), "could not read shared client certificate from secret")
	})
}

func Test_CreateClusterWithSharedCert(t *testing.T) {
	const testNamespace = "argocd"
	const testResourceProxyAddr = "resource-proxy:8443"

	t.Run("Returns error when shared secret is missing tls.crt", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-cert",
				Namespace: testNamespace,
			},
			Data: map[string][]byte{
				"tls.key": []byte("key-data"),
				"ca.crt":  []byte("ca-data"),
			},
		}
		_, err := kubeclient.CoreV1().Secrets(testNamespace).Create(context.Background(), secret, metav1.CreateOptions{})
		require.NoError(t, err)

		err = CreateCluster(context.Background(), kubeclient, testNamespace, "test-agent", testResourceProxyAddr, "missing-cert")

		require.Error(t, err)
		require.Contains(t, err.Error(), "missing tls.crt")
	})

	t.Run("Returns error when shared secret is missing tls.key", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-key",
				Namespace: testNamespace,
			},
			Data: map[string][]byte{
				"tls.crt": []byte("cert-data"),
				"ca.crt":  []byte("ca-data"),
			},
		}
		_, err := kubeclient.CoreV1().Secrets(testNamespace).Create(context.Background(), secret, metav1.CreateOptions{})
		require.NoError(t, err)

		err = CreateCluster(context.Background(), kubeclient, testNamespace, "test-agent", testResourceProxyAddr, "missing-key")

		require.Error(t, err)
		require.Contains(t, err.Error(), "missing tls.key")
	})

	t.Run("Returns error when shared secret is missing ca.crt", func(t *testing.T) {
		kubeclient := kube.NewFakeClientsetWithResources()

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-ca",
				Namespace: testNamespace,
			},
			Data: map[string][]byte{
				"tls.crt": []byte("cert-data"),
				"tls.key": []byte("key-data"),
			},
		}
		_, err := kubeclient.CoreV1().Secrets(testNamespace).Create(context.Background(), secret, metav1.CreateOptions{})
		require.NoError(t, err)

		err = CreateCluster(context.Background(), kubeclient, testNamespace, "test-agent", testResourceProxyAddr, "missing-ca")

		require.Error(t, err)
		require.Contains(t, err.Error(), "missing ca.crt")
	})
}
