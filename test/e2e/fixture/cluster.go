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

package fixture

import (
	"fmt"
	"os"
	"time"

	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
	"github.com/redis/go-redis/v9"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ManagedAgentServerKey              = "AGENT_E2E_MANAGED_CLUSTER_SERVER"
	AutonomousAgentServerKey           = "AGENT_E2E_AUTONOMOUS_CLUSTER_SERVER"
	PrincipalRedisServerAddressKey     = "AGENT_E2E_PRINCIPAL_REDIS_SERVER_ADDRESS"
	PrincipalRedisServerPasswordKey    = "AGENT_E2E_PRINCIPAL_REDIS_PASSWORD"
	AgentManagedRedisServerAddressKey  = "AGENT_E2E_AGENT_MANAGED_REDIS_SERVER_ADDRESS"
	AgentManagedRedisServerPasswordKey = "AGENT_E2E_AGENT_MANAGED_REDIS_PASSWORD"
)

// HasConnectionStatus checks if the connection info for a given server matches
// the expected connection state in principal cluster.
func HasConnectionStatus(serverName string, expected appv1.ConnectionState) bool {
	actual, err := GetPrincipalClusterInfo(serverName)

	if err != nil {
		fmt.Println("HasConnectionStatus: error", err)
		return false
	}

	fmt.Printf("HasConnectionStatus expected: (%s, %s), actual: (%s, %s)\n",
		expected.Status, expected.Message, actual.ConnectionState.Status, actual.ConnectionState.Message)

	return actual.ConnectionState.Status == expected.Status &&
		actual.ConnectionState.Message == expected.Message &&
		verifyConnectionModificationTime(actual.ConnectionState.ModifiedAt, expected.ModifiedAt)
}

// HasClusterCacheInfoSynced checks if the cluster cache info is synced between principal and agent
func HasClusterCacheInfoSynced(serverName string) bool {
	principalClusterInfo, err := GetPrincipalClusterInfo(serverName)
	if err != nil {
		fmt.Println("HasConnectionStatus: error", err)
		return false
	}

	agentClusterInfo, err := GetManagedAgentClusterInfo()
	if err != nil {
		fmt.Println("HasClusterCacheInfoSynced: error", err)
		return false
	}

	fmt.Printf("HasClusterCacheInfoSynced principal: (%d, %d, %d), agent: (%d, %d, %d)\n",
		principalClusterInfo.ApplicationsCount, principalClusterInfo.CacheInfo.APIsCount, principalClusterInfo.CacheInfo.ResourcesCount,
		agentClusterInfo.ApplicationsCount, agentClusterInfo.CacheInfo.APIsCount, agentClusterInfo.CacheInfo.ResourcesCount)

	return principalClusterInfo.ApplicationsCount == agentClusterInfo.ApplicationsCount &&
		principalClusterInfo.CacheInfo.APIsCount == agentClusterInfo.CacheInfo.APIsCount &&
		principalClusterInfo.CacheInfo.ResourcesCount == agentClusterInfo.CacheInfo.ResourcesCount
}

func HasApplicationsCount(expected int64) bool {
	agentCacheInfo, err := GetManagedAgentClusterInfo()
	if err != nil {
		fmt.Println("HasApplicationsCount: error", err)
		return false
	}
	return agentCacheInfo.ApplicationsCount == expected
}

func verifyConnectionModificationTime(actualTime *metav1.Time, expectedTime *metav1.Time) bool {
	// initially we don't know when agent was started, hence just checking actual time is not Nil
	if expectedTime == nil && actualTime != nil {
		return true
	}

	// if expected time is provided then verify that actual time is within 5 sec range from expected time,
	// because starting agent may take some time
	result := actualTime.After(expectedTime.Add(-5 * time.Second))
	fmt.Printf("verifyConnectionModificationTime expected: %t, actual: %t\n", true, result)
	return result
}

// GetManagedAgentClusterInfo retrieves cluster info from managed agent's Redis server
func GetManagedAgentClusterInfo() (appv1.ClusterInfo, error) {
	// Create Redis client and cache
	redisOptions := &redis.Options{Addr: os.Getenv(AgentManagedRedisServerAddressKey),
		Password: os.Getenv(AgentManagedRedisServerPasswordKey)}

	redisClient := redis.NewClient(redisOptions)
	cache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(redisClient, time.Minute, cacheutil.RedisCompressionGZip)), time.Minute)

	clusterServer := "https://kubernetes.default.svc"

	// Fetch cluster info from redis cache
	clusterInfo := appv1.ClusterInfo{}
	err := cache.GetClusterInfo(clusterServer, &clusterInfo)
	if err != nil {
		// Treat missing cache key error (means no apps exist yet) as zero-value info
		if err == cacheutil.ErrCacheMiss {
			return appv1.ClusterInfo{}, nil
		}
		fmt.Println("GetManagedAgentClusterInfo: error", err)
		return clusterInfo, err
	}

	return clusterInfo, nil
}

// GetPrincipalClusterInfo retrieves cluster info from principal's Redis server
func GetPrincipalClusterInfo(server string) (appv1.ClusterInfo, error) {
	// Create Redis client and cache
	redisOptions := &redis.Options{Addr: os.Getenv(PrincipalRedisServerAddressKey),
		Password: os.Getenv(PrincipalRedisServerPasswordKey)}

	redisClient := redis.NewClient(redisOptions)
	cache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(redisClient, 0, cacheutil.RedisCompressionGZip)), 0)

	// fetch cluster info
	var clusterInfo appv1.ClusterInfo
	err := cache.GetClusterInfo(server, &clusterInfo)

	return clusterInfo, err
}
