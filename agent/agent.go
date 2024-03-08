package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	kube_backend "github.com/jannfis/argocd-agent/internal/backend/kubernetes"
	"github.com/jannfis/argocd-agent/internal/event"
	appinformer "github.com/jannfis/argocd-agent/internal/informer/application"
	"github.com/jannfis/argocd-agent/internal/manager/application"
	"github.com/jannfis/argocd-agent/internal/queue"
	"github.com/jannfis/argocd-agent/internal/version"
	"github.com/jannfis/argocd-agent/pkg/client"
	"github.com/jannfis/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes"

	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
)

const waitForSyncedDuration = 1 * time.Second

// Agent is a controller that synchronizes Application resources
type Agent struct {
	context           context.Context
	cancelFn          context.CancelFunc
	client            kubernetes.Interface
	appclient         appclientset.Interface
	options           AgentOptions
	namespace         string
	allowedNamespaces []string
	informer          *appinformer.AppInformer
	infStopCh         chan struct{}
	connected         atomic.Bool
	syncCh            chan bool
	remote            *client.Remote
	appManager        *application.ApplicationManager
	mode              types.AgentMode
	queues            *queue.SendRecvQueues
	emitter           *event.Event
	watchLock         sync.RWMutex
}

const defaultQueueName = "default"

// AgentOptions defines the options for a given Controller
type AgentOptions struct {
	namespaces []string
}

type AgentOption func(*Agent) error

// NewAgent creates a new agent instance, using the given client interfaces and
// options.
func NewAgent(ctx context.Context, client kubernetes.Interface, appclient appclientset.Interface, namespace string, opts ...AgentOption) (*Agent, error) {
	a := &Agent{}
	a.client = client
	a.appclient = appclient
	a.infStopCh = make(chan struct{})
	a.namespace = namespace
	a.mode = types.AgentModeAutonomous

	for _, o := range opts {
		err := o(a)
		if err != nil {
			return nil, err
		}
	}

	// Initial state of the agent is disconnected
	a.connected.Store(false)

	// a.managedApps = NewManagedApps()

	// We have one queue in the agent, named default
	a.queues = queue.NewSendRecvQueues()
	if err := a.queues.Create(defaultQueueName); err != nil {
		return nil, fmt.Errorf("unable to create default queue: %w", err)
	}

	a.informer = appinformer.NewAppInformer(ctx, a.appclient, a.namespace,
		appinformer.WithListAppCallback(a.listAppCallback),
		appinformer.WithNewAppCallback(a.addAppCreationToQueue),
		appinformer.WithUpdateAppCallback(a.addAppUpdateToQueue),
		appinformer.WithDeleteAppCallback(a.addAppDeletionToQueue),
		appinformer.WithFilterChain(a.DefaultFilterChain()),
	)

	allowUpsert := false
	if a.mode == types.AgentModeManaged {
		allowUpsert = true
	}

	// The agent only supports Kubernetes as application backend
	a.appManager = application.NewApplicationManager(
		kube_backend.NewKubernetesBackend(a.appclient, a.namespace, a.informer, true),
		a.namespace,
		application.WithAllowUpsert(allowUpsert),
	)

	a.syncCh = make(chan bool, 1)
	return a, nil
}

func (a *Agent) Start(ctx context.Context) error {
	infCtx, cancelFn := context.WithCancel(ctx)
	log().Infof("Starting %s (agent) v%s (ns=%s, allowed_namespaces=%v, mode=%s)", version.Name(), version.Version(), a.namespace, a.options.namespaces, a.mode)
	a.context = infCtx
	a.cancelFn = cancelFn
	go func() {
		a.informer.Start(a.context.Done())
		log().Warnf("Informer has exited")
	}()
	if a.remote != nil {
		a.remote.SetClientMode(a.mode)
		// TODO: Right now, maintainConnection always returns nil. Revisit
		// this.
		_ = a.maintainConnection()
	}

	a.emitter = event.NewEventEmitter(fmt.Sprintf("agent://%s", "agent-managed"))

	// Wait for the informer to be synced
	err := a.informer.EnsureSynced(waitForSyncedDuration)
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
