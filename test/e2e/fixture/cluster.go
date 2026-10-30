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
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
)

// extractServerName extracts the hostname or IP from a Redis address for TLS ServerName validation
func extractServerName(addr string) string {
	// Try to split host:port
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// If no port, use the address as-is (e.g., "argocd-redis")
		return addr
	}
	return host
}

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

	// Principal Redis configuration
	PrincipalRedisAddr       string
	PrincipalRedisPassword   string
	PrincipalRedisTLSEnabled bool

	// Kubernetes clients (for loading TLS certificates from secrets)
	ManagedAgentClient KubeClient
	PrincipalClient    KubeClient

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
	err := getCacheInstance(AgentManagedName, clusterDetails).GetClusterInfo(AgentClusterServerURL, &clusterInfo)
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

	err := getCacheInstance(PrincipalName, clusterDetails).GetClusterInfo(server, &clusterInfo)
	if err != nil {
		// Treat missing cache key error (means no apps exist yet) as zero-value info
		if err == cacheutil.ErrCacheMiss {
			return appv1.ClusterInfo{}, nil
		}
		return clusterInfo, err
	}
	return clusterInfo, err
}

// getCacheInstance creates a new cache instance for the given source
func getCacheInstance(source string, clusterDetails *ClusterDetails) *appstatecache.Cache {
	ctx := context.Background()
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
				ServerName: extractServerName(clusterDetails.PrincipalRedisAddr),
			}

			// Load CA certificate directly from Kubernetes secret
			certPool, err := tlsutil.X509CertPoolFromSecret(ctx, clusterDetails.PrincipalClient.Clientset,
				PrincipalNamespace, "argocd-redis-tls", "ca.crt")
			if err != nil {
				panic(fmt.Sprintf("Failed to load Principal Redis CA certificate from secret argocd-redis-tls: %v. "+
					"Run 'make setup-e2e' to generate certificates.", err))
			}

			tlsConfig.RootCAs = certPool
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
				ServerName: extractServerName(clusterDetails.ManagedAgentRedisAddr),
			}

			// Load CA certificate directly from Kubernetes secret
			certPool, err := tlsutil.X509CertPoolFromSecret(ctx, clusterDetails.ManagedAgentClient.Clientset,
				ManagedAgentNamespace, "argocd-redis-tls", "ca.crt")
			if err != nil {
				panic(fmt.Sprintf("Failed to load Managed Agent Redis CA certificate from secret argocd-redis-tls: %v. "+
					"Run 'make setup-e2e' to generate certificates.", err))
			}

			tlsConfig.RootCAs = certPool
			redisOptions.TLSConfig = tlsConfig
		}
	default:
		panic(fmt.Sprintf("invalid source: %s", source))
	}

	redisClient := redis.NewClient(redisOptions)
	cache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(redisClient, 0, cacheutil.RedisCompressionGZip)), 0)

	return cache
}

// getClusterConfigurations gets the cluster configurations from the managed and principal clusters
func getClusterConfigurations(ctx context.Context, managedAgentClient KubeClient, principalClient KubeClient, clusterDetails *ClusterDetails) error {
	// Store client references for TLS certificate loading
	clusterDetails.ManagedAgentClient = managedAgentClient
	clusterDetails.PrincipalClient = principalClient

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
	serviceKey := types.NamespacedName{Name: "argocd-redis", Namespace: ManagedAgentNamespace}
	err := managedAgentClient.Get(ctx, serviceKey, service, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Redis service: %w", err)
	}

	// Get Redis address from LoadBalancer ingress
	var redisAddr string
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		ingress := service.Status.LoadBalancer.Ingress[0]
		if ingress.IP != "" {
			redisAddr = fmt.Sprintf("%s:6379", ingress.IP)
		} else if ingress.Hostname != "" {
			redisAddr = fmt.Sprintf("%s:6379", ingress.Hostname)
		}
	}

	// Fall back to an OpenShift Route (if available). 'hack/dev-env/reverse-tunnels/openshift-routes/setup.sh' exposes argocd-redis this way.
	// However: That Route lives on the *host* cluster, in the
	// 'vcluster-agent-managed' namespace (not the vcluster's own 'argocd-managed' namespace),
	// since the route.openshift.io API isn't available from inside the vcluster.
	var redisTLSEnabled bool
	if redisAddr == "" {
		routeHost, err := getRouteHost(ctx, "vcluster-agent-managed", "argocd-redis")
		if err != nil {
			return err
		}
		if routeHost != "" {
			redisAddr = fmt.Sprintf("%s:443", routeHost)
			redisTLSEnabled = true
		}
	}

	if redisAddr == "" {
		return fmt.Errorf("could not get Redis server address from LoadBalancer ingress or Route")
	}

	clusterDetails.ManagedAgentRedisTLSEnabled = redisTLSEnabled
	clusterDetails.ManagedAgentRedisAddr = redisAddr

	// Fetch Redis secret to get the password
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Name: "argocd-redis", Namespace: ManagedAgentNamespace}
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

// hostK8sContext returns the kubeconfig context name to use for *host* cluster lookups (see
// getRouteHost), honoring the HOST_K8S_CONTEXT env var used by the hack/dev-env scripts
// (hack/dev-env/start-e2e.sh, hack/dev-env/reverse-tunnels/openshift-routes/setup.sh) - those
// scripts set it to a "--context=<name>" kubectl flag rather than a bare context name, hence the
// trim. Returns "" (GetSystemKubeConfig's "use the kubeconfig's current-context" default) when
// the env var is unset, preserving prior behavior.
func hostK8sContext() string {
	return strings.TrimPrefix(os.Getenv("HOST_K8S_CONTEXT"), "--context=")
}

// getRouteHost looks up the hostname of the named OpenShift Route in the given namespace on the
// *host* cluster (not one of the vcluster-* contexts: those don't expose the route.openshift.io
// API, since it's an OpenShift extension API not synced into the vcluster).
//
// Returns an empty string (with no error) if the Route API isn't available on the host cluster or no such Route exists, so
// callers can fall back to other address discovery mechanisms (e.g. a LoadBalancer Service).
func getRouteHost(ctx context.Context, hostNamespace string, routeName string) (string, error) {
	hostConfig, err := GetSystemKubeConfig(hostK8sContext())
	if err != nil {
		return "", fmt.Errorf("failed to load host cluster kubeconfig: %w", err)
	}

	hostClient, err := NewKubeClient(hostConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create host cluster client: %w", err)
	}

	if hostClient.dclient == nil {
		return "", nil
	}

	gvr := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
	route, err := hostClient.dclient.Resource(gvr).Namespace(hostNamespace).Get(ctx, routeName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get %s Route: %w", routeName, err)
	}

	host, _, err := unstructured.NestedString(route.Object, "spec", "host")
	if err != nil {
		return "", fmt.Errorf("failed to read %s Route host: %w", routeName, err)
	}
	return host, nil
}

// getPrincipalRedisConfig gets the principal Redis config from the principal cluster
func getPrincipalRedisConfig(ctx context.Context, principalClient KubeClient, clusterDetails *ClusterDetails) error {
	// Fetch Redis service to get the address
	service := &corev1.Service{}
	serviceKey := types.NamespacedName{Name: "argocd-redis", Namespace: PrincipalNamespace}
	err := principalClient.Get(ctx, serviceKey, service, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Principal Redis service: %w", err)
	}

	// Get Redis address from LoadBalancer ingress
	var redisAddr string
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		ingress := service.Status.LoadBalancer.Ingress[0]
		if ingress.IP != "" {
			redisAddr = fmt.Sprintf("%s:6379", ingress.IP)
		} else if ingress.Hostname != "" {
			redisAddr = fmt.Sprintf("%s:6379", ingress.Hostname)
		}
	}

	// Optional: fall back to an OpenShift Route (if available).
	// 'hack/dev-env/reverse-tunnels/openshift-routes/setup.sh' exposes argocd-redis
	// this way.
	// However: That Route lives on the *host* cluster, in the
	// 'vcluster-control-plane' namespace (not the vcluster's own 'argocd-principal' namespace),
	// since the route.openshift.io API isn't available from inside the vcluster.
	var redisTLSEnabled bool
	if redisAddr == "" {
		routeHost, err := getRouteHost(ctx, "vcluster-control-plane", "argocd-redis")
		if err != nil {
			return err
		}
		if routeHost != "" {
			redisAddr = fmt.Sprintf("%s:443", routeHost)
			redisTLSEnabled = true
		}
	}

	if redisAddr == "" {
		return fmt.Errorf("redis service does not have a LoadBalancer IP or hostname")
	}

	clusterDetails.PrincipalRedisTLSEnabled = redisTLSEnabled
	clusterDetails.PrincipalRedisAddr = redisAddr

	// Fetch Redis secret to get the password
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Name: "argocd-redis", Namespace: PrincipalNamespace}
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
	managedSecretKey := types.NamespacedName{Name: "cluster-agent-managed", Namespace: PrincipalNamespace}
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
	autonomousSecretKey := types.NamespacedName{Name: "cluster-agent-autonomous", Namespace: PrincipalNamespace}
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
