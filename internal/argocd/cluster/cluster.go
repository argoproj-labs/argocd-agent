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
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	"github.com/sirupsen/logrus"

	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
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

// CreateCluster creates a cluster secret for an agent's cluster on the principal.
// If sharedClientCertSecretName is provided, it reads the TLS credentials from that secret
// (shared client cert mode). Otherwise, it generates a new client certificate signed
// by the principal's CA (legacy mode, requires CA private key).
func CreateCluster(ctx context.Context, kubeclient kubernetes.Interface, namespace, agentName, resourceProxyAddress, sharedClientCertSecretName string) error {
	var clientCert, clientKey, caData string
	var err error

	if sharedClientCertSecretName != "" {
		// Shared client cert mode: read from existing secret
		log().WithFields(logrus.Fields{
			"agent":  agentName,
			"secret": sharedClientCertSecretName,
		}).Info("Using shared client certificate mode for cluster registration")

		clientCert, clientKey, caData, err = readSharedClientCertFromSecret(ctx, kubeclient, namespace, sharedClientCertSecretName)
		if err != nil {
			return fmt.Errorf("could not read shared client certificate from secret %s: %v", sharedClientCertSecretName, err)
		}
	} else {
		// Legacy mode: generate client certificate signed by principal's CA
		log().WithField("agent", agentName).Info("Generating unique client certificate for cluster registration")

		clientCert, clientKey, caData, err = generateAgentClientCert(ctx, kubeclient, namespace, agentName, config.SecretNamePrincipalCA)
		if err != nil {
			return fmt.Errorf("could not generate client certificate: %v", err)
		}
	}

	// Note: this structure has to be same as manual creation done by `argocd-agentctl agent create <agent_name>`
	cluster := &appv1.Cluster{
		Server: fmt.Sprintf("https://%s?agentName=%s", resourceProxyAddress, agentName),
		Name:   agentName,
		Labels: map[string]string{
			LabelKeyClusterAgentMapping:   agentName,
			LabelKeySelfRegisteredCluster: "true",
		},
		Config: appv1.ClusterConfig{
			TLSClientConfig: appv1.TLSClientConfig{
				CertData: []byte(clientCert),
				KeyData:  []byte(clientKey),
				CAData:   []byte(caData),
			},
		},
	}

	// Convert cluster object to Kubernetes secret object
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getClusterSecretName(agentName),
			Namespace: namespace,
		},
	}
	if err := ClusterToSecret(cluster, secret); err != nil {
		return fmt.Errorf("could not convert cluster to secret: %v", err)
	}

	// Create the secret to register the agent's cluster.
	if _, err = kubeclient.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("could not create cluster secret to register agent's cluster: %v", err)
	}

	return nil
}

// readSharedClientCertFromSecret reads TLS credentials from an existing Kubernetes TLS secret.
// The secret should contain tls.crt, tls.key, and ca.crt keys.
func readSharedClientCertFromSecret(ctx context.Context, kubeclient kubernetes.Interface, namespace, sharedClientCertSecretName string) (clientCert, clientKey, caData string, err error) {
	secret, err := kubeclient.CoreV1().Secrets(namespace).Get(ctx, sharedClientCertSecretName, metav1.GetOptions{})
	if err != nil {
		return "", "", "", fmt.Errorf("could not read TLS secret %s/%s: %v", namespace, sharedClientCertSecretName, err)
	}

	certData, ok := secret.Data["tls.crt"]
	if !ok {
		return "", "", "", fmt.Errorf("secret %s/%s missing tls.crt", namespace, sharedClientCertSecretName)
	}

	keyData, ok := secret.Data["tls.key"]
	if !ok {
		return "", "", "", fmt.Errorf("secret %s/%s missing tls.key", namespace, sharedClientCertSecretName)
	}

	// CA cert can be in ca.crt (cert-manager style) or tls.crt of a separate CA secret
	caBytes, ok := secret.Data["ca.crt"]
	if !ok {
		return "", "", "", fmt.Errorf("secret %s/%s missing ca.crt", namespace, sharedClientCertSecretName)
	}

	return string(certData), string(keyData), string(caBytes), nil
}

// ClusterSecretExists checks if a cluster secret exists for the given agent.
func ClusterSecretExists(ctx context.Context, kubeclient kubernetes.Interface, namespace, agentName string) (bool, error) {
	if _, err := kubeclient.CoreV1().Secrets(namespace).Get(ctx, getClusterSecretName(agentName), metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func generateAgentClientCert(ctx context.Context, kubeclient kubernetes.Interface, namespace, agentName, caSecretName string) (clientCert, clientKey, caData string, err error) {

	// Read the CA certificate from the principal's CA secret
	tlsCert, err := tlsutil.TLSCertFromSecret(ctx, kubeclient, namespace, caSecretName)
	if err != nil {
		err = fmt.Errorf("could not read CA secret: %v", err)
		return
	}

	// Parse CA certificate from PEM format
	signerCert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		err = fmt.Errorf("could not parse CA certificate: %v", err)
		return
	}

	// Generate a client cert with agent name as CN and sign it with the CA's cert and key
	clientCert, clientKey, err = tlsutil.GenerateClientCertificate(agentName, signerCert, tlsCert.PrivateKey)
	if err != nil {
		err = fmt.Errorf("could not create client cert: %v", err)
		return
	}

	// Convert CA certificate to PEM format
	caData, err = tlsutil.CertDataToPEM(tlsCert.Certificate[0])
	if err != nil {
		err = fmt.Errorf("could not convert CA certificate to PEM format: %v", err)
		return
	}

	return
}

func getClusterSecretName(agentName string) string {
	return "cluster-" + agentName
}
