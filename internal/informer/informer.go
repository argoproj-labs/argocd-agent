package informer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

type GenericInformer struct {
	listFunc      ListFunc
	watchFunc     WatchFunc
	addFunc       AddFunc
	updateFunc    UpdateFunc
	deleteFunc    DeleteFunc
	filterFunc    FilterFunc
	synced        atomic.Bool
	running       atomic.Bool
	resyncPeriod  time.Duration
	informer      cache.SharedIndexInformer
	runch         chan struct{}
	logger        *logrus.Entry
	mutex         sync.RWMutex
	labelSelector string
	fieldSelector string
	namespaces    map[string]interface{}
}

type InformerOption func(i *GenericInformer) error

type ListFunc func(options v1.ListOptions, namespace string) (runtime.Object, error)
type WatchFunc func(options v1.ListOptions, namespace string) (watch.Interface, error)
type AddFunc func(obj interface{})
type UpdateFunc func(newObj interface{}, oldObj interface{})
type DeleteFunc func(obj interface{})
type FilterFunc func(obj interface{}) bool

func WithListCallback(f ListFunc) InformerOption {
	return func(i *GenericInformer) error {
		i.listFunc = f
		return nil
	}
}

func WithWatchCallback(f WatchFunc) InformerOption {
	return func(i *GenericInformer) error {
		i.watchFunc = f
		return nil
	}
}

func WithAddCallback(f AddFunc) InformerOption {
	return func(i *GenericInformer) error {
		i.addFunc = f
		return nil
	}
}

func WithUpdateCallback(f UpdateFunc) InformerOption {
	return func(i *GenericInformer) error {
		i.updateFunc = f
		return nil
	}
}

func WithDeleteCallback(f DeleteFunc) InformerOption {
	return func(i *GenericInformer) error {
		i.deleteFunc = f
		return nil
	}
}

func WithFilterFunc(f FilterFunc) InformerOption {
	return func(i *GenericInformer) error {
		i.filterFunc = f
		return nil
	}
}

func WithResyncPeriod(t time.Duration) InformerOption {
	return func(i *GenericInformer) error {
		i.resyncPeriod = t
		return nil
	}
}

func WithLabelSelector(sel string) InformerOption {
	return func(i *GenericInformer) error {
		i.labelSelector = sel
		return nil
	}
}

func WithFieldSelector(sel string) InformerOption {
	return func(i *GenericInformer) error {
		i.fieldSelector = sel
		return nil
	}
}

func WithNamespaces(namespaces ...string) InformerOption {
	return func(i *GenericInformer) error {
		for _, ns := range namespaces {
			i.namespaces[ns] = true
		}
		return nil
	}
}

func NewInformer(objType runtime.Object, options ...InformerOption) (*GenericInformer, error) {
	i := &GenericInformer{
		namespaces: make(map[string]interface{}),
	}
	i.logger = log().WithFields(logrus.Fields{
		"resource": fmt.Sprintf("%T", objType),
	})
	i.runch = make(chan struct{})
	i.SetSynced(false)
	for _, o := range options {
		err := o(i)
		if err != nil {
			return nil, err
		}
	}
	if i.listFunc == nil {
		return nil, fmt.Errorf("unable to create an informer without list function")
	}
	if i.watchFunc == nil {
		return nil, fmt.Errorf("unable to create an informer without watch function")
	}
	logCtx := i.logger
	i.informer = cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				logCtx.Trace("Executing list")
				return i.listFunc(options, i.watchAndListNamespace())
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				i.SetSynced(false)
				defer i.SetSynced(true)
				logCtx.Trace("Starting watcher")
				return i.watchFunc(options, i.watchAndListNamespace())
			},
		},
		objType,
		i.resyncPeriod,
		cache.Indexers{
			cache.NamespaceIndex: func(obj interface{}) ([]string, error) {
				return cache.MetaNamespaceIndexFunc(obj)
			},
		},
	)
	_, err := i.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			un, err := toUnstructured(obj)
			if err != nil {
				i.logger.WithError(err).Errorf("Could not convert type %T to unstructured", obj)
				return
			}
			if !i.isNamespaceAllowed(un) {
				i.logger.Tracef("Namespace %s not allowed", un.GetNamespace())
				return
			}
			if i.filterFunc != nil {
				if !i.filterFunc(obj) {
					return
				}
			}
			if i.addFunc != nil {
				i.addFunc(obj)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			un, err := toUnstructured(newObj)
			if err != nil {
				i.logger.WithError(err).Errorf("Could not convert type %T to unstructured", newObj)
				return
			}
			if !i.isNamespaceAllowed(un) {
				i.logger.Tracef("Namespace %s not allowed", un.GetNamespace())
				return
			}
			if i.filterFunc != nil {
				if !i.filterFunc(newObj) {
					return
				}
			}
			if i.updateFunc != nil {
				i.updateFunc(oldObj, newObj)
			}
		},
		DeleteFunc: func(obj interface{}) {
			un, err := toUnstructured(obj)
			if err != nil {
				i.logger.WithError(err).Errorf("Could not convert type %T to unstructured", obj)
				return
			}
			if !i.isNamespaceAllowed(un) {
				i.logger.Tracef("Namespace %s not allowed", un.GetNamespace())
				return
			}
			if i.filterFunc != nil {
				if !i.filterFunc(obj) {
					return
				}
			}
			if i.deleteFunc != nil {
				i.deleteFunc(obj)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	return i, nil
}

// IsSynced returns whether the informer is considered to be in sync or not
func (i *GenericInformer) IsSynced() bool {
	return i.synced.Load()
}

// SetSynced sets the sync state to either true or false
func (i *GenericInformer) SetSynced(synced bool) {
	i.logger.Tracef("Setting informer sync state to %v", synced)
	i.synced.Store(synced)
}

func (i *GenericInformer) IsRunning() bool {
	return i.running.Load()
}

func (i *GenericInformer) SetRunning(running bool) {
	i.logger.Tracef("Setting informer run state to %v", running)
	i.running.Store(running)
}

func (i *GenericInformer) Start(ctx context.Context) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.IsRunning() {
		return fmt.Errorf("cannot start informer: already running")
	}
	i.SetRunning(true)
	i.logger.Debug("Starting informer goroutine")
	go i.informer.Run(i.runch)
	return nil
}

func (i *GenericInformer) Stop() error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if !i.IsRunning() {
		return fmt.Errorf("cannot stop informer: informer not running")
	}
	i.logger.Debug("Stopping informer")
	close(i.runch)
	i.SetRunning(false)
	return nil
}

func (i *GenericInformer) AddNamespace(namespace string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if _, ok := i.namespaces[namespace]; ok {
		return fmt.Errorf("namespace %s already included", namespace)
	}
	i.namespaces[namespace] = true
	return nil
}

func (i *GenericInformer) RemoveNamespace(namespace string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if _, ok := i.namespaces[namespace]; !ok {
		return fmt.Errorf("namespace %s not included", namespace)
	}
	delete(i.namespaces, namespace)
	return nil
}

// watchAndListNamespace returns a string to be passed as namespace argument
// to the List() and Watch() functions of the AppProject client.
func (i *GenericInformer) watchAndListNamespace() string {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	if len(i.namespaces) == 1 {
		for k := range i.namespaces {
			return k
		}
	}
	// Zero or multiple namespaces mean we need cluster-scoped operations
	return ""
}

// isNamespaceAllowed returns whether the namespace of an event's object is
// permitted.
func (i *GenericInformer) isNamespaceAllowed(un unstructured.Unstructured) bool {
	if len(i.namespaces) == 0 {
		return true
	}
	_, ok := i.namespaces[un.GetNamespace()]
	return ok
}

// toUnstructured converts any resource implementing runtime.Object to an
// unstructured data type and returns it. If the resource could not be
// converted, returns the appropriate error.
func toUnstructured(obj interface{}) (unstructured.Unstructured, error) {
	var ext runtime.RawExtension
	var scope conversion.Scope
	o, ok := obj.(runtime.Object)
	if !ok {
		return unstructured.Unstructured{}, fmt.Errorf("failed type assertion to runtime.Object")
	}
	if err := runtime.Convert_runtime_Object_To_runtime_RawExtension(&o, &ext, scope); err != nil {
		return unstructured.Unstructured{}, err
	}
	if ro, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj); err != nil {
		return unstructured.Unstructured{}, err
	} else {
		uo := unstructured.Unstructured{Object: ro}
		return uo, nil
	}
}

func log() *logrus.Entry {
	return logrus.WithFields(logrus.Fields{
		"module": "Informer",
	})
}
