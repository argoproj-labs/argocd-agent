// Copyright 2025 The argocd-agent Authors
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

package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

var _ backend.Repository = &KubernetesBackend{}

// KubernetesBackend is an implementation of the backend.Repository interface, which is used to manage Argo CD Repository secrets.
// KubernetesBackend stores/retrieves all data from Argo CD Repository secrets on the cluster that is local to the agent/principal.
//
// KubernetesBackend is used by both the principal and agent components.
type KubernetesBackend struct {
	// kubeclient is used to interface with Argo CD Repository secrets on the cluster on which agent/principal is installed.
	kubeclient kubernetes.Interface
	// repositoryInformer is used to watch for change events for Argo CD Repository secrets on the cluster
	repositoryInformer informer.InformerInterface
	// namespace to contain the source of Argo CD Repository secrets in all cases, mainly used by agents
	namespace string
	// usePatch is used to indicate whether the KubernetesBackend should use JSON Patch for updates
	usePatch bool
}

func NewKubernetesBackend(kubeclient kubernetes.Interface, namespace string, repoInformer informer.InformerInterface, usePatch bool) *KubernetesBackend {
	return &KubernetesBackend{
		kubeclient:         kubeclient,
		repositoryInformer: repoInformer,
		namespace:          namespace,
		usePatch:           usePatch,
	}
}

func (be *KubernetesBackend) List(ctx context.Context, selector backend.RepositorySelector) ([]corev1.Secret, error) {
	l, err := be.kubeclient.CoreV1().Secrets(selector.Namespace).List(ctx, v1.ListOptions{
		LabelSelector: labels.SelectorFromSet(selector.Labels).String(),
	})
	if err != nil {
		return nil, err
	}

	return l.Items, nil
}

func (be *KubernetesBackend) Create(ctx context.Context, project *corev1.Secret) (*corev1.Secret, error) {
	return be.kubeclient.CoreV1().Secrets(project.Namespace).Create(ctx, project, v1.CreateOptions{FieldManager: "foo"})
}

func (be *KubernetesBackend) Get(ctx context.Context, name string, namespace string) (*corev1.Secret, error) {
	return be.kubeclient.CoreV1().Secrets(namespace).Get(ctx, name, v1.GetOptions{})
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

	return be.kubeclient.CoreV1().Secrets(namespace).Delete(ctx, name, deleteOptions)
}

func (be *KubernetesBackend) Update(ctx context.Context, project *corev1.Secret) (*corev1.Secret, error) {
	return be.kubeclient.CoreV1().Secrets(project.Namespace).Update(ctx, project, v1.UpdateOptions{})
}

func (be *KubernetesBackend) Patch(ctx context.Context, name string, namespace string, patch []byte) (*corev1.Secret, error) {
	return be.kubeclient.CoreV1().Secrets(namespace).Patch(ctx, name, types.JSONPatchType, patch, v1.PatchOptions{})
}

func (be *KubernetesBackend) SupportsPatch() bool {
	return be.usePatch
}

func (be *KubernetesBackend) StartInformer(ctx context.Context) error {
	return be.repositoryInformer.Start(ctx)
}

func (be *KubernetesBackend) EnsureSynced(timeout time.Duration) error {
	ctx, cancelFunc := context.WithTimeout(context.Background(), timeout)
	defer cancelFunc()
	return be.repositoryInformer.WaitForSync(ctx)
}

func DefaultFilterChain(namespace string) *filter.Chain[*corev1.Secret] {
	c := filter.NewFilterChain[*corev1.Secret]()
	c.AppendAdmitFilter(func(res *corev1.Secret) bool {
		return isValidRepositorySecret(res, namespace)
	})
	return c
}

// isValidRepositorySecret is used by the repository informer to filter repository secrets.
func isValidRepositorySecret(res *corev1.Secret, namespace string) bool {
	logCtx := log().WithFields(logrus.Fields{
		"namespace": res.Namespace,
		"name":      res.Name,
	})

	// Watch secrets in a given namespace
	if res.Namespace != namespace {
		return false
	}

	// Watch only repository secrets
	if res.Labels == nil || res.Labels[common.LabelKeySecretType] != common.LabelValueSecretTypeRepository {
		return false
	}

	if res.Data == nil {
		logCtx.Tracef("Repository secret has no data")
		return false
	}

	// Repository secrets must be project scoped
	project, ok := res.Data["project"]
	if !ok {
		logCtx.Tracef("Repository secret is not project scoped")
		return false
	}

	if len(project) == 0 {
		logCtx.Tracef("Repository secret should not have an empty project")
		return false
	}

	logCtx.Tracef("Received a repository secret")

	return true
}

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ComponentLogger("RepositoryBackend")
}
