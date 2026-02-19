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

package registration

import (
	"context"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/issuer"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type AgentRegistrationManager struct {
	selfAgentRegistrationEnabled bool
	namespace                    string
	resourceProxyAddress         string
	clientCertSecretName         string
	kubeclient                   kubernetes.Interface
	issuer                       issuer.Issuer
}

func NewAgentRegistrationManager(selfAgentRegistrationEnabled bool, namespace, resourceProxyAddress, clientCertSecretName string,
	kubeclient kubernetes.Interface, iss issuer.Issuer) *AgentRegistrationManager {
	return &AgentRegistrationManager{
		selfAgentRegistrationEnabled: selfAgentRegistrationEnabled,
		namespace:                    namespace,
		resourceProxyAddress:         resourceProxyAddress,
		clientCertSecretName:         clientCertSecretName,
		kubeclient:                   kubeclient,
		issuer:                       iss,
	}
}

// RegisterAgent checks if a cluster secret exists for the agent and creates/updates if needed.
// If the secret exists but the token is invalid (e.g., signing key rotated), it will be refreshed.
func (mgr *AgentRegistrationManager) RegisterAgent(ctx context.Context, agentName string) error {
	if !mgr.selfAgentRegistrationEnabled {
		return nil
	}

	logCtx := log().WithField("agent", agentName)

	// Get cluster secret if it exists
	existingSecret, err := cluster.GetClusterSecret(ctx, mgr.kubeclient, mgr.namespace, agentName)
	if err != nil {
		return fmt.Errorf("could not get cluster secret: %v", err)
	}

	if existingSecret != nil {
		logCtx.Debug("Cluster secret for agent exists")

		// Check if this is a self-registered secret or a manually created one
		if !cluster.IsClusterSelfRegistered(existingSecret) {
			// This is a manually created secret, do not modify it
			logCtx.Debug("Manually created cluster secret exists, skipping self-registration")
			return nil
		}

		logCtx.Debug("Existing cluster secret is self-registered")

		// Validate existing token, refresh if invalid
		valid, err := mgr.validateClusterTokenFromSecret(existingSecret, agentName)
		if err != nil {
			logCtx.WithField("reason", err).Debug("Could not validate cluster token, will refresh")
			valid = false
		}

		if valid {
			logCtx.Debug("Cluster secret already exists with valid token, skipping registration")
			return nil
		}

		// Token is invalid, update it
		if err := cluster.UpdateClusterBearerTokenFromSecret(ctx, mgr.kubeclient, mgr.namespace, agentName, existingSecret, mgr.issuer); err != nil {
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

	return nil
}

// validateClusterTokenFromSecret checks if the cluster secret has a valid bearer token.
func (mgr *AgentRegistrationManager) validateClusterTokenFromSecret(secret *corev1.Secret, agentName string) (bool, error) {
	token, err := cluster.GetBearerTokenFromSecret(secret)
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

func (mgr *AgentRegistrationManager) IsSelfAgentRegistrationEnabled() bool {
	return mgr.selfAgentRegistrationEnabled
}

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ModuleLogger("AgentRegistration")
}
