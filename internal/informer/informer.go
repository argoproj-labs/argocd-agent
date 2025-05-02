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
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

// Informer is a generic informer, suitable for all Kubernetes types.
//
// T specifies the type of the resource this informer should handle.
//
// The informer must be supplied with at least a lister and a watcher
// function.
type Informer[T runtime.Object] struct {
	informer cache.SharedIndexInformer

	listFunc  ListFunc
	watchFunc WatchFunc

	resyncPeriod time.Duration

	// onAdd is a function that is called when a resource is added
	onAdd AddHandler[T]
	// onUpdate is a function that is called when a resource is updated
	onUpdate UpdateHandler[T]
	// onDelete is a function that is called when a resource is deleted
	onDelete DeleteHandler[T]

	evHandler cache.ResourceEventHandlerRegistration

	resType reflect.Type

	// logger is this informer's logger.
	logger *logrus.Entry

	// ctlCh is a control channel for the underlying shared informer
	ctlCh chan struct{}

	// running indicates whether the informer is currently running
	running atomic.Bool

	// mutex is a mutex for parallel operations
	mutex sync.RWMutex

	// namespace is non-empty if the informer is in namespaced-scope
	namespace string

	// metrics holds informer metrics
	metrics *metrics.InformerMetrics

	// filters is a filter chain to be used by this informer
	filters *filter.Chain[T]
}

// InformerInterface defines the interface for the informer
type InformerInterface interface {
	Start(ctx context.Context) error
	Stop() error
	HasSynced() bool
	WaitForSync(ctx context.Context) error
}

var _ InformerInterface = &Informer[runtime.Object]{}

type AddHandler[Res runtime.Object] func(obj Res)
type UpdateHandler[Res runtime.Object] func(old Res, new Res)
type DeleteHandler[Res runtime.Object] func(obj Res)

var ErrRunning = errors.New("informer is running")
var ErrNotRunning = errors.New("informer is not running")
var ErrNoListFunc = errors.New("no list func defined")
var ErrNoWatchFunc = errors.New("no watch func defined")

// NewInformer instantiates a new informer for resource type T.
//
// You must supply at least a list and a watch function using WithLisHandler and
// WithWatchHandler.
//
// Resource callbacks can be provided at the time of instantiation
// using WithAddHandler, WithUpdateHandler and WithDeleteHandler options.
func NewInformer[T runtime.Object](ctx context.Context, opts ...InformerOption[T]) (*Informer[T], error) {
	i := &Informer[T]{}
	var r T
	i.resType = reflect.TypeOf(r)
	i.logger = logrus.NewEntry(logrus.StandardLogger()).WithFields(logrus.Fields{
		"type":   i.resType,
		"module": "Informer",
	})
	for _, o := range opts {
		err := o(i)
		if err != nil {
			return nil, fmt.Errorf("could not set option: %w", err)
		}
	}
	// Both, listFunc and watchFunc need to be set
	if i.listFunc == nil {
		return nil, ErrNoListFunc
	}
	if i.watchFunc == nil {
		return nil, ErrNoWatchFunc
	}
	i.createSharedInformer(ctx)
	if err := i.installEventHandlers(); err != nil {
		return nil, err
	}
	i.ctlCh = make(chan struct{})
	return i, nil
}

// createSharedInformer creates the underlying shared index informer for the
// Informer i.
func (i *Informer[T]) createSharedInformer(ctx context.Context) {
	var r T
	i.informer = cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if i.listFunc == nil {
					panic("no list func defined")
				}
				i.logger.Trace("Starting to list resources")
				objs, err := i.listFunc(ctx, options)
				if err != nil {
					i.logger.WithError(err).Error("Could not list resources")
				}
				i.logger.Trace("Done listing resources")
				return objs, err
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if i.watchFunc == nil {
					panic("no watch func defined")
				}
				i.logger.Info("Starting watcher")
				wif, err := i.watchFunc(ctx, options)
				if err != nil {
					i.logger.WithError(err).Error("Could not start watcher")
				}
				i.logger.Trace("Done starting watcher")
				return wif, err
			},
		},
		r,
		i.resyncPeriod,
		cache.Indexers{
			cache.NamespaceIndex: func(obj any) ([]string, error) {
				return cache.MetaNamespaceIndexFunc(obj)
			},
		},
	)
}

// installEventHandlers installs any event handlers for the underlying shared
// index informer for Informer i.
func (i *Informer[T]) installEventHandlers() error {
	var err error
	i.evHandler, err = i.informer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj any) bool {
			if i.filters == nil {
				return true
			}
			res, ok := obj.(T)
			if !ok {
				i.logger.Errorf("Could not type convert %T to %s", obj, i.resType)
				return false
			}
			return i.filters.Admit(res)
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				res, ok := obj.(T)
				if !ok {
					return
				}
				if i.onAdd != nil {
					i.onAdd(res)
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldRes, oldOk := oldObj.(T)
				newRes, newOk := newObj.(T)
				if !oldOk || !newOk {
					return
				}
				if i.onUpdate != nil {
					i.onUpdate(oldRes, newRes)
				}
			},
			DeleteFunc: func(obj interface{}) {
				res, ok := obj.(T)
				if !ok {
					return
				}
				if i.onDelete != nil {
					i.onDelete(res)
				}
			},
		},
	})
	if err != nil {
		return err
	}
	return nil
}

// Start starts the informer until the informer's control channel is closed.
// Note that this method will block, so it should be executed in a dedicated
// go routine.
func (i *Informer[T]) Start(ctx context.Context) error {
	// We can't use defer to unlock the mutex because the underlying informer's
	// Start function does not return until the control channel is closed.
	// Make sure that the mutex is released before exiting the function.
	i.mutex.Lock()
	i.logger.Debug("Starting informer")
	if i.running.Load() {
		i.mutex.Unlock()
		return ErrRunning
	}

	// We need to make a new channel, as the previous might be closed already
	i.ctlCh = make(chan struct{})

	i.running.Store(true)
	i.mutex.Unlock()

	// Unlock the mutex as we hand over control to the underlying informer
	// As soon as the informer exits, we acquire the mutex again.
	i.informer.Run(i.ctlCh)
	i.running.Store(false)
	return nil
}

// Stop stops the running informer
func (i *Informer[T]) Stop() error {
	i.logger.Infof("Stopping informer")
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if !i.running.Load() {
		return ErrNotRunning
	}
	close(i.ctlCh)
	i.running.Store(false)
	return nil
}

// HasSynced returns true if the informer is synced
func (i *Informer[T]) HasSynced() bool {
	return i.evHandler.HasSynced()
}

// WaitForSync blocks until either the informer has synced, or the context ctx
// is done. If the informer has synced, returns nil. Otherwise, the reason why
// the context was aborted is returned.
func (i *Informer[T]) WaitForSync(ctx context.Context) error {
	for !i.HasSynced() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
	return nil
}
