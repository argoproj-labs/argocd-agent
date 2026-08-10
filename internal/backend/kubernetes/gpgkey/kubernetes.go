// Copyright 2026 The argocd-agent Authors
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

package gpgkey

import (
	"context"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj/argo-cd/v3/common"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var _ backend.GPGKey = &KubernetesBackend{}

// KubernetesBackend is an implementation of the backend.GPGKey interface for the argocd-gpg-keys-cm ConfigMap.
type KubernetesBackend struct {
	kubeclient     kubernetes.Interface
	gpgKeyInformer informer.InformerInterface
	namespace      string
}

func NewKubernetesBackend(kubeclient kubernetes.Interface, namespace string, gpgKeyInformer informer.InformerInterface) *KubernetesBackend {
	return &KubernetesBackend{
		kubeclient:     kubeclient,
		gpgKeyInformer: gpgKeyInformer,
		namespace:      namespace,
	}
}

func (be *KubernetesBackend) Get(ctx context.Context, name string, namespace string) (*corev1.ConfigMap, error) {
	return be.kubeclient.CoreV1().ConfigMaps(namespace).Get(ctx, name, v1.GetOptions{})
}

func (be *KubernetesBackend) Create(ctx context.Context, cm *corev1.ConfigMap) (*corev1.ConfigMap, error) {
	return be.kubeclient.CoreV1().ConfigMaps(cm.Namespace).Create(ctx, cm, v1.CreateOptions{FieldManager: "argocd-agent"})
}

func (be *KubernetesBackend) Update(ctx context.Context, cm *corev1.ConfigMap) (*corev1.ConfigMap, error) {
	return be.kubeclient.CoreV1().ConfigMaps(cm.Namespace).Update(ctx, cm, v1.UpdateOptions{})
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
			return fmt.Errorf("unexpected propagationPolicy value: '%v'", *deletionPropagation)
		}
	}
	return be.kubeclient.CoreV1().ConfigMaps(namespace).Delete(ctx, name, v1.DeleteOptions{
		PropagationPolicy: &k8sPropagationPolicy,
	})
}

func (be *KubernetesBackend) StartInformer(ctx context.Context) error {
	return be.gpgKeyInformer.Start(ctx)
}

func (be *KubernetesBackend) EnsureSynced(timeout time.Duration) error {
	ctx, cancelFunc := context.WithTimeout(context.Background(), timeout)
	defer cancelFunc()
	return be.gpgKeyInformer.WaitForSync(ctx)
}

func DefaultFilterChain(namespace string) *filter.Chain[*corev1.ConfigMap] {
	c := filter.NewFilterChain[*corev1.ConfigMap]()

	// Ignore the GPG key ConfigMap if it has the skip sync label
	c.AppendAdmitFilter(func(res *corev1.ConfigMap) bool {
		if v, ok := res.Labels[config.SkipSyncLabel]; ok && v == "true" {
			return false
		}
		return true
	})

	c.AppendAdmitFilter(func(res *corev1.ConfigMap) bool {
		return isValidGPGKeysConfigMap(res, namespace)
	})
	return c
}

func isValidGPGKeysConfigMap(res *corev1.ConfigMap, namespace string) bool {
	return res.Namespace == namespace && res.Name == common.ArgoCDGPGKeysConfigMapName
}
