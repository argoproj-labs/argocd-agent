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
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj/argo-cd/v3/common"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func setup(t *testing.T, redisAddress string) (string, *Manager) {
	t.Helper()
	agentName, clusterName := "agent-test", "cluster"
	m := &Manager{
		namespace:            "default",
		ctx:                  context.Background(),
		kubeclient:           kube.NewFakeKubeClient(),
		clusters:             make(map[string]*appv1.Cluster),
		redisAddress:         redisAddress,
		redisCompressionType: cacheutil.RedisCompressionNone,
	}

	// map cluster with agent
	err := m.MapCluster(agentName, &appv1.Cluster{Name: clusterName})
	require.NoError(t, err)

	// create redis secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: common.RedisInitialCredentials},
		Data:       map[string][]byte{common.RedisInitialCredentialsKey: []byte("password123")},
	}
	_, err = m.kubeclient.CoreV1().Secrets(m.namespace).Create(m.ctx, secret, metav1.CreateOptions{})
	require.NoError(t, err)

	return agentName, m
}

func getClusterInfo(t *testing.T, agentName string, m *Manager) appv1.ClusterInfo {

	// Create Redis client and cache
	redisOptions := &redis.Options{Addr: m.redisAddress}
	err := common.SetOptionalRedisPasswordFromKubeConfig(m.ctx, m.kubeclient, m.namespace, redisOptions)
	require.NoError(t, err)

	redisClient := redis.NewClient(redisOptions)
	cache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(redisClient, time.Hour, m.redisCompressionType)), time.Hour)

	// fetch cluster info
	var clusterInfo appv1.ClusterInfo
	cluster := m.mapping(agentName)
	err = cache.GetClusterInfo(cluster.Server, &clusterInfo)
	require.NoError(t, err)
	require.NotNil(t, clusterInfo)
	return clusterInfo
}

func Test_UpdateClusterInfo(t *testing.T) {
	miniRedis, err := miniredis.Run()
	defer miniRedis.Close()
	require.NoError(t, err)
	require.NotNil(t, miniRedis)

	agentName, m := setup(t, miniRedis.Addr())

	t.Run("Update cluster info when agent is connected", func(t *testing.T) {
		// update cluster info
		m.UpdateClusterConnectionInfo(agentName, appv1.ConnectionStatusSuccessful, time.Now())

		// verify cluster info is updated
		clusterInfo := getClusterInfo(t, agentName, m)
		require.Equal(t, clusterInfo.ConnectionState.Status, appv1.ConnectionStatusSuccessful)
		require.Equal(t, clusterInfo.ConnectionState.Message, fmt.Sprintf("Agent: '%s' is connected with principal", agentName))
		require.True(t, clusterInfo.ConnectionState.ModifiedAt.After(time.Now().Add(-2*time.Second)))

		time.Sleep(3 * time.Second)

		// re-save info
		m.refreshClusterConnectionInfo()

		// verify cluster info is same
		clusterInfoNew := getClusterInfo(t, agentName, m)
		require.Equal(t, clusterInfo, clusterInfoNew)
	})

	t.Run("Update cluster info when agent is disconnected", func(t *testing.T) {
		// update cluster info
		m.UpdateClusterConnectionInfo(agentName, appv1.ConnectionStatusFailed, time.Now())

		// verify cluster info is updated
		clusterInfo := getClusterInfo(t, agentName, m)
		require.Equal(t, clusterInfo.ConnectionState.Status, appv1.ConnectionStatusFailed)
		require.Equal(t, clusterInfo.ConnectionState.Message, fmt.Sprintf("Agent: '%s' is disconnected with principal", agentName))
		require.True(t, clusterInfo.ConnectionState.ModifiedAt.After(time.Now().Add(-2*time.Second)))

		time.Sleep(3 * time.Second)

		// re-save info
		m.refreshClusterConnectionInfo()

		// verify cluster info is same
		clusterInfoNew := getClusterInfo(t, agentName, m)
		require.Equal(t, clusterInfo, clusterInfoNew)
	})
}
