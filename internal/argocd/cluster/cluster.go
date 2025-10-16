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
	"errors"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SetAgentConnectionStatus updates cluster info with connection state and time in mapped cluster at principal.
// This is called when the agent is connected or disconnected with the principal.
func (m *Manager) SetAgentConnectionStatus(agentName, status appv1.ConnectionStatus, modifiedAt time.Time) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Check if we have a mapping for the requested agent
	cluster := m.mapping(agentName)
	if cluster == nil {
		log().Errorf("Agent %s is not mapped to any cluster", agentName)
		return
	}

	state := "disconnected"
	if status == appv1.ConnectionStatusSuccessful {
		state = "connected"
	}

	// Update the cluster connection state and time in mapped cluster at principal.
	if err := m.setClusterInfo(cluster.Server, agentName, cluster.Name,
		&appv1.ClusterInfo{
			ConnectionState: appv1.ConnectionState{
				Status:     status,
				Message:    fmt.Sprintf("Agent: '%s' is %s with principal", agentName, state),
				ModifiedAt: &metav1.Time{Time: modifiedAt},
			},
		}); err != nil {
		log().Errorf("failed to refresh connection info in cluster: '%s' mapped with agent: '%s'. Error: %v", cluster.Name, agentName, err)
		return
	}

	log().Infof("Updated connection status to '%s' in Cluster: '%s' mapped with Agent: '%s'", status, cluster.Name, agentName)
}

// refreshClusterInfo gets latest cluster info from cache and re-saves it to avoid deletion of info
// by Argo CD after cache expiration time duration (i.e. 10 minutes)
func (m *Manager) refreshClusterInfo() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.clusterCache == nil {
		log().Warn("Cluster cache is not available, skipping refresh")
		return
	}

	// Iterate through all clusters.
	for agentName, cluster := range m.clusters {
		clusterInfo := &appv1.ClusterInfo{}

		if err := m.clusterCache.GetClusterInfo(cluster.Server, clusterInfo); err != nil {
			if !errors.Is(err, cacheutil.ErrCacheMiss) {
				log().Errorf("failed to get connection info from cluster: '%s' mapped with agent: '%s'. Error: %v", cluster.Name, agentName, err)
			}
			continue
		}

		// Re-save same info.
		if err := m.setClusterInfo(cluster.Server, agentName, cluster.Name, clusterInfo); err != nil {
			log().Errorf("failed to refresh connection info in cluster: '%s' mapped with agent: '%s'. Error: %v", cluster.Name, agentName, err)
			continue
		}
	}
}

// SetClusterCacheStats updates cluster cache info with Application, Resource and API counts in principal.
// This is called when principal receives clusterCacheInfoUpdate event from agent.
func (m *Manager) SetClusterCacheStats(clusterInfo *event.ClusterCacheInfo, agentName string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Check if we have a mapping for the requested agent.
	cluster := m.mapping(agentName)
	if cluster == nil {
		log().Errorf("agent %s is not mapped to any cluster", agentName)
		return fmt.Errorf("agent %s is not mapped to any cluster", agentName)
	}

	existingClusterInfo := &appv1.ClusterInfo{}
	if m.clusterCache != nil {
		if err := m.clusterCache.GetClusterInfo(cluster.Server, existingClusterInfo); err != nil {
			if !errors.Is(err, cacheutil.ErrCacheMiss) {
				log().Errorf("failed to get existing cluster info for cluster: '%s' mapped with agent: '%s'. Error: %v", cluster.Name, agentName, err)
			}
		}
	}

	// Create new cluster info with cache stats
	newClusterInfo := &appv1.ClusterInfo{
		ApplicationsCount: clusterInfo.ApplicationsCount,
		CacheInfo: appv1.ClusterCacheInfo{
			APIsCount:      clusterInfo.APIsCount,
			ResourcesCount: clusterInfo.ResourcesCount,
		},
	}

	// Preserve existing cluster connection status
	if existingClusterInfo.ConnectionState != (appv1.ConnectionState{}) {
		newClusterInfo.ConnectionState = existingClusterInfo.ConnectionState
		if existingClusterInfo.CacheInfo.LastCacheSyncTime != nil {
			newClusterInfo.CacheInfo.LastCacheSyncTime = existingClusterInfo.CacheInfo.LastCacheSyncTime
		}
	}

	// Set the info in mapped cluster at principal.
	if err := m.setClusterInfo(cluster.Server, agentName, cluster.Name, newClusterInfo); err != nil {
		log().Errorf("failed to update cluster cache stats in cluster: '%s' mapped with agent: '%s'. Error: %v", cluster.Name, agentName, err)
		return err
	}

	log().WithFields(logrus.Fields{
		"applicationsCount": clusterInfo.ApplicationsCount,
		"apisCount":         clusterInfo.APIsCount,
		"resourcesCount":    clusterInfo.ResourcesCount,
		"cluster":           cluster.Name,
		"agent":             agentName,
	}).Infof("Updated cluster cache stats in cluster.")

	return nil
}

// setClusterInfo saves the given ClusterInfo in the cache.
func (m *Manager) setClusterInfo(clusterServer, agentName, clusterName string, clusterInfo *appv1.ClusterInfo) error {
	// Check if cluster cache is available
	if m.clusterCache == nil {
		return fmt.Errorf("cluster cache is not available")
	}

	// Save the given cluster info in cache.
	if err := m.clusterCache.SetClusterInfo(clusterServer, clusterInfo); err != nil {
		return fmt.Errorf("failed to refresh connection info in cluster: '%s' mapped with agent: '%s': %v", clusterName, agentName, err)
	}
	return nil
}

// NewClusterCacheInstance creates a new cache instance with Redis connection
func NewClusterCacheInstance(redisAddress, redisPassword string, redisCompressionType cacheutil.RedisCompressionType) (*appstatecache.Cache, error) {

	redisOptions := &redis.Options{Addr: redisAddress, Password: redisPassword}
	redisClient := redis.NewClient(redisOptions)

	clusterCache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(redisClient, 0, redisCompressionType)), 0)

	return clusterCache, nil
}
