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

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	appinformer "github.com/argoproj-labs/argocd-agent/internal/informer/application"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ backend.Application = &KubernetesBackend{}

type KubernetesBackend struct {
	appClient appclientset.Interface
	informer  *appinformer.AppInformer
	namespace string
	usePatch  bool
}

func NewKubernetesBackend(appClient appclientset.Interface, namespace string, informer *appinformer.AppInformer, usePatch bool) *KubernetesBackend {
	return &KubernetesBackend{
		appClient: appClient,
		informer:  informer,
		usePatch:  usePatch,
		namespace: namespace,
	}
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
	return be.appClient.ArgoprojV1alpha1().Applications(namespace).Get(ctx, name, v1.GetOptions{})
}

func (be *KubernetesBackend) Delete(ctx context.Context, name string, namespace string) error {
	return be.appClient.ArgoprojV1alpha1().Applications(namespace).Delete(ctx, name, v1.DeleteOptions{})
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

func (be *KubernetesBackend) StartInformer(ctx context.Context) {
	be.informer.Start(ctx.Done())
}
