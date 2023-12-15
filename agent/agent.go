package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jannfis/argocd-agent/internal/application"
	kube_backend "github.com/jannfis/argocd-agent/internal/backend/kubernetes"
	"github.com/jannfis/argocd-agent/internal/event"
	"github.com/jannfis/argocd-agent/internal/filter"
	appinformer "github.com/jannfis/argocd-agent/internal/informers/application"
	"github.com/jannfis/argocd-agent/internal/queue"
	"github.com/jannfis/argocd-agent/internal/version"
	"github.com/jannfis/argocd-agent/pkg/client"
	"github.com/jannfis/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
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
	filters           *filter.Chain
	connected         atomic.Bool
	syncCh            chan bool
	remote            *client.Remote
	appManager        *application.Manager
	mode              types.AgentMode

	queues  *queue.SendRecvQueues
	emitter *event.Event

	// managedApps is a map whose key is the qualified
	// managedApps *ManagedApps
	watchLock sync.RWMutex
}

const defaultQueueName = "default"

// AgentOptions defines the options for a given Controller
type AgentOptions struct {
	namespaces []string
}

type AgentOption func(*Agent) error

func (a *Agent) informerListCallback(apps []v1alpha1.Application) []v1alpha1.Application {
	newApps := make([]v1alpha1.Application, 0)
	for _, app := range apps {
		if a.filters.Admit(&app) {
			newApps = append(newApps, app)
		}
	}
	return newApps
}

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
	a.queues.Create(defaultQueueName)

	a.informer = appinformer.NewAppInformer(ctx, a.appclient, a.namespace,
		appinformer.WithListAppCallback(a.listAppCallback),
		appinformer.WithNewAppCallback(a.addAppCreationToQueue),
		appinformer.WithUpdateAppCallback(a.addAppUpdateToQueue),
		appinformer.WithDeleteAppCallback(a.addAppDeletionToQueue),
		appinformer.WithFilterChain(a.DefaultFilterChain()),
	)

	// The agent only supports Kubernetes as application backend
	a.appManager = application.NewManager(
		kube_backend.NewKubernetesBackend(a.appclient, a.informer, true),
		a.namespace,
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
		a.maintainConnection()
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

func (a *Agent) IsConnected() bool {
	return a.remote != nil && a.connected.Load()
}

func log() *logrus.Entry {
	return logrus.WithField("module", "Agent")
}
