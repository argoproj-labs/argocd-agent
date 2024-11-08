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

package application

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	applisters "github.com/argoproj/argo-cd/v2/pkg/client/listers/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/glob"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/sirupsen/logrus"
)

const defaultResyncPeriod = 1 * time.Minute

// AppInformer is a filtering and customizable SharedIndexInformer for Argo CD
// Application resources in a cluster. It works across a configurable set of
// namespaces, lets you define a list of filters to indicate interest in the
// resource of a particular event and allows you to set up callbacks to handle
// the events.
type AppInformer struct {
	appClient appclientset.Interface
	// options   *AppInformerOptions

	appInformer *informer.GenericInformer
	appLister   applisters.ApplicationLister

	// The logger for this informer
	logger *logrus.Entry

	// namespaces is a list of namespaces this informer is allowed to process
	// resources in.
	namespaces []string

	metrics *metrics.ApplicationWatcherMetrics

	// Callback functions for the events of the informer
	addFunc    func(proj *v1alpha1.Application)
	updateFunc func(oldProj *v1alpha1.Application, newProj *v1alpha1.Application)
	deleteFunc func(proj *v1alpha1.Application)

	filterFunc func(proj *v1alpha1.Application) bool

	filters *filter.Chain

	// lock should be acquired when reading/writing from callbacks defined in 'options' field
	lock sync.RWMutex

	// synced indicates whether the informer is synced and the watch is set up
	synced atomic.Bool

	// clusterScope indicates the scope of the AppInformer
	clusterScope bool
}

// NewAppInformer returns a new application informer for a given namespace
func NewAppInformer(ctx context.Context, client appclientset.Interface, namespace string, opts ...AppInformerOption) (*AppInformer, error) {
	ai := &AppInformer{
		clusterScope: false,
		filters:      filter.NewFilterChain(),
	}

	for _, opt := range opts {
		opt(ai)
	}

	if ai.logger == nil {
		ai.logger = logrus.WithField("module", "ApplicationInformer")
	}

	iopts := []informer.InformerOption{}
	if ai.metrics != nil {
		iopts = append(iopts, informer.WithMetrics(ai.metrics.AppsAdded, ai.metrics.AppsUpdated, ai.metrics.AppsRemoved, ai.metrics.AppsWatched))
	}

	i, err := informer.NewGenericInformer(&v1alpha1.Application{},
		append([]informer.InformerOption{
			informer.WithListCallback(func(options v1.ListOptions, namespace string) (runtime.Object, error) {
				log().WithField("namespace", namespace).Infof("Listing Applications")
				apps, err := client.ArgoprojV1alpha1().Applications(namespace).List(ctx, options)
				log().Infof("List call returned %d Applications", len(apps.Items))
				if ai.filterFunc != nil {
					newItems := make([]v1alpha1.Application, 0)
					for _, a := range apps.Items {
						if ai.filterFunc(&a) {
							newItems = append(newItems, a)
						}
					}
					ai.logger.Debugf("Listed %d Applications after filtering", len(newItems))
					apps.Items = newItems
				}
				return apps, err
			}),
			informer.WithNamespaces(ai.namespaces...),
			informer.WithWatchCallback(func(options v1.ListOptions, namespace string) (watch.Interface, error) {
				log().Info("Watching Applications")
				ai.synced.Store(false)
				logCtx := log().WithField("component", "WatchFunc")
				logCtx.Info("Starting application watcher")
				defer ai.synced.Store(true)
				return client.ArgoprojV1alpha1().Applications(namespace).Watch(ctx, options)
			}),
			informer.WithAddCallback(func(obj interface{}) {
				log().Info("Add Applications Callback")
				app, ok := obj.(*v1alpha1.Application)
				if !ok {
					ai.logger.Errorf("Received add event for unknown type %T", obj)
					return
				}
				ai.logger.Debugf("Application add event: %s", app.Name)
				if ai.addFunc != nil {
					ai.addFunc(app)
				}
			}),
			informer.WithUpdateCallback(func(oldObj, newObj interface{}) {
				log().Info("Update Applications Callback")
				oldApp, oldAppOk := oldObj.(*v1alpha1.Application)
				newApp, newAppOk := newObj.(*v1alpha1.Application)
				if !newAppOk || !oldAppOk {
					ai.logger.Errorf("Received update event for unknown type old:%T new:%T", oldObj, newObj)
					return
				}
				ai.logger.Debugf("Application update event: old:%s new:%s", oldApp.Name, newApp.Name)
				if ai.updateFunc != nil {
					ai.updateFunc(oldApp, newApp)
				}
			}),
			informer.WithDeleteCallback(func(obj interface{}) {
				log().Info("Delete AppProject Callback")
				app, ok := obj.(*v1alpha1.Application)
				if !ok {
					ai.logger.Errorf("Received delete event for unknown type %T", obj)
					return
				}
				ai.logger.Debugf("AppProject delete event: %s", app.Name)
				if ai.deleteFunc != nil {
					ai.deleteFunc(app)
				}
			}),
			informer.WithFilterFunc(func(obj interface{}) bool {
				if ai.filterFunc == nil {
					return true
				}
				app, ok := obj.(*v1alpha1.Application)
				if !ok {
					ai.logger.Errorf("Failed type conversion for unknown type %T", obj)
					return false
				}
				return ai.filterFunc(app)
			}),
		}, iopts...)...)

	if err != nil {
		return nil, err
	}
	ai.appInformer = i
	ai.appLister = applisters.NewApplicationLister(ai.appInformer.Indexer())
	return ai, nil
}

func (i *AppInformer) Start(ctx context.Context) {
	scope := "namespace"
	if len(i.namespaces) > 1 {
		scope = "cluster"
	}
	log().Infof("Starting Application informer (scope: %s)", scope)
	if err := i.appInformer.Start(ctx); err != nil {
		i.logger.WithError(err).Error("Failed to start Application informer")
		ctx.Done()
	}
}

func (ai *AppInformer) Stop() error {
	ai.logger.Infof("Stopping Application informer")
	if err := ai.appInformer.Stop(); err != nil {
		return err
	}
	return nil
}

// shouldProcessApp returns true if the app is allowed to be processed
func (ai *AppInformer) shouldProcessApp(app *v1alpha1.Application) bool {
	return glob.MatchStringInList(ai.namespaces, app.Namespace, glob.REGEXP) &&
		ai.filters.Admit(app)
}

// EnsureSynced waits until either the AppInformer has fully synced or the
// timeout has been reached. In the latter case, an error will be returned.
// Note that this call blocks until either situation arises.
func (ai *AppInformer) EnsureSynced(d time.Duration) error {
	tckr := time.NewTicker(d)
	for {
		select {
		case <-tckr.C:
			return fmt.Errorf("sync timeout reached")
		default:
			if ai.synced.Load() {
				return nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func log() *logrus.Entry {
	return logrus.WithField("module", "AppInformer")
}

func init() {
}
