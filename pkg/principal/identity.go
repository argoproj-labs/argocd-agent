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

package principal

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	IdentityConfigMapName = "argocd-agent-principal-identity"
	identityKey           = "principal-uid"
)

// EnsurePrincipalUID reads or creates the principal identity ConfigMap and
// returns the persistent UUID. The UUID survives restarts and scaling events.
func EnsurePrincipalUID(ctx context.Context, client kubernetes.Interface, namespace string) (string, error) {
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, IdentityConfigMapName, metav1.GetOptions{})
	if err == nil {
		uid, ok := cm.Data[identityKey]
		if ok && uid != "" {
			return uid, nil
		}
		return "", fmt.Errorf("identity ConfigMap exists but %q key is empty", identityKey)
	}

	if !errors.IsNotFound(err) {
		return "", fmt.Errorf("failed to get identity ConfigMap: %w", err)
	}

	uid := uuid.New().String()
	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      IdentityConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			identityKey: uid,
		},
	}

	if _, err := client.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		if errors.IsAlreadyExists(err) {
			return EnsurePrincipalUID(ctx, client, namespace)
		}
		return "", fmt.Errorf("failed to create identity ConfigMap: %w", err)
	}

	return uid, nil
}
