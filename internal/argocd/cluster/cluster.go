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
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/argoproj/argo-cd/v3/common"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// SetAgentConnectionStatus updates cluster info with connection state and time in mapped cluster at principal.
// This is called when the agent is connected or disconnected with the principal.
func (m *Manager) SetAgentConnectionStatus(agentName, status string, t time.Time) {
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
	if err := setClusterInfo(m.ctx, m.kubeclient, m.namespace, cluster.Server, m.redisAddress, agentName, cluster.Name, m.redisCompressionType,
		&appv1.ClusterInfo{
			ConnectionState: appv1.ConnectionState{
				Status:     status,
				Message:    fmt.Sprintf("Agent: '%s' is %s with principal", agentName, state),
				ModifiedAt: &metav1.Time{Time: t},
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

	// Iterate through all clusters.
	for agentName, cluster := range m.clusters {
		clusterInfo, err := GetClusterInfo(m.ctx, m.kubeclient, m.namespace, cluster.Server, m.redisAddress, m.redisCompressionType)
		if err != nil {
			if !errors.Is(err, cacheutil.ErrCacheMiss) {
				log().Errorf("failed to get connection info from cluster: '%s' mapped with agent: '%s'. Error: %v", cluster.Name, agentName, err)
			}
			continue
		}

		// Re-save same info.
		if err := setClusterInfo(m.ctx, m.kubeclient, m.namespace, cluster.Server, m.redisAddress,
			agentName, cluster.Name, m.redisCompressionType, clusterInfo); err != nil {
			log().Errorf("failed to refresh connection info in cluster: '%s' mapped with agent: '%s'. Error: %v", cluster.Name, agentName, err)
			continue
		}
	}
}

// SetClusterCacheStats updates cluster cache info with Application, Resource and API counts in principal.
// This is called when principal receives clusterCacheInfoUpdate event from agent.
func (m *Manager) SetClusterCacheStats(clusterInfo appv1.ClusterInfo, agentName string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Check if we have a mapping for the requested agent.
	cluster := m.mapping(agentName)
	if cluster == nil {
		log().Errorf("agent %s is not mapped to any cluster", agentName)
		return
	}

	// Get existing cluster info to preserve cluster connection status
	existingClusterInfo, err := GetClusterInfo(m.ctx, m.kubeclient, m.namespace, cluster.Server, m.redisAddress, m.redisCompressionType)
	if err != nil && !errors.Is(err, cacheutil.ErrCacheMiss) {
		log().Errorf("failed to get existing cluster info for cluster: '%s' mapped with agent: '%s'. Error: %v", cluster.Name, agentName, err)
		// Continue with default values if we can't get existing info
	}

	// Create new cluster info with cache stats
	newClusterInfo := &appv1.ClusterInfo{
		ApplicationsCount: clusterInfo.ApplicationsCount,
		CacheInfo: appv1.ClusterCacheInfo{
			APIsCount:      clusterInfo.CacheInfo.APIsCount,
			ResourcesCount: clusterInfo.CacheInfo.ResourcesCount,
		},
	}

	// Preserve existing cluster connection status
	if existingClusterInfo != nil {
		newClusterInfo.ConnectionState = existingClusterInfo.ConnectionState
		if existingClusterInfo.CacheInfo.LastCacheSyncTime != nil {
			newClusterInfo.CacheInfo.LastCacheSyncTime = existingClusterInfo.CacheInfo.LastCacheSyncTime
		}
	}

	// Set the info in mapped cluster at principal.
	if err := setClusterInfo(m.ctx, m.kubeclient, m.namespace, cluster.Server, m.redisAddress, agentName, cluster.Name, m.redisCompressionType, newClusterInfo); err != nil {
		log().Errorf("failed to update cluster cache stats in cluster: '%s' mapped with agent: '%s'. Error: %v", cluster.Name, agentName, err)
		return
	}

	log().WithFields(logrus.Fields{
		"applicationsCount": clusterInfo.ApplicationsCount,
		"apisCount":         clusterInfo.CacheInfo.APIsCount,
		"resourcesCount":    clusterInfo.CacheInfo.ResourcesCount,
		"cluster":           cluster.Name,
		"agent":             agentName,
	}).Infof("Updated cluster cache stats in cluster.")
}

// setClusterInfo saves the given ClusterInfo in the cache.
func setClusterInfo(ctx context.Context, kubeclient kubernetes.Interface, namespace, clusterServer, redisAddress, agentName, clusterName string,
	redisCompressionType cacheutil.RedisCompressionType, clusterInfo *appv1.ClusterInfo) error {

	// Get cluster cache instance from redis.
	clusterCache, err := getCacheInstance(ctx, kubeclient, namespace, redisAddress, redisCompressionType)
	if err != nil {
		return fmt.Errorf("failed to get cluster cache instance: %v", err)
	}

	// Save the given cluster info in cache.
	if err := clusterCache.SetClusterInfo(clusterServer, clusterInfo); err != nil {
		return fmt.Errorf("failed to refresh connection info in cluster: '%s' mapped with agent: '%s': %v", clusterName, agentName, err)
	}
	return nil
}

// GetClusterInfo retrieves the ClusterInfo for a given cluster from the cache.
func GetClusterInfo(ctx context.Context, kubeclient kubernetes.Interface, namespace,
	clusterServer, redisAddress string, redisCompressionType cacheutil.RedisCompressionType) (*appv1.ClusterInfo, error) {

	// Get cluster cache instance from redis.
	clusterCache, err := getCacheInstance(ctx, kubeclient, namespace, redisAddress, redisCompressionType)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster cache instance: %v", err)
	}

	clusterInfo := &appv1.ClusterInfo{}
	// Fetch the cluster info from cache.
	if err := clusterCache.GetClusterInfo(clusterServer, clusterInfo); err != nil {
		return nil, err
	}

	return clusterInfo, nil
}

// getCacheInstance creates a new cluster cache instance from redis.
func getCacheInstance(ctx context.Context, kubeclient kubernetes.Interface,
	namespace, redisAddress string, redisCompressionType cacheutil.RedisCompressionType) (*appstatecache.Cache, error) {

	redisOptions := &redis.Options{Addr: redisAddress}

	if err := common.SetOptionalRedisPasswordFromKubeConfig(ctx, kubeclient, namespace, redisOptions); err != nil {
		return nil, fmt.Errorf("failed to set redis password for namespace %s: %v", namespace, err)
	}

	clusterCache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(
			redis.NewClient(redisOptions), time.Minute, redisCompressionType),
	), time.Minute)

	return clusterCache, nil
}
