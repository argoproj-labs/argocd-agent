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
	"os"
	"time"

	appv1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v2/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v2/util/cache/appstate"
	"github.com/redis/go-redis/v9"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ManagedAgentServerKey    = "AGENT_E2E_MANAGED_CLUSTER_SERVER"
	AutonomousAgentServerKey = "AGENT_E2E_AUTONOMOUS_CLUSTER_SERVER"
	RedisServerAddressKey    = "AGENT_E2E_REDIS_SERVER_ADDRESS"
	RedisServerPasswordKey   = "AGENT_E2E_REDIS_PASSWORD"
)

func HasConnectionInfo(serverName string, expected appv1.ConnectionState) bool {
	actual, err := getClusterInfo(serverName)

	if err != nil {
		return false
	}

	return actual.ConnectionState.Status == expected.Status &&
		actual.ConnectionState.Message == expected.Message &&
		verifyConnectionModificationTime(actual.ConnectionState.ModifiedAt, expected.ModifiedAt)
}

// getClusterInfo retrieves connection info from control plane's Redis server
func getClusterInfo(server string) (appv1.ClusterInfo, error) {

	// Create Redis client and cache
	redisOptions := &redis.Options{Addr: os.Getenv(RedisServerAddressKey),
		Password: os.Getenv(RedisServerPasswordKey)}

	redisClient := redis.NewClient(redisOptions)
	cache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(redisClient, time.Hour, cacheutil.RedisCompressionGZip)), time.Hour)

	// fetch cluster info
	var clusterInfo appv1.ClusterInfo
	err := cache.GetClusterInfo(server, &clusterInfo)

	return clusterInfo, err
}

func verifyConnectionModificationTime(actualTime *metav1.Time, expectedTime *metav1.Time) bool {
	// initially we don't know when agent was started, hence just checking actual time is not Nil
	if expectedTime == nil && actualTime != nil {
		return true
	}

	// if expected time is provided then verify that actual time is within 5 sec range from expected time,
	// because starting agent may take some time
	return actualTime.After(expectedTime.Add(-5 * time.Second))
}
