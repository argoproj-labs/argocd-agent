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
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"

	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	PrincipalName         = "principal"
	AgentManagedName      = "agent-managed"
	AgentAutonomousName   = "agent-autonomous"
	AgentClusterServerURL = "https://kubernetes.default.svc"
)

type ClusterDetails struct {
	// Managed agent Redis configuration
	ManagedAgentRedisAddr       string
	ManagedAgentRedisPassword   string
	ManagedAgentRedisTLSEnabled bool
	ManagedAgentRedisTLSCAPath  string

	// Principal Redis configuration
	PrincipalRedisAddr       string
	PrincipalRedisPassword   string
	PrincipalRedisTLSEnabled bool
	PrincipalRedisTLSCAPath  string

	// Cluster server addresses
	ManagedClusterAddr    string
	AutonomousClusterAddr string
}

// HasConnectionStatus checks if the connection info for a given server matches
// the expected connection state in principal cluster.
func HasConnectionStatus(agentName string, expected appv1.ConnectionState, clusterDetails *ClusterDetails) bool {
	actual, err := GetPrincipalClusterInfo(agentName, clusterDetails)

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
func HasClusterCacheInfoSynced(agentName string, clusterDetails *ClusterDetails) bool {
	principalClusterInfo, err := GetPrincipalClusterInfo(agentName, clusterDetails)
	if err != nil {
		fmt.Println("HasClusterCacheInfoSynced: error", err)
		return false
	}

	agentClusterInfo, err := GetManagedAgentClusterInfo(clusterDetails)
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

func HasApplicationsCount(expected int64, clusterDetails *ClusterDetails) bool {
	agentCacheInfo, err := GetManagedAgentClusterInfo(clusterDetails)
	if err != nil {
		fmt.Println("HasApplicationsCount: error", err)
		return false
	}
	fmt.Printf("HasApplicationsCount expected: %d, actual: %d\n", expected, agentCacheInfo.ApplicationsCount)
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
func GetManagedAgentClusterInfo(clusterDetails *ClusterDetails) (appv1.ClusterInfo, error) {

	// Fetch cluster info from redis cache
	clusterInfo := appv1.ClusterInfo{}
	err := getCachedCacheInstance(AgentManagedName, clusterDetails).GetClusterInfo(AgentClusterServerURL, &clusterInfo)
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
func GetPrincipalClusterInfo(agentName string, clusterDetails *ClusterDetails) (appv1.ClusterInfo, error) {
	var clusterInfo appv1.ClusterInfo

	server := ""
	switch agentName {
	case AgentManagedName:
		server = clusterDetails.ManagedClusterAddr
	case AgentAutonomousName:
		server = clusterDetails.AutonomousClusterAddr
	default:
		return appv1.ClusterInfo{}, fmt.Errorf("invalid agent name: %s", agentName)
	}

	fmt.Printf("GetPrincipalClusterInfo: Looking up cluster info for agent=%s, server=%s, redis=%s\n",
		agentName, server, clusterDetails.PrincipalRedisAddr)

	err := getCachedCacheInstance(PrincipalName, clusterDetails).GetClusterInfo(server, &clusterInfo)
	if err != nil {
		// Treat missing cache key error (means no apps exist yet) as zero-value info
		if err == cacheutil.ErrCacheMiss {
			fmt.Printf("GetPrincipalClusterInfo: Cache miss for server=%s\n", server)
			return appv1.ClusterInfo{}, nil
		}
		fmt.Printf("GetPrincipalClusterInfo: Error getting cluster info: %v\n", err)
		return clusterInfo, err
	}
	return clusterInfo, err
}

// getCacheInstance creates a new cache instance for the given source
func getCacheInstance(source string, clusterDetails *ClusterDetails) *appstatecache.Cache {
	redisOptions := &redis.Options{}
	switch source {
	case PrincipalName:
		redisOptions.Addr = clusterDetails.PrincipalRedisAddr
		redisOptions.Password = clusterDetails.PrincipalRedisPassword
		redisOptions.MaintNotificationsConfig = &maintnotifications.Config{
			Mode: maintnotifications.ModeDisabled,
		}

		// Enable TLS if configured
		if clusterDetails.PrincipalRedisTLSEnabled {
			tlsConfig := &tls.Config{
				MinVersion: tls.VersionTLS12,
			}

			// Load CA certificate for proper verification if available
			if clusterDetails.PrincipalRedisTLSCAPath != "" {
				// Check if CA cert file exists
				if _, err := os.Stat(clusterDetails.PrincipalRedisTLSCAPath); err == nil {
					caCertPEM, err := os.ReadFile(clusterDetails.PrincipalRedisTLSCAPath)
					if err != nil {
						panic(fmt.Sprintf("failed to read principal Redis CA certificate: %v", err))
					}

					certPool := x509.NewCertPool()
					if !certPool.AppendCertsFromPEM(caCertPEM) {
						panic(fmt.Sprintf("failed to parse principal Redis CA certificate from %s", clusterDetails.PrincipalRedisTLSCAPath))
					}

					tlsConfig.RootCAs = certPool
				} else {
					// CA cert not found, skip verification (for backwards compatibility with existing tests)
					fmt.Printf("Warning: Principal Redis CA certificate not found at %s, skipping verification\n", clusterDetails.PrincipalRedisTLSCAPath)
					tlsConfig.InsecureSkipVerify = true
				}
			} else {
				// No CA path specified, skip verification
				tlsConfig.InsecureSkipVerify = true
			}

			redisOptions.TLSConfig = tlsConfig
		}
	case AgentManagedName:
		redisOptions.Addr = clusterDetails.ManagedAgentRedisAddr
		redisOptions.Password = clusterDetails.ManagedAgentRedisPassword
		redisOptions.MaintNotificationsConfig = &maintnotifications.Config{
			Mode: maintnotifications.ModeDisabled,
		}

		// Enable TLS if configured
		if clusterDetails.ManagedAgentRedisTLSEnabled {
			tlsConfig := &tls.Config{
				MinVersion: tls.VersionTLS12,
			}

			// Load CA certificate for proper verification if available
			if clusterDetails.ManagedAgentRedisTLSCAPath != "" {
				// Check if CA cert file exists
				if _, err := os.Stat(clusterDetails.ManagedAgentRedisTLSCAPath); err == nil {
					caCertPEM, err := os.ReadFile(clusterDetails.ManagedAgentRedisTLSCAPath)
					if err != nil {
						panic(fmt.Sprintf("failed to read managed agent Redis CA certificate: %v", err))
					}

					certPool := x509.NewCertPool()
					if !certPool.AppendCertsFromPEM(caCertPEM) {
						panic(fmt.Sprintf("failed to parse managed agent Redis CA certificate from %s", clusterDetails.ManagedAgentRedisTLSCAPath))
					}

					tlsConfig.RootCAs = certPool
				} else {
					// CA cert not found, skip verification (for backwards compatibility with existing tests)
					fmt.Printf("Warning: Managed agent Redis CA certificate not found at %s, skipping verification\n", clusterDetails.ManagedAgentRedisTLSCAPath)
					tlsConfig.InsecureSkipVerify = true
				}
			} else {
				// No CA path specified, skip verification
				tlsConfig.InsecureSkipVerify = true
			}

			redisOptions.TLSConfig = tlsConfig
		}
	default:
		panic(fmt.Sprintf("invalid source: %s", source))
	}

	// Set generous timeouts for E2E tests to handle port-forward latency
	redisOptions.DialTimeout = 10 * time.Second
	redisOptions.ReadTimeout = 30 * time.Second // Increased for slow port-forward operations
	redisOptions.WriteTimeout = 10 * time.Second
	redisOptions.MinRetryBackoff = 100 * time.Millisecond
	redisOptions.MaxRetryBackoff = 1 * time.Second
	redisOptions.ConnMaxIdleTime = 5 * time.Minute // Faster connection cleanup in test environments

	redisClient := redis.NewClient(redisOptions)
	cache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(redisClient, 0, cacheutil.RedisCompressionGZip)), 0)

	return cache
}

// cachedRedisClients stores Redis clients to prevent connection leaks
var (
	cachedRedisClients     = make(map[string]*appstatecache.Cache)
	cachedRedisClientMutex sync.Mutex
)

// getCachedCacheInstance returns a cached Redis client or creates a new one
func getCachedCacheInstance(source string, clusterDetails *ClusterDetails) *appstatecache.Cache {
	cachedRedisClientMutex.Lock()
	defer cachedRedisClientMutex.Unlock()

	// Create cache key based on source and address
	var cacheKey string
	switch source {
	case PrincipalName:
		cacheKey = fmt.Sprintf("%s:%s", source, clusterDetails.PrincipalRedisAddr)
	case AgentManagedName:
		cacheKey = fmt.Sprintf("%s:%s", source, clusterDetails.ManagedAgentRedisAddr)
	default:
		panic(fmt.Sprintf("invalid source for cached client: %s", source))
	}

	// Return cached client if it exists
	if client, ok := cachedRedisClients[cacheKey]; ok {
		return client
	}

	// Create new client and cache it
	client := getCacheInstance(source, clusterDetails)
	cachedRedisClients[cacheKey] = client
	return client
}

// CleanupRedisCachedClients closes all cached Redis clients (should be called at end of test suite)
func CleanupRedisCachedClients() {
	cachedRedisClientMutex.Lock()
	defer cachedRedisClientMutex.Unlock()

	fmt.Printf("Cleaning up %d cached Redis clients\n", len(cachedRedisClients))
	// Clear the cache map - connections will be garbage collected
	cachedRedisClients = make(map[string]*appstatecache.Cache)
}

// getClusterConfigurations gets the cluster configurations from the managed and principal clusters
func getClusterConfigurations(ctx context.Context, managedAgentClient KubeClient, principalClient KubeClient, clusterDetails *ClusterDetails) error {
	// Get managed agent Redis config
	if err := getManagedAgentRedisConfig(ctx, managedAgentClient, clusterDetails); err != nil {
		return err
	}

	// Get principal Redis config
	if err := getPrincipalRedisConfig(ctx, principalClient, clusterDetails); err != nil {
		return err
	}

	// Get cluster server addresses
	if err := getClusterServerAddresses(ctx, principalClient, clusterDetails); err != nil {
		return err
	}
	return nil
}

// getManagedAgentRedisConfig gets the managed agent Redis config from the managed cluster
func getManagedAgentRedisConfig(ctx context.Context, managedAgentClient KubeClient, clusterDetails *ClusterDetails) error {
	// Fetch Redis service to get the address
	service := &corev1.Service{}
	serviceKey := types.NamespacedName{Name: "argocd-redis", Namespace: "argocd"}
	err := managedAgentClient.Get(ctx, serviceKey, service, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Redis service: %w", err)
	}

	// Get Redis address from LoadBalancer ingress or spec
	var redisAddr string
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		ingress := service.Status.LoadBalancer.Ingress[0]
		if ingress.IP != "" {
			redisAddr = fmt.Sprintf("%s:6379", ingress.IP)
		} else if ingress.Hostname != "" {
			redisAddr = fmt.Sprintf("%s:6379", ingress.Hostname)
		}
	}
	// Fall back to spec.loadBalancerIP for local development (vcluster)
	if redisAddr == "" && service.Spec.LoadBalancerIP != "" {
		redisAddr = fmt.Sprintf("%s:6379", service.Spec.LoadBalancerIP)
	}
	// Fall back to ClusterIP as last resort
	if redisAddr == "" && service.Spec.ClusterIP != "" {
		redisAddr = fmt.Sprintf("%s:6379", service.Spec.ClusterIP)
	}
	if redisAddr == "" {
		return fmt.Errorf("could not get Redis server address from LoadBalancer ingress, spec, or ClusterIP")
	}

	// Redis TLS is always enabled for E2E tests
	clusterDetails.ManagedAgentRedisTLSEnabled = true

	// Set CA certificate path (same as used by agents)
	clusterDetails.ManagedAgentRedisTLSCAPath = "hack/dev-env/creds/redis-tls/ca.crt"

	// Allow override via environment variable for local development with port-forward
	if envAddr := os.Getenv("MANAGED_AGENT_REDIS_ADDR"); envAddr != "" {
		redisAddr = envAddr
	}

	clusterDetails.ManagedAgentRedisAddr = redisAddr

	// Fetch Redis secret to get the password
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Name: "argocd-redis", Namespace: "argocd"}
	err = managedAgentClient.Get(ctx, secretKey, secret, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Redis secret: %w", err)
	}

	// Decode the password from base64
	authData, exists := secret.Data["auth"]
	if !exists {
		return fmt.Errorf("auth field is not found in Redis secret")
	}

	clusterDetails.ManagedAgentRedisPassword = string(authData)

	return nil
}

// getPrincipalRedisConfig gets the principal Redis config from the principal cluster
func getPrincipalRedisConfig(ctx context.Context, principalClient KubeClient, clusterDetails *ClusterDetails) error {
	// Fetch Redis service to get the address
	service := &corev1.Service{}
	serviceKey := types.NamespacedName{Name: "argocd-redis", Namespace: "argocd"}
	err := principalClient.Get(ctx, serviceKey, service, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Principal Redis service: %w", err)
	}

	// Get Redis address from LoadBalancer ingress or spec
	var redisAddr string
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		ingress := service.Status.LoadBalancer.Ingress[0]
		if ingress.IP != "" {
			redisAddr = fmt.Sprintf("%s:6379", ingress.IP)
		} else if ingress.Hostname != "" {
			redisAddr = fmt.Sprintf("%s:6379", ingress.Hostname)
		}
	}
	// Fall back to spec.loadBalancerIP for local development (vcluster)
	if redisAddr == "" && service.Spec.LoadBalancerIP != "" {
		redisAddr = fmt.Sprintf("%s:6379", service.Spec.LoadBalancerIP)
	}
	// Fall back to ClusterIP as last resort
	if redisAddr == "" && service.Spec.ClusterIP != "" {
		redisAddr = fmt.Sprintf("%s:6379", service.Spec.ClusterIP)
	}
	if redisAddr == "" {
		return fmt.Errorf("could not get Principal Redis server address from LoadBalancer ingress, spec, or ClusterIP")
	}

	clusterDetails.PrincipalRedisTLSEnabled = true

	// Set CA certificate path (same as used by principal)
	clusterDetails.PrincipalRedisTLSCAPath = "hack/dev-env/creds/redis-tls/ca.crt"

	// Allow override via environment variable for local development with port-forward
	if envAddr := os.Getenv("ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS"); envAddr != "" {
		redisAddr = envAddr
	}

	clusterDetails.PrincipalRedisAddr = redisAddr

	// Fetch Redis secret to get the password
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Name: "argocd-redis", Namespace: "argocd"}
	err = principalClient.Get(ctx, secretKey, secret, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Principal Redis secret: %w", err)
	}

	// Decode the password from base64
	authData, exists := secret.Data["auth"]
	if !exists {
		return fmt.Errorf("auth field is not found in Principal Redis secret")
	}

	clusterDetails.PrincipalRedisPassword = string(authData)

	return nil
}

// getClusterServerAddresses gets the cluster server addresses from the principal cluster
func getClusterServerAddresses(ctx context.Context, principalClient KubeClient, clusterDetails *ClusterDetails) error {
	// Fetch managed cluster server address
	managedSecret := &corev1.Secret{}
	managedSecretKey := types.NamespacedName{Name: "cluster-agent-managed", Namespace: "argocd"}
	err := principalClient.Get(ctx, managedSecretKey, managedSecret, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get managed cluster secret: %w", err)
	}

	serverData, exists := managedSecret.Data["server"]
	if !exists {
		return fmt.Errorf("server field is not found in managed cluster secret")
	}
	clusterDetails.ManagedClusterAddr = string(serverData)

	// Fetch autonomous cluster server address
	autonomousSecret := &corev1.Secret{}
	autonomousSecretKey := types.NamespacedName{Name: "cluster-agent-autonomous", Namespace: "argocd"}
	err = principalClient.Get(ctx, autonomousSecretKey, autonomousSecret, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get autonomous cluster secret: %w", err)
	}

	serverData, exists = autonomousSecret.Data["server"]
	if !exists {
		return fmt.Errorf("server field is not found in autonomous cluster secret")
	}
	clusterDetails.AutonomousClusterAddr = string(serverData)

	return nil
}
