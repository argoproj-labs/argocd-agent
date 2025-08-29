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
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/backend"
	kubeapp "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/application"
	kubeappproject "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/appproject"
	kubenamespace "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/namespace"
	kuberepository "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/repository"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/manager/application"
	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	"github.com/argoproj-labs/argocd-agent/internal/manager/repository"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/argoproj-labs/argocd-agent/pkg/client"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	appCache "github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	appstatecache "github.com/argoproj/argo-cd/v3/util/cache/appstate"
	ty "k8s.io/apimachinery/pkg/types"
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
	syncCh           chan bool
	remote           *client.Remote
	appManager       *application.ApplicationManager
	projectManager   *appproject.AppProjectManager
	repoManager      *repository.RepositoryManager
	namespaceManager *kubenamespace.KubernetesBackend
	mode             types.AgentMode
	// queues is a queue of create/update/delete events to send to the principal
	queues  *queue.SendRecvQueues
	emitter *event.EventSource
	// At present, 'watchLock' is only acquired on calls to 'addAppUpdateToQueue'. This behaviour was added as a short-term attempt to preserve update event ordering. However, this is known to be problematic due to the potential for race conditions, both within itself, and between other event processors like deleteAppCallback.
	watchLock sync.RWMutex

	eventWriter *event.EventWriter
	version     *version.Version
	kubeClient  *kube.KubernetesClient

	// metrics holds agent side metrics
	metrics *metrics.AgentMetrics

	// determines if a resync check is done with the principal when the agent restarts.
	resyncedOnStart bool
	// resources is a list of all the resources that are currently being managed by the agent
	resources *resources.Resources

	// redisProxyMsgHandler manages redis connection state for agent
	redisProxyMsgHandler *redisProxyMsgHandler

	// enableResourceProxy determines if the agent should proxy resources to the principal
	enableResourceProxy bool

	cacheRefreshInterval time.Duration
	clusterCache         *appstatecache.Cache
}

const defaultQueueName = "default"

// AgentOptions defines the options for a given Controller
type AgentOptions struct {
	// In the future, the 'namespaces' field may be used to support multiple Argo CD namespaces (for example, apps in any namespace) from a single agent instance, on a workload cluster. See 'filters.go' (in this package) for logic that reads from this value and avoids processing events outside of the specified namespaces.
	// - However, note that the 'namespace' field of Agent is automatically included by default.
	//
	// However, as of this writing, this feature is not available.
	namespaces []string

	metricsPort int

	healthzPort int
}

// AgentOption is a functional option type used to configure an Agent instance during initialization.
// It takes a pointer to an Agent and returns an error if the configuration fails.
type AgentOption func(*Agent) error

// NewAgent creates a new agent instance, using the given client interfaces and
// options.
func NewAgent(ctx context.Context, client *kube.KubernetesClient, namespace string, opts ...AgentOption) (*Agent, error) {
	a := &Agent{
		version: version.New("argocd-agent"),
	}
	a.infStopCh = make(chan struct{})
	a.namespace = namespace
	a.mode = types.AgentModeAutonomous
	a.redisProxyMsgHandler = &redisProxyMsgHandler{}
	// Resource proxy is enabled by default.
	a.enableResourceProxy = true

	for _, o := range opts {
		err := o(a)
		if err != nil {
			return nil, err
		}
	}

	if a.remote == nil {
		return nil, fmt.Errorf("remote not defined")
	}

	a.kubeClient = client

	// Initial state of the agent is disconnected
	a.connected.Store(false)

	// We have one queue in the agent, named default
	a.queues = queue.NewSendRecvQueues()
	if err := a.queues.Create(defaultQueueName); err != nil {
		return nil, fmt.Errorf("unable to create default queue: %w", err)
	}

	var managerMode manager.ManagerMode
	switch a.mode {
	case types.AgentModeAutonomous:
		managerMode = manager.ManagerModeAutonomous
	case types.AgentModeManaged:
		managerMode = manager.ManagerModeManaged
	default:
		return nil, fmt.Errorf("unexpected agent mode: %v", a.mode)
	}

	// appListFunc and watchFunc are anonymous functions for the informer
	appListFunc := func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
		return client.ApplicationsClientset.ArgoprojV1alpha1().Applications(a.namespace).List(ctx, opts)
	}
	appWatchFunc := func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
		return client.ApplicationsClientset.ArgoprojV1alpha1().Applications(a.namespace).Watch(ctx, opts)
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

	appProjectManagerOption := []appproject.AppProjectManagerOption{
		appproject.WithAllowUpsert(true),
		appproject.WithRole(manager.ManagerRoleAgent),
		appproject.WithMode(managerMode),
	}

	appManagerOpts := []application.ApplicationManagerOption{
		application.WithRole(manager.ManagerRoleAgent),
		application.WithMode(managerMode),
	}

	if a.options.metricsPort > 0 {
		a.metrics = metrics.NewAgentMetrics()
	}

	appInformer, err := informer.NewInformer(ctx, appInformerOptions...)
	if err != nil {
		return nil, fmt.Errorf("could not instantiate application informer: %w", err)
	}

	// Only allow upsert for managed agents
	allowUpsert := a.mode == types.AgentModeManaged

	appManagerOpts = append(appManagerOpts, application.WithAllowUpsert(allowUpsert))

	projListFunc := func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
		return client.ApplicationsClientset.ArgoprojV1alpha1().AppProjects(a.namespace).List(ctx, opts)
	}

	projWatchFunc := func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
		return client.ApplicationsClientset.ArgoprojV1alpha1().AppProjects(a.namespace).Watch(ctx, opts)
	}

	projInformerOptions := []informer.InformerOption[*v1alpha1.AppProject]{
		informer.WithListHandler[*v1alpha1.AppProject](projListFunc),
		informer.WithWatchHandler[*v1alpha1.AppProject](projWatchFunc),
		informer.WithAddHandler[*v1alpha1.AppProject](a.addAppProjectCreationToQueue),
		informer.WithUpdateHandler[*v1alpha1.AppProject](a.addAppProjectUpdateToQueue),
		informer.WithDeleteHandler[*v1alpha1.AppProject](a.addAppProjectDeletionToQueue),
	}

	projInformer, err := informer.NewInformer(ctx, projInformerOptions...)
	if err != nil {
		return nil, fmt.Errorf("could not instantiate project informer: %w", err)
	}

	// The agent only supports Kubernetes as application backend
	a.appManager, err = application.NewApplicationManager(
		kubeapp.NewKubernetesBackend(client.ApplicationsClientset, a.namespace, appInformer, true),
		a.namespace,
		appManagerOpts...,
	)
	if err != nil {
		return nil, err
	}

	a.projectManager, err = appproject.NewAppProjectManager(
		kubeappproject.NewKubernetesBackend(client.ApplicationsClientset, a.namespace, projInformer, true),
		a.namespace,
		appProjectManagerOption...)
	if err != nil {
		return nil, err
	}

	repoInformerOptions := []informer.InformerOption[*corev1.Secret]{
		informer.WithListHandler[*corev1.Secret](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return client.Clientset.CoreV1().Secrets(a.namespace).List(ctx, opts)
		}),
		informer.WithWatchHandler[*corev1.Secret](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return client.Clientset.CoreV1().Secrets(a.namespace).Watch(ctx, opts)
		}),
		informer.WithAddHandler[*corev1.Secret](a.handleRepositoryCreation),
		informer.WithUpdateHandler[*corev1.Secret](a.handleRepositoryUpdate),
		informer.WithDeleteHandler[*corev1.Secret](a.handleRepositoryDeletion),
		informer.WithFilters(kuberepository.DefaultFilterChain(a.namespace)),
	}

	repoInformer, err := informer.NewInformer(ctx, repoInformerOptions...)
	if err != nil {
		return nil, fmt.Errorf("could not instantiate repository informer: %w", err)
	}

	repoBackened := kuberepository.NewKubernetesBackend(client.Clientset, a.namespace, repoInformer, true)
	a.repoManager = repository.NewManager(repoBackened, a.namespace, true)

	nsInformerOpts := []informer.InformerOption[*corev1.Namespace]{
		informer.WithListHandler[*corev1.Namespace](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return client.Clientset.CoreV1().Namespaces().List(ctx, opts)
		}),
		informer.WithWatchHandler[*corev1.Namespace](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return client.Clientset.CoreV1().Namespaces().Watch(ctx, opts)
		}),
		informer.WithDeleteHandler[*corev1.Namespace](a.deleteNamespaceCallback),
	}

	nsInformer, err := informer.NewInformer(ctx, nsInformerOpts...)
	if err != nil {
		return nil, err
	}
	a.namespaceManager = kubenamespace.NewKubernetesBackend(nsInformer)

	a.resources = resources.NewResources()

	a.syncCh = make(chan bool, 1)

	argoClient, argoCache, err := a.getRedisClientAndCache()
	if err != nil {
		return nil, err
	}

	a.redisProxyMsgHandler.argoCDRedisCache = argoCache
	a.redisProxyMsgHandler.argoCDRedisClient = argoClient
	a.redisProxyMsgHandler.connections = &connectionEntries{
		connMap: map[string]connectionEntry{},
	}

	clusterCache, err := cluster.NewClusterCacheInstance(ctx, client.Clientset,
		a.namespace, a.redisProxyMsgHandler.redisAddress, cacheutil.RedisCompressionGZip)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster cache instance: %v", err)
	}
	a.clusterCache = clusterCache

	return a, nil
}

func (a *Agent) Start(ctx context.Context) error {
	infCtx, cancelFn := context.WithCancel(ctx)
	log().Infof("Starting %s (agent) v%s (ns=%s, allowed_namespaces=%v, mode=%s, auth=%s)", a.version.Name(), a.version.Version(), a.namespace, a.options.namespaces, a.mode, a.remote.AuthMethod())
	a.context = infCtx
	a.cancelFn = cancelFn

	// For managed-agent we need to maintain a cache to keep applications in sync with last known state of
	// principal in case agent is disconnected with principal or application in managed-cluster is modified.
	if a.mode == types.AgentModeManaged {
		log().Infof("Recreating application spec cache from existing resources on cluster")
		appList, err := a.appManager.List(ctx, backend.ApplicationSelector{Namespaces: []string{a.namespace}})
		if err != nil {
			log().Errorf("Error while fetching list of applications: %v", err)
		}

		for _, app := range appList {
			sourceUID, exists := app.Annotations[manager.SourceUIDAnnotation]
			if exists {
				appCache.SetApplicationSpec(ty.UID(sourceUID), app.Spec, log())
			}
		}
	}

	if a.options.metricsPort > 0 {
		metrics.StartMetricsServer(metrics.WithListener("", a.options.metricsPort))
	}

	a.emitter = event.NewEventSource(fmt.Sprintf("agent://%s", "agent-managed"))

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

	// Wait for the appProject informer to be synced
	err = a.projectManager.EnsureSynced(waitForSyncedDuration)
	if err != nil {
		return fmt.Errorf("failed to sync appProjects: %w", err)
	}

	// The namespace informer lives in its own go routine
	go func() {
		if err := a.namespaceManager.StartInformer(a.context); err != nil {
			log().WithError(err).Error("Namespace informer has exited non-successfully")
		} else {
			log().Info("Namespace informer has exited")
		}
	}()

	if err := a.namespaceManager.EnsureSynced(waitForSyncedDuration); err != nil {
		return fmt.Errorf("unable to sync Namespace informer: %w", err)
	}
	log().Infof("Namespace informer synced and ready")

	// Start the Repository backend in the background
	go func() {
		if err := a.repoManager.StartBackend(a.context); err != nil {
			log().WithError(err).Error("Repository backend has exited non-successfully")
		} else {
			log().Info("Repository backend has exited")
		}
	}()

	if err = a.repoManager.EnsureSynced(waitForSyncedDuration); err != nil {
		return fmt.Errorf("unable to sync Repository informer: %w", err)
	}
	log().Infof("Repository informer synced and ready")

	if a.options.healthzPort > 0 {
		// Endpoint to check if the agent is up and running
		http.HandleFunc("/healthz", a.healthzHandler)
		healthzAddr := fmt.Sprintf(":%d", a.options.healthzPort)

		log().Infof("Starting healthz server on %s", healthzAddr)
		//nolint:errcheck
		go http.ListenAndServe(healthzAddr, nil)
	}

	// Start the background process of periodic sync of cluster cache info.
	// This will send periodic updates of Application, Resource and API counts to principal.
	if a.mode == types.AgentModeManaged {
		go func() {
			ticker := time.NewTicker(a.cacheRefreshInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					a.addClusterCacheInfoUpdateToQueue()
				case <-a.context.Done():
					return
				}
			}
		}()
	}

	if a.remote != nil {
		a.remote.SetClientMode(a.mode)
		// TODO: Right now, maintainConnection always returns nil. Revisit
		// this.
		_ = a.maintainConnection()
	}

	return nil
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
	return logging.ModuleLogger("Agent")
}

func (a *Agent) healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	if a.IsConnected() {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}
