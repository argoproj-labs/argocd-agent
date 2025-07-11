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
Package kubernetes implements an AppProject backend that uses a Kubernetes
informer to keep track of resources, and an appclientset to manipulate
AppProject resources on the cluster.
*/
package appproject

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
)

var _ backend.AppProject = &KubernetesBackend{}

// KubernetesBackend is an implementation of the backend.AppProject interface, which is used by AppProjectManager to track/update the state of Argo CD AppProjects.
// KubernetesBackend stores/retrieves all data from Argo CD AppProject CRs on the cluster that is local to the agent/principal.
//
// KubernetesBackend is used by both the principal and agent components.
type KubernetesBackend struct {
	// appClient is used to interfact with Argo CD AppProject resources on the cluster on which agent/principal is installed.
	appClient appclientset.Interface
	// appProjectInformer is used to watch for change events for Argo CD AppProject resources on the cluster
	appProjectInformer informer.InformerInterface
	// namespace to contain the source of Argo CD AppProject CRs in all cases, mainly used by agents
	namespace string
	usePatch  bool
}

func NewKubernetesBackend(appClient appclientset.Interface, namespace string, appProjectInformer informer.InformerInterface, usePatch bool) *KubernetesBackend {
	return &KubernetesBackend{
		appClient:          appClient,
		appProjectInformer: appProjectInformer,
		namespace:          namespace,
		usePatch:           usePatch,
	}
}

func (be *KubernetesBackend) List(ctx context.Context, selector backend.AppProjectSelector) ([]v1alpha1.AppProject, error) {

	l, err := be.appClient.ArgoprojV1alpha1().AppProjects(be.namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return l.Items, nil
}

func (be *KubernetesBackend) Create(ctx context.Context, project *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
	return be.appClient.ArgoprojV1alpha1().AppProjects(project.Namespace).Create(ctx, project, v1.CreateOptions{FieldManager: "foo"})
}

func (be *KubernetesBackend) Get(ctx context.Context, name string, namespace string) (*v1alpha1.AppProject, error) {
	return be.appClient.ArgoprojV1alpha1().AppProjects(namespace).Get(ctx, name, v1.GetOptions{})
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

	return be.appClient.ArgoprojV1alpha1().AppProjects(namespace).Delete(ctx, name, deleteOptions)
}

func (be *KubernetesBackend) Update(ctx context.Context, project *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
	return be.appClient.ArgoprojV1alpha1().AppProjects(project.Namespace).Update(ctx, project, v1.UpdateOptions{})
}

func (be *KubernetesBackend) Patch(ctx context.Context, name string, namespace string, patch []byte) (*v1alpha1.AppProject, error) {
	return be.appClient.ArgoprojV1alpha1().AppProjects(namespace).Patch(ctx, name, types.JSONPatchType, patch, v1.PatchOptions{})
}

func (be *KubernetesBackend) SupportsPatch() bool {
	return be.usePatch
}

func (be *KubernetesBackend) StartInformer(ctx context.Context) error {
	return be.appProjectInformer.Start(ctx)
}

func (be *KubernetesBackend) EnsureSynced(timeout time.Duration) error {
	ctx, cancelFunc := context.WithTimeout(context.Background(), timeout)
	defer cancelFunc()
	return be.appProjectInformer.WaitForSync(ctx)
}
