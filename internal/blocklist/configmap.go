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
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// FingerprintsFromConfigMapData extracts the blocklist fingerprints from a
// ConfigMap's Data map. Each key in the map is a fingerprint.
func FingerprintsFromConfigMapData(data map[string]string) []string {
	fps := make([]string, 0, len(data))
	for fp := range data {
		fps = append(fps, fp)
	}
	return fps
}

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

	return FingerprintsFromConfigMapData(cm.Data), nil
}

// SaveToConfigMap writes the given fingerprints to the Kubernetes ConfigMap.
// Creates the ConfigMap if it does not exist.
func SaveToConfigMap(ctx context.Context, client kubernetes.Interface, namespace string, fingerprints []string) error {
	cmData := make(map[string]string, len(fingerprints))
	for _, fp := range fingerprints {
		cmData[fp] = ""
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
			Data: cmData,
		}
		if _, err := client.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("failed to create blocklist ConfigMap: %w", err)
		}
		return nil
	}

	cm.Data = cmData
	if _, err := client.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update blocklist ConfigMap: %w", err)
	}
	return nil
}
