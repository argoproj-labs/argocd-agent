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

/*
Package kubernetes implements an Application backend that uses a Kubernetes
informer to keep track of resources, and an appclientset to manipulate
Application resources on the cluster.
*/
package application

import (
	"context"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v3/pkg/client/clientset/versioned"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

var _ backend.Application = &KubernetesBackend{}

// KubernetesBackend is an implementation of the backend.Application interface, which is used by ApplicationManager to track/update the state of Argo CD Applications.
// KubernetesBackend stores/retrieves all data from Argo CD Application CRs on the cluster that is local to the agent/principal.
//
// KubernetesBackend is used by both the principal and agent components.
type KubernetesBackend struct {
	// appClient is used to interfact with Argo CD Application resources on the cluster on which agent/principal is installed.
	appClient appclientset.Interface
	// appInformer is used to watch for change events for Argo CD Application resources on the cluster
	appInformer informer.InformerInterface
	// appLister is used to list Argo CD Application resources from the cache
	appLister cache.GenericLister
	// namespace is not currently read, is not guaranteed to be non-empty, and is not guaranteed to contain the source of Argo CD Application CRs in all cases
	namespace string
	usePatch  bool
}

func NewKubernetesBackend(appClient appclientset.Interface, namespace string, appInformer informer.InformerInterface, usePatch bool) *KubernetesBackend {
	be := &KubernetesBackend{
		appClient:   appClient,
		appInformer: appInformer,
		usePatch:    usePatch,
		namespace:   namespace,
	}
	if specificInformer, ok := appInformer.(*informer.Informer[*v1alpha1.Application]); ok {
		be.appLister = specificInformer.Lister()
	}
	return be
}

func (be *KubernetesBackend) List(ctx context.Context, selector backend.ApplicationSelector) ([]v1alpha1.Application, error) {
	res := make([]v1alpha1.Application, 0)
	if len(selector.Namespaces) > 0 {
		for _, ns := range selector.Namespaces {
			l, err := be.appClient.ArgoprojV1alpha1().Applications(ns).List(ctx, v1.ListOptions{})
			if err != nil {
				return nil, err
			}
			res = append(res, l.Items...)
		}
	} else {
		l, err := be.appClient.ArgoprojV1alpha1().Applications("").List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, err
		}
		res = append(res, l.Items...)
	}
	return res, nil
}

func (be *KubernetesBackend) Create(ctx context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error) {
	return be.appClient.ArgoprojV1alpha1().Applications(app.Namespace).Create(ctx, app, v1.CreateOptions{FieldManager: "foo"})
}

func (be *KubernetesBackend) Get(ctx context.Context, name string, namespace string) (*v1alpha1.Application, error) {
	forUpdate, _ := ctx.Value(backend.ForUpdateContextKey).(bool)

	if !forUpdate && be.appLister != nil && be.appInformer.HasSynced() {
		namespaceLister := be.appLister.ByNamespace(namespace)
		if namespaceLister != nil {
			obj, err := namespaceLister.Get(name)
			if err != nil {
				return be.appClient.ArgoprojV1alpha1().Applications(namespace).Get(ctx, name, v1.GetOptions{})
			}
			app, ok := obj.(*v1alpha1.Application)
			if !ok {
				return nil, fmt.Errorf("object is not an Application: %T", obj)
			}
			return app.DeepCopy(), nil
		}
	}

	return be.appClient.ArgoprojV1alpha1().Applications(namespace).Get(ctx, name, v1.GetOptions{})
}

func (be *KubernetesBackend) Delete(ctx context.Context, name string, namespace string, deletionPropagation *backend.DeletionPropagation) error {

	// If nil, default to foreground
	k8sPropagationPolicy := v1.DeletePropagationForeground

	if deletionPropagation != nil {
		// Otherwise, directly translate constant from backend to k8s version

		switch *deletionPropagation {
		case backend.DeletePropagationForeground:
			k8sPropagationPolicy = v1.DeletePropagationForeground
		case backend.DeletePropagationBackground:
			k8sPropagationPolicy = v1.DeletePropagationBackground
		case backend.DeletePropagationOrphan:
			k8sPropagationPolicy = v1.DeletePropagationOrphan
		default:
			return fmt.Errorf("unexpected propagationPolicy value: '%v'", deletionPropagation)
		}
	}

	deleteOptions := v1.DeleteOptions{
		PropagationPolicy: &k8sPropagationPolicy,
	}

	return be.appClient.ArgoprojV1alpha1().Applications(namespace).Delete(ctx, name, deleteOptions)
}

func (be *KubernetesBackend) Update(ctx context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error) {
	return be.appClient.ArgoprojV1alpha1().Applications(app.Namespace).Update(ctx, app, v1.UpdateOptions{})
}

func (be *KubernetesBackend) Patch(ctx context.Context, name string, namespace string, patch []byte) (*v1alpha1.Application, error) {
	return be.appClient.ArgoprojV1alpha1().Applications(namespace).Patch(ctx, name, types.JSONPatchType, patch, v1.PatchOptions{})
}

func (be *KubernetesBackend) SupportsPatch() bool {
	return be.usePatch
}

func (be *KubernetesBackend) StartInformer(ctx context.Context) error {
	return be.appInformer.Start(ctx)
}

func (be *KubernetesBackend) EnsureSynced(timeout time.Duration) error {
	ctx, cancelFunc := context.WithTimeout(context.Background(), timeout)
	defer cancelFunc()
	return be.appInformer.WaitForSync(ctx)
}
