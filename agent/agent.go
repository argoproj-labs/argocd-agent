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

package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	kubeapp "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/application"
	kubeappproject "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/appproject"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/manager/application"
	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/argoproj-labs/argocd-agent/pkg/client"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
)

const waitForSyncedDuration = 10 * time.Second

// Agent is a controller that synchronizes Application resources
type Agent struct {
	context  context.Context
	cancelFn context.CancelFunc
	options  AgentOptions
	// namespace is the namespace to manage applications in
	namespace string
	// allowedNamespaces is not currently used. See also 'namespaces' field in AgentOptions
	allowedNamespaces []string
	// infStopCh is not currently used
	infStopCh chan struct{}
	connected atomic.Bool
	// syncCh is not currently used
	syncCh         chan bool
	remote         *client.Remote
	appManager     *application.ApplicationManager
	projectManager *appproject.AppProjectManager
	mode           types.AgentMode
	// queues is a queue of create/update/delete events to send to the principal
	queues  *queue.SendRecvQueues
	emitter *event.EventSource
	// At present, 'watchLock' is only acquired on calls to 'addAppUpdateToQueue'. This behaviour was added as a short-term attempt to preserve update event ordering. However, this is known to be problematic due to the potential for race conditions, both within itself, and between other event processors like deleteAppCallback.
	watchLock sync.RWMutex
	version   *version.Version

	eventWriter *event.EventWriter
}

const defaultQueueName = "default"

// AgentOptions defines the options for a given Controller
type AgentOptions struct {
	// In the future, the 'namespaces' field may be used to support multiple Argo CD namespaces (for example, apps in any namespace) from a single agent instance, on a workload cluster. See 'filters.go' (in this package) for logic that reads from this value and avoids processing events outside of the specified namespaces.
	// - However, note that the 'namespace' field of Agent is automatically included by default.
	//
	// However, as of this writing, this feature is not available.
	namespaces []string
}

type AgentOption func(*Agent) error

// NewAgent creates a new agent instance, using the given client interfaces and
// options.
func NewAgent(ctx context.Context, appclient appclientset.Interface, namespace string, opts ...AgentOption) (*Agent, error) {
	a := &Agent{
		version: version.New("argocd-agent", "agent"),
	}
	a.infStopCh = make(chan struct{})
	a.namespace = namespace
	a.mode = types.AgentModeAutonomous

	for _, o := range opts {
		err := o(a)
		if err != nil {
			return nil, err
		}
	}

	if a.remote == nil {
		return nil, fmt.Errorf("remote not defined")
	}

	// Initial state of the agent is disconnected
	a.connected.Store(false)

	// a.managedApps = NewManagedApps()

	// We have one queue in the agent, named default
	a.queues = queue.NewSendRecvQueues()
	if err := a.queues.Create(defaultQueueName); err != nil {
		return nil, fmt.Errorf("unable to create default queue: %w", err)
	}

	var managerMode manager.ManagerMode
	if a.mode == types.AgentModeAutonomous {
		managerMode = manager.ManagerModeAutonomous
	} else if a.mode == types.AgentModeManaged {
		managerMode = manager.ManagerModeManaged
	} else {
		return nil, fmt.Errorf("unexpected agent mode: %v", a.mode)
	}

	// appListFunc and watchFunc are anonymous functions for the informer
	appListFunc := func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
		return appclient.ArgoprojV1alpha1().Applications(a.namespace).List(ctx, opts)
	}
	appWatchFunc := func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
		return appclient.ArgoprojV1alpha1().Applications(a.namespace).Watch(ctx, opts)
	}

	appInformerOptions := []informer.InformerOption[*v1alpha1.Application]{
		informer.WithListHandler[*v1alpha1.Application](appListFunc),
		informer.WithWatchHandler[*v1alpha1.Application](appWatchFunc),
		informer.WithAddHandler[*v1alpha1.Application](a.addAppCreationToQueue),
		informer.WithUpdateHandler[*v1alpha1.Application](a.addAppUpdateToQueue),
		informer.WithDeleteHandler[*v1alpha1.Application](a.addAppDeletionToQueue),
		informer.WithFilters[*v1alpha1.Application](a.DefaultAppFilterChain()),
		informer.WithNamespaceScope[*v1alpha1.Application](a.namespace),
	}

	appInformer, err := informer.NewInformer(ctx, appInformerOptions...)
	if err != nil {
		return nil, fmt.Errorf("could not instantiate application informer: %w", err)
	}

	allowUpsert := false
	if a.mode == types.AgentModeManaged {
		allowUpsert = true
	}

	appProjectManagerOption := []appproject.AppProjectManagerOption{
		appproject.WithAllowUpsert(true),
		appproject.WithRole(manager.ManagerRoleAgent),
		appproject.WithMode(managerMode),
	}

	projListFunc := func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
		return appclient.ArgoprojV1alpha1().AppProjects(a.namespace).List(ctx, opts)
	}
	projWatchFunc := func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
		return appclient.ArgoprojV1alpha1().AppProjects(a.namespace).Watch(ctx, opts)
	}

	projInformerOptions := []informer.InformerOption[*v1alpha1.AppProject]{
		informer.WithListHandler[*v1alpha1.AppProject](projListFunc),
		informer.WithWatchHandler[*v1alpha1.AppProject](projWatchFunc),
	}

	projInformer, err := informer.NewInformer(ctx, projInformerOptions...)
	if err != nil {
		return nil, fmt.Errorf("could not instantiate project informer: %w", err)
	}

	// The agent only supports Kubernetes as application backend
	a.appManager, err = application.NewApplicationManager(
		kubeapp.NewKubernetesBackend(appclient, a.namespace, appInformer, true),
		a.namespace,
		application.WithAllowUpsert(allowUpsert),
		application.WithRole(manager.ManagerRoleAgent),
		application.WithMode(managerMode),
	)
	if err != nil {
		return nil, err
	}

	a.projectManager, err = appproject.NewAppProjectManager(
		kubeappproject.NewKubernetesBackend(appclient, a.namespace, projInformer, true),
		a.namespace,
		appProjectManagerOption...)
	if err != nil {
		return nil, err
	}

	a.syncCh = make(chan bool, 1)
	return a, nil
}

func (a *Agent) Start(ctx context.Context) error {
	infCtx, cancelFn := context.WithCancel(ctx)
	log().Infof("Starting %s (agent) v%s (ns=%s, allowed_namespaces=%v, mode=%s)", a.version.Name(), a.version.Version(), a.namespace, a.options.namespaces, a.mode)
	a.context = infCtx
	a.cancelFn = cancelFn

	// Start the Application backend in the background
	go func() {
		if err := a.appManager.StartBackend(a.context); err != nil {
			log().WithError(err).Error("Application backend has exited non-successfully")
		} else {
			log().Info("Application backend has exited")
		}
	}()

	// Start the AppProject backend in the background
	go func() {
		if err := a.projectManager.StartBackend(a.context); err != nil {
			log().WithError(err).Error("AppProject backend has exited non-successfully")
		} else {
			log().Info("AppProject backend has exited")
		}
	}()

	// Wait for the app informer to be synced
	err := a.appManager.EnsureSynced(waitForSyncedDuration)
	if err != nil {
		return fmt.Errorf("failed to sync applications: %w", err)
	}

	if a.remote != nil {
		a.remote.SetClientMode(a.mode)
		// TODO: Right now, maintainConnection always returns nil. Revisit
		// this.
		_ = a.maintainConnection()
	}

	a.emitter = event.NewEventSource(fmt.Sprintf("agent://%s", "agent-managed"))

	return err
}

func (a *Agent) Stop() error {
	log().Infof("Stopping agent")
	tckr := time.NewTicker(2 * time.Second)
	if a.context == nil || a.cancelFn == nil {
		return fmt.Errorf("could not stop agent: agent has not started")
	}
	a.cancelFn()
	stopping := true
	for stopping {
		select {
		case <-a.context.Done():
			log().Infof("Stopped")
			stopping = false
		case <-tckr.C:
			log().Infof("Timeout reached, forcing stop")
			stopping = false
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
	return nil
}

// IsConnected returns whether the agent is connected to the principal
func (a *Agent) IsConnected() bool {
	return a.remote != nil && a.connected.Load()
}

// SetConnected sets the connection state of the agent
func (a *Agent) SetConnected(connected bool) {
	a.connected.Store(connected)
}

func log() *logrus.Entry {
	return logrus.WithField("module", "Agent")
}
