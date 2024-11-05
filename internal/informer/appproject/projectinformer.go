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

package appproject

import (
	"context"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	applisters "github.com/argoproj/argo-cd/v2/pkg/client/listers/application/v1alpha1"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
)

type AppProjectInformer struct {
	filterFunc func(proj *v1alpha1.AppProject) bool
	namespace  string
	logger     *logrus.Entry

	projectInformer *informer.GenericInformer
	projectLister   applisters.AppProjectLister

	addFunc    func(proj *v1alpha1.AppProject)
	updateFunc func(oldProj *v1alpha1.AppProject, newProj *v1alpha1.AppProject)
	deleteFunc func(proj *v1alpha1.AppProject)

	metrics *metrics.AppProjectWatcherMetrics
}

type AppProjectInformerOption func(pi *AppProjectInformer) error

// WithListFilter sets a filter function for the add, update and delete events.
// This function will be called for each event, and if it returns false, the
// event's execution will not continue.
func WithListFilter(f func(proj *v1alpha1.AppProject) bool) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.filterFunc = f
		return nil
	}
}

// WithAddFunc sets the function to be called when an AppProject is created
// on the cluster.
func WithAddFunc(f func(proj *v1alpha1.AppProject)) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.addFunc = f
		return nil
	}
}

// WithUpdateFunc sets the function to be called when an AppProject is updated
// on the cluster.
func WithUpdateFunc(f func(oldProj *v1alpha1.AppProject, newProj *v1alpha1.AppProject)) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.updateFunc = f
		return nil
	}
}

// WithDeleteFunc sets the function to be called when an AppProject is deleted
// on the cluster.
func WithDeleteFunc(f func(proj *v1alpha1.AppProject)) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.deleteFunc = f
		return nil
	}
}

// WithLogger sets the logger to use with this AppProjectInformer
func WithLogger(l *logrus.Entry) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.logger = l
		return nil
	}
}

// WithMetrics sets the AppProject watcher metrics to be used with this
// informer.
func WithMetrics(m *metrics.AppProjectWatcherMetrics) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.metrics = m
		return nil
	}
}

// NewAppProjectInformer returns a new instance of a GenericInformer set up to
// handle AppProjects. It will be configured with the given options, using the
// given appclientset.
func NewAppProjectInformer(ctx context.Context, client appclientset.Interface, namespace string, options ...AppProjectInformerOption) (*AppProjectInformer, error) {
	pi := &AppProjectInformer{
		namespace: namespace,
	}
	for _, o := range options {
		err := o(pi)
		if err != nil {
			return nil, err
		}
	}
	if pi.logger == nil {
		pi.logger = logrus.WithField("module", "AppProjectInformer")
	}
	iopts := []informer.InformerOption{}
	if pi.metrics != nil {
		iopts = append(iopts, informer.WithMetrics(pi.metrics.ProjectsAdded, pi.metrics.ProjectsUpdated, pi.metrics.ProjectsRemoved, pi.metrics.ProjectsWatched))
	}
	i, err := informer.NewGenericInformer(&v1alpha1.AppProject{},
		append([]informer.InformerOption{
			informer.WithListCallback(func(options v1.ListOptions, namespace string) (runtime.Object, error) {
				log().Infof("Listing AppProjects in namespace %s", namespace)
				projects, err := client.ArgoprojV1alpha1().AppProjects(namespace).List(ctx, options)
				log().Infof("Lister returned %d AppProjects", len(projects.Items))
				if pi.filterFunc != nil {
					newItems := make([]v1alpha1.AppProject, 0)
					for _, p := range projects.Items {
						if pi.filterFunc(&p) {
							newItems = append(newItems, p)
						}
					}
					pi.logger.Debugf("Lister has %d AppProjects after filtering", len(newItems))
					projects.Items = newItems
				}
				return projects, err
			}),
			informer.WithNamespaces(pi.namespace),
			informer.WithWatchCallback(func(options v1.ListOptions, namespace string) (watch.Interface, error) {
				log().Info("Watching AppProjects")
				return client.ArgoprojV1alpha1().AppProjects(namespace).Watch(ctx, options)
			}),
			informer.WithAddCallback(func(obj interface{}) {
				log().Info("Add AppProject Callback")
				proj, ok := obj.(*v1alpha1.AppProject)
				if !ok {
					pi.logger.Errorf("Received add event for unknown type %T", obj)
					return
				}
				pi.logger.Debugf("AppProject add event: %s", proj.Name)
				if pi.addFunc != nil {
					pi.addFunc(proj)
				}
			}),
			informer.WithUpdateCallback(func(oldObj, newObj interface{}) {
				log().Info("Update AppProject Callback")
				oldProj, oldProjOk := oldObj.(*v1alpha1.AppProject)
				newProj, newProjOk := newObj.(*v1alpha1.AppProject)
				if !newProjOk || !oldProjOk {
					pi.logger.Errorf("Received update event for unknown type old:%T new:%T", oldObj, newObj)
					return
				}
				pi.logger.Debugf("AppProject update event: old:%s new:%s", oldProj.Name, newProj.Name)
				if pi.updateFunc != nil {
					pi.updateFunc(oldProj, newProj)
				}
			}),
			informer.WithDeleteCallback(func(obj interface{}) {
				log().Info("Delete AppProject Callback")
				proj, ok := obj.(*v1alpha1.AppProject)
				if !ok {
					pi.logger.Errorf("Received delete event for unknown type %T", obj)
					return
				}
				pi.logger.Debugf("AppProject delete event: %s", proj.Name)
				if pi.deleteFunc != nil {
					pi.deleteFunc(proj)
				}
			}),
			informer.WithFilterFunc(func(obj interface{}) bool {
				if pi.filterFunc == nil {
					return true
				}
				o, ok := obj.(*v1alpha1.AppProject)
				if !ok {
					pi.logger.Errorf("Failed type conversion for unknown type %T", obj)
					return false
				}
				return pi.filterFunc(o)
			}),
		}, iopts...)...)
	pi.projectInformer = i
	pi.projectLister = applisters.NewAppProjectLister(i.Indexer())
	return pi, err
}

func (i *AppProjectInformer) Start(ctx context.Context) {
	log().Infof("Starting app project informer (namespace: %s)", i.namespace)
	err := i.projectInformer.Start(ctx)
	if err != nil {
		log().Errorf("Failed to start app project informer: %v", err)
		return
	}
}

func log() *logrus.Entry {
	return logrus.WithField("module", "AppProjectInformer")
}

func init() {
}
