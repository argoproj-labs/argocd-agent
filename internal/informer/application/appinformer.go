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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	applisters "github.com/argoproj/argo-cd/v2/pkg/client/listers/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/glob"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/sirupsen/logrus"
)

const defaultResyncPeriod = 1 * time.Minute

// AppInformer is a filtering and customizable SharedIndexInformer for Argo CD
// Application resources in a cluster. It works across a configurable set of
// namespaces, lets you define a list of filters to indicate interest in the
// resource of a particular even and allows you to set up callbacks to handle
// the events.
type AppInformer struct {
	appClient appclientset.Interface
	options   *AppInformerOptions

	AppInformer cache.SharedIndexInformer
	AppLister   applisters.ApplicationLister

	lock sync.RWMutex

	// synced indicates whether the informer is synced and the watch is set up
	synced atomic.Bool
}

// NewAppInformer returns a new application informer for a given namespace
func NewAppInformer(ctx context.Context, client appclientset.Interface, namespace string, opts ...AppInformerOption) *AppInformer {
	o := &AppInformerOptions{
		resync: defaultResyncPeriod,
	}
	o.filters = filter.NewFilterChain()
	for _, opt := range opts {
		opt(o)
	}

	if len(o.namespaces) > 0 {
		o.namespaces = append(o.namespaces, namespace)
		o.namespace = ""
	} else {
		o.namespace = namespace
	}

	i := &AppInformer{options: o, appClient: client}

	i.AppInformer = cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				logCtx := log().WithField("component", "ListWatch")
				logCtx.Debugf("Listing apps into cache")
				appList, err := i.appClient.ArgoprojV1alpha1().Applications(o.namespace).List(ctx, options)
				if err != nil {
					logCtx.Warnf("Error listing apps: %v", err)
					return nil, err
				}

				// The result of the list call will get pre-filtered to only
				// contain apps that a) are in a namespace we are allowed to
				// process and b) pass admission through the informer's chain
				// of configured filters.
				preFilteredItems := make([]v1alpha1.Application, 0)
				for _, app := range appList.Items {
					if i.shouldProcessApp(&app) {
						preFilteredItems = append(preFilteredItems, app)
						logCtx.Tracef("Allowing app %s in namespace %s", app.Name, app.Namespace)
					} else {
						logCtx.Tracef("Not allowing app %s in namespace %s", app.Name, app.Namespace)
					}
				}

				// The pre-filtered list is passed to the configured callback
				// to perform custom filtering and tranformation.
				if i.options.listCb != nil {
					preFilteredItems = i.options.listCb(preFilteredItems)
				}
				appList.Items = preFilteredItems

				if i.options.appMetrics != nil {
					i.options.appMetrics.AppsWatched.Set(float64(len(appList.Items)))
				}
				logCtx.Tracef("Listed %d applications", len(appList.Items))
				return appList, err
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				i.synced.Store(false)
				logCtx := log().WithField("component", "WatchFunc")
				logCtx.Info("Starting application watcher")
				defer i.synced.Store(true)
				return i.appClient.ArgoprojV1alpha1().Applications(o.namespace).Watch(ctx, options)
			},
		},
		&v1alpha1.Application{},
		i.options.resync,
		cache.Indexers{
			cache.NamespaceIndex: func(obj interface{}) ([]string, error) {
				return cache.MetaNamespaceIndexFunc(obj)
			},
		},
	)
	_, _ = i.AppInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				app, ok := obj.(*v1alpha1.Application)
				if !ok || app == nil {
					// if i.options.errorCb != nil {
					// 	i.options.errorCb(nil, "invalid resource received by add event")
					// }
					return
				}
				logCtx := log().WithFields(logrus.Fields{
					"component":   "AddFunc",
					"application": app.QualifiedName(),
				})
				logCtx.Trace("New application event")
				if !i.shouldProcessApp(app) {
					return
				}
				if i.options.newCb != nil {
					i.options.newCb(app)
				}
				if i.options.appMetrics != nil {
					i.options.appMetrics.AppsAdded.Inc()
					i.options.appMetrics.AppsWatched.Inc()
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldApp, oldOk := oldObj.(*v1alpha1.Application)
				newApp, newOk := newObj.(*v1alpha1.Application)
				if !newOk || !oldOk {
					// if i.options.errorCb != nil {
					// 	i.options.errorCb(nil, "invalid resource received by update event")
					// }
					return
				}
				logCtx := log().WithFields(logrus.Fields{}) //("component", "UpdateFunc")
				logCtx.Tracef("Application update event")
				logCtx = logCtx.WithField("application", newApp.Name)

				// Namespace of new and old app must match. Theoretically, they
				// should always match, but we safeguard.
				if oldApp.Namespace != newApp.Namespace {
					logCtx.Warnf("namespace mismatch between old and new app")
					return
				}

				if !i.shouldProcessApp(newApp) {
					logCtx.Tracef("application not allowed")
					return
				}

				if !i.options.filters.ProcessChange(oldApp, newApp) {
					logCtx.Debugf("Change will not be processed")
					return
				}
				updateCb := i.UpdateAppCallback()
				if updateCb != nil {
					updateCb(oldApp, newApp)
				}
				if i.options.appMetrics != nil {
					i.options.appMetrics.AppsUpdated.Inc()
				}
			},
			DeleteFunc: func(obj interface{}) {
				logCtx := log().WithField("component", "DeleteFunc")
				logCtx.Tracef("Application update event")
				app, ok := obj.(*v1alpha1.Application)
				if !ok || app == nil {
					// if i.options.errorCb != nil {
					// 	i.options.errorCb(nil, "invalid resource received by delete event")
					// }
					return
				}
				logCtx = logCtx.WithField("application", app.QualifiedName())
				if !i.shouldProcessApp(app) {
					logCtx.Tracef("Ignoring application delete event")
					return
				}
				if i.options.deleteCb != nil {
					i.options.deleteCb(app)
				}
				if i.options.appMetrics != nil {
					i.options.appMetrics.AppsRemoved.Inc()
					i.options.appMetrics.AppsWatched.Dec()
				}
			},
		},
	)
	i.AppLister = applisters.NewApplicationLister(i.AppInformer.GetIndexer())
	// SetWatchErrorHandler only returns error when informer already started,
	// so it should be safe to not handle the error.
	_ = i.AppInformer.SetWatchErrorHandler(cache.DefaultWatchErrorHandler)
	return i
}

func (i *AppInformer) Start(stopch <-chan struct{}) {
	log().Infof("Starting app informer (namespaces: %s)", strings.Join(append([]string{i.options.namespace}, i.options.namespaces...), ","))
	i.AppInformer.Run(stopch)
	log().Infof("App informer has shutdown")
}

// shouldProcessApp returns true if the app is allowed to be processed
func (i *AppInformer) shouldProcessApp(app *v1alpha1.Application) bool {
	return glob.MatchStringInList(append([]string{i.options.namespace}, i.options.namespaces...), app.Namespace, false) &&
		i.options.filters.Admit(app)
}

// EnsureSynced waits until either the AppInformer has fully synced or the
// timeout has been reached. In the latter case, an error will be returned.
// Note that this call blocks until either situation arises.
func (i *AppInformer) EnsureSynced(d time.Duration) error {
	tckr := time.NewTicker(d)
	for {
		select {
		case <-tckr.C:
			return fmt.Errorf("sync timeout reached")
		default:
			if i.synced.Load() {
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
