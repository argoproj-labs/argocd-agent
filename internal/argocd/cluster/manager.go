// Copyright 2024 The argocd-agent Authors
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

/*
Package cluster implements various functions for working with Argo CD cluster
configuration.

Its main component is the cluster manager Manager, which essentially manages
Argo CD cluster secrets and maps them to agents.
*/
package cluster

import (
	"context"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj/argo-cd/v2/common"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
)

const LabelKeyClusterAgentMapping = "argocd-agent.argoproj-labs.io/agent-name"

const LabelValueManagerName = "argocd-agent"

// syncTimeout is the duration to wait until the manager's informer has synced
const syncTimeout = 30 * time.Second

// Manager manages Argo CD cluster secrets on the principal
type Manager struct {
	mutex      sync.RWMutex
	ctx        context.Context
	informer   *informer.Informer[*v1.Secret]
	namespace  string
	kubeclient kubernetes.Interface
	// clusters is a map of clusters to agent names.
	clusters map[string]*v1alpha1.Cluster
	// filters is a filter chain for the secret informer used by the cluster
	// manager
	filters *filter.Chain[*v1.Secret]
}

// NewManager instantiates and initializes a new Manager.
func NewManager(ctx context.Context, namespace string, kubeclient kubernetes.Interface) (*Manager, error) {
	var err error
	m := &Manager{
		clusters:   make(map[string]*v1alpha1.Cluster),
		namespace:  namespace,
		kubeclient: kubeclient,
		ctx:        ctx,
		filters:    filter.NewFilterChain[*v1.Secret](),
	}

	// We are only interested in secrets that have both, Argo CD's label for
	// cluster secrets and the label that holds our agent name.
	m.filters.AppendAdmitFilter(func(c *v1.Secret) bool {
		if v, ok := c.Labels[common.LabelKeySecretType]; !ok || v != common.LabelValueSecretTypeCluster {
			log().Tracef("No label secret-type of cluster found on secret %s/%s", c.Namespace, c.Name)
			return false
		}
		if v, ok := c.Labels[LabelKeyClusterAgentMapping]; !ok || v == "" {
			log().Tracef("No label for agent-mapping found on secret %s/%s", c.Namespace, c.Name)
			return false
		}
		return true
	})

	// Create the informer for our cluster secrets
	m.informer, err = informer.NewInformer[*v1.Secret](ctx,
		informer.WithListHandler[*v1.Secret](func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			return kubeclient.CoreV1().Secrets(m.namespace).List(ctx, opts)
		}),
		informer.WithWatchHandler[*v1.Secret](func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			return kubeclient.CoreV1().Secrets(m.namespace).Watch(ctx, opts)
		}),
		informer.WithAddHandler(m.onClusterAdded),
		informer.WithUpdateHandler(m.onClusterUpdated),
		informer.WithDeleteHandler(m.onClusterDeleted),
		informer.WithFilters(m.filters),
	)
	if err != nil {
		return nil, err
	}

	return m, nil
}

// Start starts the manager m and its informer. Start waits for the informer
// to be synced before returning. If the informer could not sync before the
// timeout expires, Start will return an error.
func (m *Manager) Start() error {
	log().Info("Starting cluster manager")
	go func() {
		err := m.informer.Start(m.ctx)
		if err != nil {
			log().WithError(err).Error("Could not start informer")
		}
	}()
	ctx, cancel := context.WithTimeout(m.ctx, syncTimeout)
	defer cancel()
	return m.informer.WaitForSync(ctx)
}

func (m *Manager) Stop() error {
	log().Info("Stopping cluster manager")
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.clusters = make(map[string]*v1alpha1.Cluster)
	return m.informer.Stop()
}

func log() *logrus.Entry {
	return logrus.WithField("component", "ClusterManager")
}
