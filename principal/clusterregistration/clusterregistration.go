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
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
)

type ClusterRegistrationManager struct {
	selfClusterRegistrationEnabled bool
	namespace                      string
	resourceProxyAddress           string
	kubeclient                     kubernetes.Interface
}

func NewClusterRegistrationManager(enabled bool, namespace, resourceProxyAddress string,
	kubeclient kubernetes.Interface) *ClusterRegistrationManager {
	return &ClusterRegistrationManager{
		selfClusterRegistrationEnabled: enabled,
		namespace:                      namespace,
		resourceProxyAddress:           resourceProxyAddress,
		kubeclient:                     kubeclient,
	}
}

// RegisterCluster checks if a cluster secret exists for the agent and creates if it doesn't exist.
func (mgr *ClusterRegistrationManager) RegisterCluster(ctx context.Context, agentName string) error {
	if !mgr.selfClusterRegistrationEnabled {
		return nil
	}

	logCtx := log().WithField("agent", agentName)

	logCtx.Info("Self registering agent's cluster")

	// Check if cluster secret already exists for the given agent
	exists, err := cluster.ClusterSecretExists(ctx, mgr.kubeclient, mgr.namespace, agentName)
	if err != nil {
		return fmt.Errorf("error while checking if cluster secret exists: %w", err)
	}

	if exists {
		logCtx.Info("Cluster secret already exists for agent, skipping self cluster registration")
		return nil
	}

	// Create the cluster secret
	logCtx.Info("Registering agent's cluster")

	if err := cluster.CreateCluster(ctx, mgr.kubeclient, mgr.namespace, agentName, mgr.resourceProxyAddress); err != nil {
		return fmt.Errorf("failed to self register agent's cluster and create cluster secret: %w", err)
	}

	logCtx.Info("Agent's self cluster registration completed successfully")

	return nil
}

func (mgr *ClusterRegistrationManager) IsSelfClusterRegistrationEnabled() bool {
	return mgr.selfClusterRegistrationEnabled
}

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ModuleLogger("ClusterRegistration")
}
