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

package applicationset

import (
	"context"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v3/pkg/client/clientset/versioned"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ backend.ApplicationSet = &KubernetesBackend{}

// KubernetesBackend implements backend.ApplicationSet using the Argo CD client.
type KubernetesBackend struct {
	appClient appclientset.Interface
	informer  informer.InformerInterface
	namespace string
}

func NewKubernetesBackend(appClient appclientset.Interface, namespace string, inf informer.InformerInterface) *KubernetesBackend {
	return &KubernetesBackend{
		appClient: appClient,
		informer:  inf,
		namespace: namespace,
	}
}

func (be *KubernetesBackend) List(ctx context.Context, namespace string) ([]v1alpha1.ApplicationSet, error) {
	l, err := be.appClient.ArgoprojV1alpha1().ApplicationSets(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}

func (be *KubernetesBackend) Create(ctx context.Context, appSet *v1alpha1.ApplicationSet) (*v1alpha1.ApplicationSet, error) {
	return be.appClient.ArgoprojV1alpha1().ApplicationSets(appSet.Namespace).Create(ctx, appSet, v1.CreateOptions{FieldManager: "argocd-agent"})
}

func (be *KubernetesBackend) Get(ctx context.Context, name string, namespace string) (*v1alpha1.ApplicationSet, error) {
	return be.appClient.ArgoprojV1alpha1().ApplicationSets(namespace).Get(ctx, name, v1.GetOptions{})
}

func (be *KubernetesBackend) Delete(ctx context.Context, name string, namespace string, deletionPropagation *backend.DeletionPropagation) error {
	k8sPropagationPolicy := v1.DeletePropagationForeground

	if deletionPropagation != nil {
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

	return be.appClient.ArgoprojV1alpha1().ApplicationSets(namespace).Delete(ctx, name, v1.DeleteOptions{
		PropagationPolicy: &k8sPropagationPolicy,
	})
}

func (be *KubernetesBackend) Update(ctx context.Context, appSet *v1alpha1.ApplicationSet) (*v1alpha1.ApplicationSet, error) {
	return be.appClient.ArgoprojV1alpha1().ApplicationSets(appSet.Namespace).Update(ctx, appSet, v1.UpdateOptions{})
}

func (be *KubernetesBackend) StartInformer(ctx context.Context) error {
	if be.informer == nil {
		return nil
	}
	return be.informer.Start(ctx)
}

func (be *KubernetesBackend) EnsureSynced(timeout time.Duration) error {
	if be.informer == nil {
		return nil
	}
	ctx, cancelFunc := context.WithTimeout(context.Background(), timeout)
	defer cancelFunc()
	return be.informer.WaitForSync(ctx)
}
