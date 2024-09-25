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

package informer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

type GenericInformer struct {
	listFunc     ListFunc
	watchFunc    WatchFunc
	addFunc      AddFunc
	updateFunc   UpdateFunc
	deleteFunc   DeleteFunc
	filterFunc   FilterFunc
	synced       atomic.Bool
	running      atomic.Bool
	resyncPeriod time.Duration
	informer     cache.SharedIndexInformer
	runch        chan struct{}
	logger       *logrus.Entry
	// mutex prevents unsynchronized access to namespaces, and ensures that Start/Stop logic is only called once.
	mutex sync.RWMutex
	// labelSelector is not currently implemented
	labelSelector string
	// fieldSelector is not currently implemented
	fieldSelector string
	// mutex should be owned before accessing namespaces
	namespaces map[string]interface{}
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

// WithNamespaces sets the namespaces for which the informer will process any
// event. If an event is seen for an object in a namespace that is not in this
// list, the event will be ignored. If either zero or multiple namespaces are
// set, the informer will require cluster-wide permissions to list and watch
// the kind of resource to be handled by this informer.
func WithNamespaces(namespaces ...string) InformerOption {
	return func(i *GenericInformer) error {
		for _, ns := range namespaces {
			i.namespaces[ns] = true
		}
		return nil
	}
}

// NewGenericInformer returns a new instance of a GenericInformer for the given
// object type and with the given objects.
func NewGenericInformer(objType runtime.Object, options ...InformerOption) (*GenericInformer, error) {
	i := &GenericInformer{
		namespaces: make(map[string]interface{}),
	}
	i.logger = log().WithFields(logrus.Fields{
		"resource": fmt.Sprintf("%T", objType),
	})
	i.runch = make(chan struct{})
	i.setSynced(false)
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
				i.setSynced(false)
				defer i.setSynced(true)
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
			mobj, err := meta.Accessor(obj)
			if err != nil {
				i.logger.WithError(err).Errorf("Could not convert type %T to unstructured", obj)
				return
			}
			if !i.isNamespaceAllowed(mobj) {
				i.logger.Tracef("Namespace %s not allowed", mobj.GetNamespace())
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
			mobj, err := meta.Accessor(newObj)
			if err != nil {
				i.logger.WithError(err).Errorf("Could not convert type %T to unstructured", newObj)
				return
			}
			if !i.isNamespaceAllowed(mobj) {
				i.logger.Tracef("Namespace %s not allowed", mobj.GetNamespace())
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
			mobj, err := meta.Accessor(obj)
			if err != nil {
				i.logger.WithError(err).Errorf("Could not convert type %T to unstructured", obj)
				return
			}
			if !i.isNamespaceAllowed(mobj) {
				i.logger.Tracef("Namespace %s not allowed", mobj.GetNamespace())
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
	// SetWatchErrorHandler only returns error when informer already started,
	// so it should be safe to not handle the error.
	// TODO: Do we need a unique error handler?
	_ = i.informer.SetWatchErrorHandler(cache.DefaultWatchErrorHandler)
	return i, nil
}

// IsSynced returns whether the informer is considered to be in sync or not
func (i *GenericInformer) IsSynced() bool {
	return i.synced.Load()
}

// setSynced sets the sync state to either true or false
func (i *GenericInformer) setSynced(synced bool) {
	i.logger.Tracef("Setting informer sync state to %v", synced)
	i.synced.Store(synced)
}

// IsRunning returns whether the GenericInformer is running or not
func (i *GenericInformer) IsRunning() bool {
	return i.running.Load()
}

// setRunning sets whether this informer is running to either true or false
func (i *GenericInformer) setRunning(running bool) {
	i.logger.Tracef("Setting informer run state to %v", running)
	i.running.Store(running)
}

// Start starts the GenericInformer with its current options in a goroutine.
// If this method does not return an error, you can use Stop to stop the
// informer and terminate the goroutine it has created. If Start returns an
// error, no goroutine will have been created.
func (i *GenericInformer) Start(ctx context.Context) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.IsRunning() {
		return fmt.Errorf("cannot start informer: already running")
	}
	i.setRunning(true)
	i.logger.Info("Starting informer goroutine")
	go i.informer.Run(i.runch)
	return nil
}

// Stop stops the GenericInformer and terminates the associated goroutine.
func (i *GenericInformer) Stop() error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if !i.IsRunning() {
		return fmt.Errorf("cannot stop informer: informer not running")
	}
	i.logger.Debug("Stopping informer")
	close(i.runch)
	i.setRunning(false)
	return nil
}

// AddNamespace adds a namespace to the list of allowed namespaces.
func (i *GenericInformer) AddNamespace(namespace string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if _, ok := i.namespaces[namespace]; ok {
		return fmt.Errorf("namespace %s already included", namespace)
	}
	i.namespaces[namespace] = true
	return nil
}

// RemoveNamespace removes a namespace from the list of allowed namespaces
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
func (i *GenericInformer) isNamespaceAllowed(obj v1.Object) bool {
	if len(i.namespaces) == 0 {
		return true
	}
	_, ok := i.namespaces[obj.GetNamespace()]
	return ok
}

// Indexer returns the underlying shared informer's indexer
func (i *GenericInformer) Indexer() cache.Indexer {
	return i.informer.GetIndexer()
}

func log() *logrus.Entry {
	return logrus.WithFields(logrus.Fields{
		"module": "Informer",
	})
}
