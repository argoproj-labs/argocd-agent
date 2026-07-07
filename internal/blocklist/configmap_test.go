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
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLoadFromConfigMap(t *testing.T) {
	ns := "argocd"

	t.Run("returns empty when ConfigMap does not exist", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		fps, err := LoadFromConfigMap(context.Background(), client, ns)
		require.NoError(t, err)
		assert.Empty(t, fps)
	})

	t.Run("returns fingerprints from existing ConfigMap", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: config.ConfigMapNameTLSBlocklist, Namespace: ns},
			Data:       map[string]string{config.ConfigMapKeyBlocklistChecksums: `["AA:BB","CC:DD"]`},
		}
		client := fake.NewSimpleClientset(cm)
		fps, err := LoadFromConfigMap(context.Background(), client, ns)
		require.NoError(t, err)
		assert.Equal(t, []string{"AA:BB", "CC:DD"}, fps)
	})

	t.Run("returns error for malformed JSON", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: config.ConfigMapNameTLSBlocklist, Namespace: ns},
			Data:       map[string]string{config.ConfigMapKeyBlocklistChecksums: `not-json`},
		}
		client := fake.NewSimpleClientset(cm)
		_, err := LoadFromConfigMap(context.Background(), client, ns)
		assert.Error(t, err)
	})

	t.Run("returns empty for missing data key", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: config.ConfigMapNameTLSBlocklist, Namespace: ns},
			Data:       map[string]string{},
		}
		client := fake.NewSimpleClientset(cm)
		fps, err := LoadFromConfigMap(context.Background(), client, ns)
		require.NoError(t, err)
		assert.Empty(t, fps)
	})
}

func TestSaveToConfigMap(t *testing.T) {
	ns := "argocd"

	t.Run("creates ConfigMap when it does not exist", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		err := SaveToConfigMap(context.Background(), client, ns, []string{"AA:BB"})
		require.NoError(t, err)

		cm, err := client.CoreV1().ConfigMaps(ns).Get(context.Background(), config.ConfigMapNameTLSBlocklist, metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, `["AA:BB"]`, cm.Data[config.ConfigMapKeyBlocklistChecksums])
	})

	t.Run("updates existing ConfigMap", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: config.ConfigMapNameTLSBlocklist, Namespace: ns},
			Data:       map[string]string{config.ConfigMapKeyBlocklistChecksums: `["AA:BB"]`},
		}
		client := fake.NewSimpleClientset(cm)
		err := SaveToConfigMap(context.Background(), client, ns, []string{"AA:BB", "CC:DD"})
		require.NoError(t, err)

		updated, err := client.CoreV1().ConfigMaps(ns).Get(context.Background(), config.ConfigMapNameTLSBlocklist, metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, `["AA:BB","CC:DD"]`, updated.Data[config.ConfigMapKeyBlocklistChecksums])
	})

	t.Run("saves empty list", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		err := SaveToConfigMap(context.Background(), client, ns, []string{})
		require.NoError(t, err)

		cm, err := client.CoreV1().ConfigMaps(ns).Get(context.Background(), config.ConfigMapNameTLSBlocklist, metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, `[]`, cm.Data[config.ConfigMapKeyBlocklistChecksums])
	})
}
