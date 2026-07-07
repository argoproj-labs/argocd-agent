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

package blocklist

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// LoadFromConfigMap reads the blocklist fingerprints from the Kubernetes ConfigMap.
// Returns an empty slice if the ConfigMap does not exist yet.
func LoadFromConfigMap(ctx context.Context, client kubernetes.Interface, namespace string) ([]string, error) {
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, config.ConfigMapNameTLSBlocklist, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to get blocklist ConfigMap: %w", err)
	}

	data := cm.Data[config.ConfigMapKeyBlocklistChecksums]
	fingerPrints, err := ParseFingerprints(data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal blocklist data: %w", err)
	}
	return fingerPrints, nil
}

// SaveToConfigMap writes the given fingerprints to the Kubernetes ConfigMap.
// Creates the ConfigMap if it does not exist.
func SaveToConfigMap(ctx context.Context, client kubernetes.Interface, namespace string, fingerprints []string) error {
	data, err := json.Marshal(fingerprints)
	if err != nil {
		return fmt.Errorf("failed to marshal blocklist data: %w", err)
	}

	cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, config.ConfigMapNameTLSBlocklist, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get blocklist ConfigMap: %w", err)
		}
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.ConfigMapNameTLSBlocklist,
				Namespace: namespace,
			},
			Data: map[string]string{
				config.ConfigMapKeyBlocklistChecksums: string(data),
			},
		}
		if _, err := client.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("failed to create blocklist ConfigMap: %w", err)
		}
		return nil
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[config.ConfigMapKeyBlocklistChecksums] = string(data)
	if _, err := client.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update blocklist ConfigMap: %w", err)
	}
	return nil
}
