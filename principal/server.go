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

package principal

import (
	context "context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/auth"
	kubeapp "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/application"
	kubeappproject "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/appproject"
	kubenamespace "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/namespace"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/issuer"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/manager/application"
	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/internal/resync"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/principal/resourceproxy"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/glob"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

type Server struct {
	options   *ServerOptions
	tlsConfig *tls.Config
	// listener contains GRPC server listener
	listener *Listener
	// server is not currently used
	server      *http.Server
	grpcServer  *grpc.Server
	authMethods *auth.Methods
	// queues contains events that are EITHER queued to be sent to the agent ('outbox'), OR that have been received by the agent and are waiting to be processed ('inbox').
	// Server uses clientID/namespace as a key, to refer to each specific agent's queue
	queues *queue.SendRecvQueues
	// namespace is the namespace the server will use for configuration. Set only when running out of cluster.
	namespace      string
	issuer         issuer.Issuer
	noauth         map[string]bool // noauth contains endpoints accessible without authentication
	ctx            context.Context
	ctxCancel      context.CancelFunc
	appManager     *application.ApplicationManager
	projectManager *appproject.AppProjectManager

	namespaceManager *kubenamespace.KubernetesBackend
	// At present, 'watchLock' is only acquired on calls to 'updateAppCallback'. This behaviour was added as a short-term attempt to preserve update event ordering. However, this is known to be problematic due to the potential for race conditions, both within itself, and between other event processors like deleteAppCallback.
	watchLock sync.RWMutex
	// clientMap is not currently used
	clientMap map[string]string
	// namespaceMap keeps track of which local namespaces are managed by agents using which mode
	// The key of namespaceMap is the client id which the agent used to authenticate with principal, via AuthSubject.ClientID (which, it is also assumed here, corresponds to a control plane namespace of the same name)
	// NOTE: clientLock should be owned before accessing namespaceMap
	namespaceMap map[string]types.AgentMode
	// clientLock should be owned before accessing namespaceMap
	clientLock sync.RWMutex
	// events is used to construct events to pass on the wire to connected agents.
	events     *event.EventSource
	version    *version.Version
	kubeClient *kube.KubernetesClient

	autoNamespaceAllow   bool
	autoNamespacePattern *regexp.Regexp
	autoNamespaceLabels  map[string]string

	// The Principal will rely on gRPC over WebSocket for bi-directional streaming. This option could be enabled
	// when there is an intermediate component that is HTTP/2 incompatible and downgrades the incoming request to HTTP/1.1
	enableWebSocket bool

	// resourceProxyEnabled indicates whether the resource proxy should be enabled
	resourceProxyEnabled bool
	// resourceProxy intercepts requests to the agent Kubernetes APIs
	resourceProxy *resourceproxy.ResourceProxy
	// resourceProxyListenAddr is the listener address for the resource proxy
	resourceProxyListenAddr string
	// resourceProxyTlsConfig is the TLS configuration for the resource proxy
	resourceProxyTlsConfig *tls.Config

	// clusterManager manages Argo CD cluster secrets and their mappings to agents
	clusterMgr *cluster.Manager

	// metrics holds principal side metrics
	metrics *metrics.PrincipalMetrics

	// Minimum time duration for agent to wait before sending next keepalive ping to principal
	// if agent sends ping more often than specified interval then connection will be dropped
	keepAliveMinimumInterval time.Duration
	// resources is a map of all resource keys for each agent
	resources *resources.AgentResources
	// resyncStatus indicates whether an agent has been resyned after the principal restarts
	resyncStatus *resyncStatus
	// notifyOnConnect will notify to run the handlers when an agent connects to the principal
	notifyOnConnect chan types.Agent
	// handlers to run when an agent connects to the principal
	handlersOnConnect []handlersOnConnect
}

type handlersOnConnect func(agent types.Agent) error

// noAuthEndpoints is a list of endpoints that are available without the need
// for the request to be authenticated.
var noAuthEndpoints = map[string]bool{
	"/versionapi.Version/Version":          true,
	"/authapi.Authentication/Authenticate": true,
}

const waitForSyncedDuration = 60 * time.Second

// defaultResourceProxyListenerAddr is the default listener address for the
// resource proxy.
const defaultResourceProxyListenerAddr = "0.0.0.0:9090"

func NewServer(ctx context.Context, kubeClient *kube.KubernetesClient, namespace string, opts ...ServerOption) (*Server, error) {
	s := &Server{
		options:         defaultOptions(),
		queues:          queue.NewSendRecvQueues(),
		namespace:       namespace,
		noauth:          noAuthEndpoints,
		version:         version.New("argocd-agent", "principal"),
		kubeClient:      kubeClient,
		resyncStatus:    newResyncStatus(),
		resources:       resources.NewAgentResources(),
		notifyOnConnect: make(chan types.Agent),
	}

	s.ctx, s.ctxCancel = context.WithCancel(ctx)

	for _, o := range opts {
		err := o(s)
		if err != nil {
			return nil, err
		}
	}

	s.handlersOnConnect = []handlersOnConnect{
		s.handleResyncOnConnect,
	}

	if s.authMethods == nil {
		s.authMethods = auth.NewMethods()
	}

	var err error

	if s.options.signingKey == nil {
		return nil, fmt.Errorf("unexpected missing JWT signing key")
	}

	s.issuer, err = issuer.NewIssuer("argocd-agent-server", issuer.WithRSAPrivateKey(s.options.signingKey))
	if err != nil {
		return nil, err
	}

	appFilters := s.defaultAppFilterChain()

	appInformerOpts := []informer.InformerOption[*v1alpha1.Application]{
		informer.WithListHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return kubeClient.ApplicationsClientset.ArgoprojV1alpha1().Applications("").List(ctx, opts)
		}),
		informer.WithWatchHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return kubeClient.ApplicationsClientset.ArgoprojV1alpha1().Applications("").Watch(ctx, opts)
		}),
		informer.WithAddHandler[*v1alpha1.Application](s.newAppCallback),
		informer.WithUpdateHandler[*v1alpha1.Application](s.updateAppCallback),
		informer.WithDeleteHandler[*v1alpha1.Application](s.deleteAppCallback),
		informer.WithFilters[*v1alpha1.Application](appFilters),
	}

	appManagerOpts := []application.ApplicationManagerOption{
		application.WithAllowUpsert(true),
		application.WithRole(manager.ManagerRolePrincipal),
	}

	projInformerOpts := []informer.InformerOption[*v1alpha1.AppProject]{
		informer.WithListHandler[*v1alpha1.AppProject](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return kubeClient.ApplicationsClientset.ArgoprojV1alpha1().AppProjects("").List(ctx, opts)
		}),
		informer.WithWatchHandler[*v1alpha1.AppProject](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return kubeClient.ApplicationsClientset.ArgoprojV1alpha1().AppProjects("").Watch(ctx, opts)
		}),
		informer.WithAddHandler[*v1alpha1.AppProject](s.newAppProjectCallback),
		informer.WithUpdateHandler[*v1alpha1.AppProject](s.updateAppProjectCallback),
		informer.WithDeleteHandler[*v1alpha1.AppProject](s.deleteAppProjectCallback),
	}

	projManagerOpts := []appproject.AppProjectManagerOption{
		appproject.WithAllowUpsert(true),
		appproject.WithRole(manager.ManagerRolePrincipal),
	}

	if s.options.metricsPort > 0 {
		s.metrics = metrics.NewPrincipalMetrics()

		appInformerOpts = append(appInformerOpts, informer.WithMetrics[*v1alpha1.Application](prometheus.NewRegistry(), metrics.NewInformerMetrics("applications")))
		projInformerOpts = append(projInformerOpts, informer.WithMetrics[*v1alpha1.AppProject](prometheus.NewRegistry(), metrics.NewInformerMetrics("appprojects")))
	}

	appInformer, err := informer.NewInformer(ctx, appInformerOpts...)
	if err != nil {
		return nil, err
	}

	projectInformer, err := informer.NewInformer(s.ctx,
		projInformerOpts...,
	)
	if err != nil {
		return nil, err
	}

	s.appManager, err = application.NewApplicationManager(kubeapp.NewKubernetesBackend(kubeClient.ApplicationsClientset, s.namespace, appInformer, true), s.namespace,
		appManagerOpts...,
	)
	if err != nil {
		return nil, err
	}

	s.projectManager, err = appproject.NewAppProjectManager(kubeappproject.NewKubernetesBackend(kubeClient.ApplicationsClientset, s.namespace, projectInformer, true), s.namespace, projManagerOpts...)
	if err != nil {
		return nil, err
	}

	nsInformerOpts := []informer.InformerOption[*corev1.Namespace]{
		informer.WithListHandler[*corev1.Namespace](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return kubeClient.Clientset.CoreV1().Namespaces().List(ctx, opts)
		}),
		informer.WithWatchHandler[*corev1.Namespace](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return kubeClient.Clientset.CoreV1().Namespaces().Watch(ctx, opts)
		}),
		informer.WithDeleteHandler[*corev1.Namespace](s.deleteNamespaceCallback),
	}

	nsInformer, err := informer.NewInformer(ctx, nsInformerOpts...)
	if err != nil {
		return nil, err
	}
	s.namespaceManager = kubenamespace.NewKubernetesBackend(nsInformer)

	s.clientMap = map[string]string{
		`{"clientID":"argocd","mode":"autonomous"}`: "argocd",
	}
	s.namespaceMap = map[string]types.AgentMode{
		"argocd": types.AgentModeAutonomous,
	}

	if s.resourceProxyListenAddr == "" {
		s.resourceProxyListenAddr = defaultResourceProxyListenerAddr
	}

	// Instantiate our ResourceProxy to intercept Kubernetes requests from Argo
	// CD's API server.
	if s.resourceProxyEnabled {
		// TODO(jannfis): Enable fetching APIs and resource counts
		s.resourceProxy, err = resourceproxy.New(s.resourceProxyListenAddr,
			// For matching resource requests from the Argo CD API
			resourceproxy.WithRequestMatcher(
				resourceRequestRegexp,
				[]string{"get"},
				s.processResourceRequest,
			),
			// Fake version output
			resourceproxy.WithRequestMatcher(
				`^/version$`,
				[]string{"get"},
				s.proxyVersion,
			),
			resourceproxy.WithTLSConfig(s.resourceProxyTlsConfig),
		)
		if err != nil {
			return nil, err
		}
	}

	// Instantiate the cluster manager to handle Argo CD cluster secrets for
	// agents.
	s.clusterMgr, err = cluster.NewManager(s.ctx, s.namespace, s.options.redisAddress, s.options.redisCompressionType, s.kubeClient.Clientset)
	if err != nil {
		return nil, err
	}

	s.resources = resources.NewAgentResources()

	return s, nil
}

func (s *Server) proxyVersion(w http.ResponseWriter, r *http.Request, params resourceproxy.Params) {
	var kubeVersion = struct {
		Major        string
		Minor        string
		GitVersion   string
		GitCommit    string
		GitTreeState string
		BuildDate    string
		GoVersion    string
		Compiler     string
		Platform     string
	}{
		Major:        "1",
		Minor:        "28",
		GitVersion:   "1.28.5+argocd-agent",
		GitCommit:    "841856557ef0f6a399096c42635d114d6f2cf7f4",
		GitTreeState: "clean",
		BuildDate:    "n/a",
		GoVersion:    goruntime.Version(),
		Compiler:     goruntime.Compiler,
		Platform:     fmt.Sprintf("%s/%s", goruntime.GOOS, goruntime.GOARCH),
	}

	version, err := json.MarshalIndent(kubeVersion, "", "  ")
	if err != nil {
		log().Errorf("Could not marshal JSON: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(version)
	if err != nil {
		log().Errorf("Could not write K8s version to client: %v", err)
	}
}

// Start starts the Server s and its listeners in their own go routines. Any
// error during startup, before the go routines are running, will be returned
// immediately. Errors during the runtime will be propagated via errch.
func (s *Server) Start(ctx context.Context, errch chan error) error {

	if s.namespace != "" {
		log().Infof("Starting %s (server) v%s (ns=%s, allowed_namespaces=%v)", s.version.Name(), s.version.Version(), s.namespace, s.options.namespaces)
	} else {
		log().Infof("Starting %s (server) v%s (allowed_namespaces=%v)", s.version.Name(), s.version.Version(), s.options.namespaces)
	}

	if s.options.serveGRPC {
		if err := s.serveGRPC(ctx, s.metrics, errch); err != nil {
			return err
		}
	}

	if s.options.metricsPort > 0 {
		metrics.StartMetricsServer(metrics.WithListener("", s.options.metricsPort))

		// A goroutine is started which calculates average connection time of all agents
		// to export in metrics after every 3 minutes
		go func() {
			for {
				metrics.GetAvgAgentConnectionTime(s.metrics)
				time.Sleep(metrics.AvgCalculationInterval)
			}
		}()
	}

	go s.RunHandlersOnConnect(s.ctx)

	err := s.StartEventProcessor(s.ctx)
	if err != nil {
		return nil
	}

	// The application informer lives in its own go routine
	go func() {
		if err := s.appManager.StartBackend(s.ctx); err != nil {
			log().WithError(err).Error("Application backend has exited non-successfully")
		} else {
			log().Info("Application backend has exited")
		}
	}()

	// The project informer lives in its own go routine
	go func() {
		if err := s.projectManager.StartBackend(s.ctx); err != nil {
			log().WithError(err).Error("AppProject backend has exited non-successfully")
		} else {
			log().Info("AppProject backend has exited")
		}
	}()

	// The namespace informer lives in its own go routine
	go func() {
		if err := s.namespaceManager.StartInformer(s.ctx); err != nil {
			log().WithError(err).Error("Namespace informer has exited non-successfully")
		} else {
			log().Info("Namespace informer has exited")
		}
	}()

	s.events = event.NewEventSource(s.options.serverName)

	if err := s.appManager.EnsureSynced(waitForSyncedDuration); err != nil {
		return fmt.Errorf("unable to sync Application informer: %w", err)
	}
	log().Infof("Application informer synced and ready")

	if err := s.projectManager.EnsureSynced(waitForSyncedDuration); err != nil {
		return fmt.Errorf("unable to sync AppProject informer: %w", err)
	}
	log().Infof("AppProject informer synced and ready")

	// Start resource proxy if it is enabled
	if s.resourceProxy != nil {
		_, err = s.resourceProxy.Start(s.ctx)
		if err != nil {
			return fmt.Errorf("unable to start ResourceProxy: %w", err)
		}
		log().Infof("Resource proxy started")
	} else {
		log().Infof("Resource proxy is disabled")
	}

	err = s.clusterMgr.Start()
	if err != nil {
		return err
	}
	if err := s.namespaceManager.EnsureSynced(waitForSyncedDuration); err != nil {
		return fmt.Errorf("unable to sync Namespace informer: %w", err)
	}
	log().Infof("Namespace informer synced and ready")

	return nil
}

// When the principal process restarts, the agent could be out of sync with the principal.
// resyncStatus indicates whether we need to inform the agent that the principal has been restarted.
type resyncStatus struct {
	mu sync.RWMutex
	// key: agent name
	resync map[string]bool
}

func newResyncStatus() *resyncStatus {
	return &resyncStatus{
		resync: map[string]bool{},
	}
}

func (rs *resyncStatus) isResynced(agentName string) bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	_, ok := rs.resync[agentName]
	return ok
}

func (rs *resyncStatus) resynced(agentName string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	rs.resync[agentName] = true
}

// RunHandlersOnConnect runs the registered handlers when an agent connects to the principal
func (s *Server) RunHandlersOnConnect(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			log().Errorf("stop running handlers: %v", ctx.Err())
			return
		case agent := <-s.notifyOnConnect:
			logCtx := log().WithFields(logrus.Fields{
				"agent": agent.Name(),
				"mode":  agent.Mode(),
			})

			logCtx.Trace("Running handlers on a newly connected agent")
			for _, handler := range s.handlersOnConnect {
				if err := handler(agent); err != nil {
					logCtx.Errorf("failed to run handler: %v", err)
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// handleResyncOnConnect is called whenever an agent connects and it determines if an agent needs to be
// resynced with the principal. We use resyncStatus to differentiate if the principal has restarted (resync required)
// or the agent has just reconnected after a network issue (no resync). When the process restarts, we lose the
// resync status and thereby trigger the resync mechanism whenever the agent connects back.
func (s *Server) handleResyncOnConnect(agent types.Agent) error {
	logCtx := log().WithFields(logrus.Fields{
		"agent": agent.Name(),
		"mode":  agent.Mode(),
	})

	if s.resyncStatus.isResynced(agent.Name()) {
		logCtx.Trace("Skipping resync messages since the principal has synced with this agent before")
		return nil
	}

	logCtx.Trace("Checking if the principal is out of sync with the agent")

	sendQ := s.queues.SendQ(agent.Name())
	if sendQ == nil {
		return fmt.Errorf("no send queue found for agent: %s", agent.Name())
	}

	// In autonomous mode, principal acts as peer and it should resync with the agent.
	if agent.Mode() == types.AgentModeAutonomous.String() {
		// Principal should request updates from the Agent to revert any changes on the Principal side.
		dynClient, err := dynamic.NewForConfig(s.kubeClient.RestConfig)
		if err != nil {
			return err
		}

		resyncHandler := resync.NewRequestHandler(dynClient, sendQ, s.events, s.resources.Get(agent.Name()), logCtx)
		go resyncHandler.SendRequestUpdates(s.ctx)

		// Principal should request SyncedResourceList to revert any deletions on the Principal side.
		checksum := s.resources.Checksum(agent.Name())

		// send the checksum to the principal
		ev, err := s.events.RequestSyncedResourceListEvent(checksum)
		if err != nil {
			return fmt.Errorf("failed to create SyncedResourceList event: %v", err)
		}

		sendQ.Add(ev)
		logCtx.Trace("Sent a request for SyncedResourceList")
	} else {
		// In managed mode, principal is the source of truth and the it should request resource resync
		ev, err := s.events.RequestResourceResyncEvent()
		if err != nil {
			return fmt.Errorf("failed to create ResourceResync event: %v", err)
		}

		sendQ.Add(ev)
		logCtx.Trace("Sent a request for ResourceResync")
	}

	s.resyncStatus.resynced(agent.Name())
	return nil
}

// Shutdown shuts down the server s. If no server is running, or shutting down
// results in an error, an error is returned.
func (s *Server) Shutdown() error {
	var err error

	if s.resourceProxy != nil {
		if err = s.resourceProxy.Stop(s.ctx); err != nil {
			return err
		}
	}

	if err = s.clusterMgr.Stop(); err != nil {
		return err
	}

	log().Debugf("Shutdown requested")
	// Cancel server-wide context
	s.ctxCancel()

	if s.server != nil {
		if s.options.gracePeriod > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), s.options.gracePeriod)
			defer cancel()
			log().Infof("Server shutdown requested, allowing client connections to shut down for %v", s.options.gracePeriod)
			err = s.server.Shutdown(ctx)
		} else {
			log().Infof("Closing server")
			err = s.server.Close()
		}
		s.server = nil
	} else if s.grpcServer != nil {
		log().Infof("Shutting down server")
		s.grpcServer.Stop()
		s.grpcServer = nil
	} else {
		return fmt.Errorf("no server running")
	}
	return err
}

// loadTLSConfig will configure and return a tls.Config object that can be
// used by the server's listener. It will use options set in the server for
// configuring the returned object.
func (s *Server) loadTLSConfig() (*tls.Config, error) {
	var cert tls.Certificate
	var err error

	if s.options.tlsCertPath != "" && s.options.tlsKeyPath != "" {
		cert, err = tlsutil.TlsCertFromFile(s.options.tlsCertPath, s.options.tlsKeyPath, false)
	} else if s.options.tlsCert != nil && s.options.tlsKey != nil {
		cert, err = tlsutil.TlsCertFromX509(s.options.tlsCert, s.options.tlsKey)
	}
	if err != nil {
		return nil, fmt.Errorf("unable to load TLS config: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	// If the server is configured to require client certificates, set up the
	// TLS config accordingly. On verification, we store the common name of
	// the validated certificate in the server's context, so we can access it
	// later on.
	if s.options.requireClientCerts {
		log().Infof("This server will require TLS client certs as part of authentication")
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		tlsConfig.ClientCAs = s.options.rootCa
	}

	return tlsConfig, nil
}

// defaultAppFilterChain returns the default filter chain for server s to use
func (s *Server) defaultAppFilterChain() *filter.Chain[*v1alpha1.Application] {
	c := filter.NewFilterChain[*v1alpha1.Application]()
	c.AppendAdmitFilter(func(res *v1alpha1.Application) bool {
		return glob.MatchStringInList(s.options.namespaces, res.Namespace, glob.REGEXP)
	})
	return c
}

// ListenerForE2EOnly returns the listener of Server s
func (s *Server) ListenerForE2EOnly() *Listener {
	return s.listener
}

// TokenIssuerForE2EOnly returns the token issuer of Server s
func (s *Server) TokenIssuerForE2EOnly() issuer.Issuer {
	return s.issuer
}

func log() *logrus.Entry {
	return logrus.WithField("module", "server")
}

func (s *Server) AuthMethodsForE2EOnly() *auth.Methods {
	return s.authMethods
}

func (s *Server) QueuesForE2EOnly() *queue.SendRecvQueues {
	return s.queues
}

func (s *Server) agentMode(namespace string) types.AgentMode {
	s.clientLock.RLock()
	defer s.clientLock.RUnlock()
	if mode, ok := s.namespaceMap[namespace]; ok {
		return mode
	}
	return types.AgentModeUnknown
}

func (s *Server) setAgentMode(namespace string, mode types.AgentMode) {
	s.clientLock.Lock()
	defer s.clientLock.Unlock()
	s.namespaceMap[namespace] = mode
}
