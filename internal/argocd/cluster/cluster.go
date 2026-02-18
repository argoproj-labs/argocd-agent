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

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/issuer"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	"github.com/sirupsen/logrus"

	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
	"github.com/argoproj/argo-cd/v3/util/db"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const LabelKeySelfRegisteredCluster = "argocd-agent.argoproj-labs.io/self-registered-cluster"

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

	redisOptions := &redis.Options{
		Addr:     redisAddress,
		Password: redisPassword,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode: maintnotifications.ModeDisabled,
		},
	}
	redisClient := redis.NewClient(redisOptions)

	clusterCache := appstatecache.NewCache(cacheutil.NewCache(
		cacheutil.NewRedisCache(redisClient, 0, redisCompressionType)), 0)

	return clusterCache, nil
}

// CreateClusterWithBearerToken creates a cluster secret (if it doesn't already exist) for an agent using both mTLS and JWT bearer token authentication.
// - The shared client certificate (mTLS) proves the request comes from a trusted Argo CD server
// - The JWT bearer token identifies which specific agent is being accessed
func CreateClusterWithBearerToken(ctx context.Context, kubeclient kubernetes.Interface,
	namespace, agentName, resourceProxyAddress string, tokenIssuer issuer.Issuer, clientCertSecretName string) error {

	logCtx := log().WithField("agent", agentName).WithField("process", "self-agent-registration")
	logCtx.Info("Creating self-registered cluster secret with shared client cert and bearer token")

	// Generate bearer token for this agent
	bearerToken, err := tokenIssuer.IssueResourceProxyToken(agentName)
	if err != nil {
		return fmt.Errorf("could not issue resource proxy token: %v", err)
	}
	logCtx.Info("Successfully issued resource proxy token")

	// Read shared client certificate for mTLS
	clientCert, clientKey, caData, err := readClientCertFromSecret(ctx, kubeclient, namespace, clientCertSecretName)
	if err != nil {
		return fmt.Errorf("could not read client certificate from secret %s: %v", clientCertSecretName, err)
	}
	logCtx.Info("Successfully read client certificate from secret")

	// Create a new cluster secret for the agent
	cluster := &appv1.Cluster{
		Server: fmt.Sprintf("https://%s?agentName=%s", resourceProxyAddress, agentName),
		Name:   agentName,
		Labels: map[string]string{
			LabelKeyClusterAgentMapping:   agentName,
			LabelKeySelfRegisteredCluster: "true",
		},
		Config: appv1.ClusterConfig{
			BearerToken: bearerToken,
			TLSClientConfig: appv1.TLSClientConfig{
				CertData: []byte(clientCert),
				KeyData:  []byte(clientKey),
				CAData:   []byte(caData),
			},
		},
	}

	// Convert the cluster to a secret
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetClusterSecretName(agentName),
			Namespace: namespace,
		},
	}
	if err := ClusterToSecret(cluster, secret); err != nil {
		return fmt.Errorf("could not convert cluster to secret: %v", err)
	}

	// Create the cluster secret
	if _, err = kubeclient.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logCtx.Info("Cluster secret already exists, skipping creation")
			return nil
		}
		return fmt.Errorf("could not create cluster secret: %v", err)
	}

	logCtx.Info("Successfully created self-registered cluster secret with shared client cert and bearer token")
	return nil
}

// IsClusterSelfRegistered checks if a cluster secret was created by self-registration.
func IsClusterSelfRegistered(secret *v1.Secret) bool {
	if secret == nil || secret.Labels == nil {
		return false
	}
	return secret.Labels[LabelKeySelfRegisteredCluster] == "true"
}

// UpdateClusterBearerTokenFromSecret updates an existing cluster secret with a new bearer token.
// This is used when the signing key rotates and existing tokens become invalid.
func UpdateClusterBearerTokenFromSecret(ctx context.Context, kubeclient kubernetes.Interface,
	namespace, agentName string, secret *v1.Secret, tokenIssuer issuer.Issuer) error {

	logCtx := log().WithField("agent", agentName).WithField("process", "self-agent-registration")
	logCtx.Info("Updating cluster secret bearer token")

	cluster, err := db.SecretToCluster(secret)
	if err != nil {
		return fmt.Errorf("could not parse cluster secret: %v", err)
	}

	// Generate new bearer token
	bearerToken, err := tokenIssuer.IssueResourceProxyToken(agentName)
	if err != nil {
		return fmt.Errorf("could not issue resource proxy token: %v", err)
	}
	logCtx.Info("Successfully issued new resource proxy token")

	cluster.Config.BearerToken = bearerToken

	if err := ClusterToSecret(cluster, secret); err != nil {
		return fmt.Errorf("could not convert cluster to secret: %v", err)
	}

	if _, err = kubeclient.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("could not update cluster secret: %v", err)
	}

	logCtx.Info("Successfully updated cluster secret bearer token")
	return nil
}

// readClientCertFromSecret reads TLS credentials from an existing Kubernetes TLS secret.
// The secret should contain tls.crt, tls.key, and ca.crt keys.
func readClientCertFromSecret(ctx context.Context, kubeclient kubernetes.Interface, namespace, secretName string) (clientCert, clientKey, caData string, err error) {
	secret, err := kubeclient.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", "", "", fmt.Errorf("could not read TLS secret %s/%s: %w", namespace, secretName, err)
	}

	certData, ok := secret.Data["tls.crt"]
	if !ok {
		return "", "", "", fmt.Errorf("secret %s/%s missing tls.crt", namespace, secretName)
	}

	keyData, ok := secret.Data["tls.key"]
	if !ok {
		return "", "", "", fmt.Errorf("secret %s/%s missing tls.key", namespace, secretName)
	}

	caBytes, ok := secret.Data["ca.crt"]
	if !ok {
		return "", "", "", fmt.Errorf("secret %s/%s missing ca.crt", namespace, secretName)
	}

	return string(certData), string(keyData), string(caBytes), nil
}

// GetBearerTokenFromSecret extracts the bearer token from given cluster secret.
func GetBearerTokenFromSecret(secret *v1.Secret) (string, error) {
	if secret == nil {
		return "", fmt.Errorf("secret is nil")
	}

	// Convert the secret to a cluster
	cluster, err := db.SecretToCluster(secret)
	if err != nil {
		return "", fmt.Errorf("could not parse cluster secret: %w", err)
	}

	return cluster.Config.BearerToken, nil
}

// GetClusterSecret retrieves the cluster secret for the given agent.
func GetClusterSecret(ctx context.Context, kubeclient kubernetes.Interface, namespace, agentName string) (*v1.Secret, error) {
	secret, err := kubeclient.CoreV1().Secrets(namespace).Get(ctx, GetClusterSecretName(agentName), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return secret, nil
}

func GetClusterSecretName(agentName string) string {
	return "cluster-" + agentName
}
