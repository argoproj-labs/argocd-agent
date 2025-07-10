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
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/argoproj/argo-cd/v3/common"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpdateClusterConnectionInfo updates cluster info with connection state and time in mapped cluster
func (m *Manager) UpdateClusterConnectionInfo(agentName, status string, t time.Time) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Check if we have a mapping for the requested agent
	cluster := m.mapping(agentName)
	if cluster == nil {
		log().Errorf("Agent %s is not mapped to any cluster", agentName)
		return
	}

	// Create Redis client and cache
	redisOptions := &redis.Options{Addr: m.redisAddress}

	if err := common.SetOptionalRedisPasswordFromKubeConfig(m.ctx, m.kubeclient, m.namespace, redisOptions); err != nil {
		log().Errorf("Failed to fetch & set redis password for namespace %s: %v", m.namespace, err)
	}
	redisClient := redis.NewClient(redisOptions)

	cache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(redisClient, time.Minute, m.redisCompressionType)), time.Minute)

	state := "disconnected"
	if status == appv1.ConnectionStatusSuccessful {
		state = "connected"
	}

	// update the info
	if err := cache.SetClusterInfo(cluster.Server,
		&appv1.ClusterInfo{
			ConnectionState: appv1.ConnectionState{
				Status:     status,
				Message:    fmt.Sprintf("Agent: '%s' is %s with principal", agentName, state),
				ModifiedAt: &metav1.Time{Time: t}}},
	); err != nil {
		log().Errorf("Failed to update connection info in Cluster: '%s' mapped with Agent: '%s'. Error: %v", cluster.Name, agentName, err)
		return
	}

	log().Infof("Updated connection status to '%s' in Cluster: '%s' mapped with Agent: '%s'", status, cluster.Name, agentName)
}

// refreshClusterConnectionInfo gets latest cluster info from cache and re-saves it to avoid deletion of info
// by ArgoCD after cache expiration time duration (i.e. 10 Minute)
func (m *Manager) refreshClusterConnectionInfo() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// iterate through all clusters
	for agentName, cluster := range m.clusters {
		// Create Redis client and cache
		redisOptions := &redis.Options{Addr: m.redisAddress}

		if err := common.SetOptionalRedisPasswordFromKubeConfig(m.ctx, m.kubeclient, m.namespace, redisOptions); err != nil {
			log().Errorf("Failed to fetch & set redis password for namespace %s: %v", m.namespace, err)
		}
		redisClient := redis.NewClient(redisOptions)

		cache := appstatecache.NewCache(cacheutil.NewCache(
			cacheutil.NewRedisCache(redisClient, time.Minute, m.redisCompressionType)), time.Minute)

		// fetch latest info
		clusterInfo := &appv1.ClusterInfo{}
		if err := cache.GetClusterInfo(cluster.Server, clusterInfo); err != nil {
			log().Errorf("Failed to get connection info from Cluster: '%s' mapped with Agent: '%s'. Error: %v", cluster.Name, agentName, err)
			return
		}

		// re-save same info
		if err := cache.SetClusterInfo(cluster.Server, clusterInfo); err != nil {
			log().Errorf("Failed to refresh connection info in Cluster: '%s' mapped with Agent: '%s'. Error: %v", cluster.Name, agentName, err)
			return
		}
	}
}
