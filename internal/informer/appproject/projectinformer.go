package appproject

import (
	"context"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/jannfis/argocd-agent/internal/informer"
)

type AppProjectInformer struct {
	filterFunc func(proj *v1alpha1.AppProject) bool
	namespaces []string
	logger     *logrus.Entry

	addFunc    func(proj *v1alpha1.AppProject)
	updateFunc func(oldProj *v1alpha1.AppProject, newProj *v1alpha1.AppProject)
	deleteFunc func(proj *v1alpha1.AppProject)
}

type AppProjectInformerOption func(pi *AppProjectInformer) error

func WithNamespaces(namespaces ...string) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.namespaces = namespaces
		return nil
	}
}

func WithListFilter(f func(proj *v1alpha1.AppProject) bool) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.filterFunc = f
		return nil
	}
}

func WithAddFunc(f func(proj *v1alpha1.AppProject)) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.addFunc = f
		return nil
	}
}

func WithUpdateFunc(f func(oldProj *v1alpha1.AppProject, newProj *v1alpha1.AppProject)) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.updateFunc = f
		return nil
	}
}

func WithDeleteFunc(f func(proj *v1alpha1.AppProject)) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.deleteFunc = f
		return nil
	}
}

func WithLogger(l *logrus.Entry) AppProjectInformerOption {
	return func(pi *AppProjectInformer) error {
		pi.logger = l
		return nil
	}
}

func NewAppProjectInformer(ctx context.Context, client appclientset.Interface, options ...AppProjectInformerOption) (*informer.GenericInformer, error) {
	pi := &AppProjectInformer{
		namespaces: make([]string, 0),
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
	i, err := informer.NewInformer(&v1alpha1.AppProject{},
		informer.WithListCallback(func(options v1.ListOptions, namespace string) (runtime.Object, error) {
			projects, err := client.ArgoprojV1alpha1().AppProjects(namespace).List(ctx, options)
			pi.logger.Debugf("Lister returned %d AppProjects", len(projects.Items))
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
		informer.WithWatchCallback(func(options v1.ListOptions, namespace string) (watch.Interface, error) {
			return client.ArgoprojV1alpha1().AppProjects(namespace).Watch(ctx, options)
		}),
		informer.WithAddCallback(func(obj interface{}) {
			proj, ok := obj.(*v1alpha1.AppProject)
			if !ok {
				pi.logger.Errorf("Received add event for unknown type %T", obj)
				return
			}
			if pi.filterFunc != nil {
				if !pi.filterFunc(proj) {
					return
				}
			}
			pi.logger.Debugf("AppProject add event: %s", proj.Name)
			if pi.addFunc != nil {
				pi.addFunc(proj)
			}
		}),
		informer.WithUpdateCallback(func(oldObj, newObj interface{}) {
			oldProj, oldProjOk := oldObj.(*v1alpha1.AppProject)
			newProj, newProjOk := newObj.(*v1alpha1.AppProject)
			if !newProjOk || !oldProjOk {
				pi.logger.Errorf("Received update event for unknown type old:%T new:%T", oldObj, newObj)
				return
			}
			if pi.filterFunc != nil {
				if !pi.filterFunc(newProj) {
					return
				}
			}
			pi.logger.Debugf("AppProject update event: old:%s new:%s", oldProj.Name, newProj.Name)
			if pi.updateFunc != nil {
				pi.updateFunc(oldProj, newProj)
			}
		}),
		informer.WithDeleteCallback(func(obj interface{}) {
			proj, ok := obj.(*v1alpha1.AppProject)
			if !ok {
				pi.logger.Errorf("Received delete event for unknown type %T", obj)
				return
			}
			if pi.filterFunc != nil {
				if !pi.filterFunc(proj) {
					return
				}
			}
			pi.logger.Debugf("AppProject delete event: %s", proj.Name)
			if pi.deleteFunc != nil {
				pi.deleteFunc(proj)
			}
		}),
	)
	return i, err
}
