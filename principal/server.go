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
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	kubeapp "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/application"
	kubeappproject "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/appproject"
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
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/glob"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
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
	kubeClient kubernetes.Interface

	autoNamespaceAllow   bool
	autoNamespacePattern *regexp.Regexp
	autoNamespaceLabels  map[string]string

	// The Principal will rely on gRPC over WebSocket for bi-directional streaming. This option could be enabled
	// when there is an intermediate component that is HTTP/2 incompatible and downgrades the incoming request to HTTP/1.1
	enableWebSocket bool
}

// noAuthEndpoints is a list of endpoints that are available without the need
// for the request to be authenticated.
var noAuthEndpoints = map[string]bool{
	"/versionapi.Version/Version":          true,
	"/authapi.Authentication/Authenticate": true,
}

const waitForSyncedDuration = 60 * time.Second

func NewServer(ctx context.Context, kubeClient *kube.KubernetesClient, namespace string, opts ...ServerOption) (*Server, error) {
	s := &Server{
		options:    defaultOptions(),
		queues:     queue.NewSendRecvQueues(),
		namespace:  namespace,
		noauth:     noAuthEndpoints,
		version:    version.New("argocd-agent", "principal"),
		kubeClient: kubeClient.Clientset,
	}

	s.ctx, s.ctxCancel = context.WithCancel(ctx)

	for _, o := range opts {
		err := o(s)
		if err != nil {
			return nil, err
		}
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
		appInformerOpts = append(appInformerOpts, informer.WithMetrics[*v1alpha1.Application](prometheus.NewRegistry(), metrics.NewInformerMetrics("applications")))
		appManagerOpts = append(appManagerOpts, application.WithMetrics(metrics.NewApplicationClientMetrics()))
		projInformerOpts = append(projInformerOpts, informer.WithMetrics[*v1alpha1.AppProject](prometheus.NewRegistry(), metrics.NewInformerMetrics("applications")))
		projManagerOpts = append(projManagerOpts, appproject.WithMetrics(metrics.NewAppProjectClientMetrics()))
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

	s.clientMap = map[string]string{
		`{"clientID":"argocd","mode":"autonomous"}`: "argocd",
	}
	s.namespaceMap = map[string]types.AgentMode{
		"argocd": types.AgentModeAutonomous,
	}

	return s, nil
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
		if err := s.serveGRPC(ctx, errch); err != nil {
			return err
		}
	}

	if s.options.metricsPort > 0 {
		metrics.StartMetricsServer(metrics.WithListener("", s.options.metricsPort))
	}

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

	s.events = event.NewEventSource(s.options.serverName)

	if err := s.appManager.EnsureSynced(waitForSyncedDuration); err != nil {
		return fmt.Errorf("unable to sync Application informer: %w", err)
	}
	log().Infof("Application informer synced and ready")

	if err := s.projectManager.EnsureSynced(waitForSyncedDuration); err != nil {
		return fmt.Errorf("unable to sync AppProject informer: %w", err)
	}
	log().Infof("AppProject informer synced and ready")

	return nil
}

// Shutdown shuts down the server s. If no server is running, or shutting down
// results in an error, an error is returned.
func (s *Server) Shutdown() error {
	var err error

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
