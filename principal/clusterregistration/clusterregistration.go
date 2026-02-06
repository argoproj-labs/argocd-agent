// Copyright 2026 The argocd-agent Authors
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

package clusterregistration

import (
	"context"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/issuer"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
)

type ClusterRegistrationManager struct {
	selfClusterRegistrationEnabled bool
	namespace                      string
	resourceProxyAddress           string
	clientCertSecretName           string
	kubeclient                     kubernetes.Interface
	issuer                         issuer.Issuer
}

func NewClusterRegistrationManager(enabled bool, namespace, resourceProxyAddress, clientCertSecretName string,
	kubeclient kubernetes.Interface, iss issuer.Issuer) *ClusterRegistrationManager {
	return &ClusterRegistrationManager{
		selfClusterRegistrationEnabled: enabled,
		namespace:                      namespace,
		resourceProxyAddress:           resourceProxyAddress,
		clientCertSecretName:           clientCertSecretName,
		kubeclient:                     kubeclient,
		issuer:                         iss,
	}
}

// RegisterCluster checks if a cluster secret exists for the agent and creates/updates if needed.
// If the secret exists but the token is invalid (e.g., signing key rotated), it will be refreshed.
func (mgr *ClusterRegistrationManager) RegisterCluster(ctx context.Context, agentName string) error {
	if !mgr.selfClusterRegistrationEnabled {
		return nil
	}

	logCtx := log().WithField("agent", agentName)

	logCtx.Info("Self registering agent's cluster")

	// Check if cluster secret already exists for the given agent
	exists, err := cluster.ClusterSecretExists(ctx, mgr.kubeclient, mgr.namespace, agentName)
	if err != nil {
		return fmt.Errorf("error while checking if cluster secret exists: %v", err)
	}

	if exists {
		logCtx.Info("Cluster secret for agent exists")

		// Check if this is a self-registered secret or a manually created one
		isSelfRegistered, err := cluster.IsClusterSelfRegistered(ctx, mgr.kubeclient, mgr.namespace, agentName)
		if err != nil {
			return fmt.Errorf("could not check if cluster is self-registered: %w", err)
		}

		if !isSelfRegistered {
			// This is a manually created secret, do not modify it
			logCtx.Info("Manually created cluster secret exists, skipping self-registration")
			return nil
		}

		logCtx.Info("Existing cluster secret is self-registered")

		// Validate existing token, refresh if invalid
		valid, err := mgr.validateClusterToken(ctx, agentName)
		if err != nil {
			logCtx.WithError(err).Info("Could not validate cluster token, will refresh")
			valid = false
		}

		if valid {
			logCtx.Info("Cluster secret already exists with valid token, skipping registration")
			return nil
		}

		// Token is invalid, update it
		logCtx.Info("Cluster token invalid or expired, refreshing")
		if err := cluster.UpdateClusterBearerToken(ctx, mgr.kubeclient, mgr.namespace, agentName, mgr.issuer); err != nil {
			return fmt.Errorf("failed to refresh cluster bearer token: %v", err)
		}

		logCtx.Info("Cluster bearer token refreshed successfully")
		return nil
	}

	// Create new cluster secret
	logCtx.Info("Creating self-registered cluster secret for agent")

	if err := cluster.CreateClusterWithBearerToken(ctx, mgr.kubeclient, mgr.namespace, agentName, mgr.resourceProxyAddress, mgr.issuer, mgr.clientCertSecretName); err != nil {
		return fmt.Errorf("failed to create self-registered cluster secret: %w", err)
	}

	logCtx.Info("Agent cluster self-registration completed successfully")

	return nil
}

// validateClusterToken checks if the existing cluster secret has a valid bearer token.
func (mgr *ClusterRegistrationManager) validateClusterToken(ctx context.Context, agentName string) (bool, error) {
	token, err := cluster.GetClusterBearerToken(ctx, mgr.kubeclient, mgr.namespace, agentName)
	if err != nil {
		return false, err
	}

	if token == "" {
		return false, nil
	}

	// Validate the token using the issuer
	claims, err := mgr.issuer.ValidateResourceProxyToken(token)
	if err != nil {
		return false, err
	}

	// Verify the token is for this agent
	subject, err := claims.GetSubject()
	if err != nil {
		return false, err
	}

	return subject == agentName, nil
}

func (mgr *ClusterRegistrationManager) IsSelfClusterRegistrationEnabled() bool {
	return mgr.selfClusterRegistrationEnabled
}

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ModuleLogger("ClusterRegistration")
}
