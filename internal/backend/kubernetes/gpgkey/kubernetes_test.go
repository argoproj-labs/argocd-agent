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
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_isValidGPGKeysConfigMap(t *testing.T) {
	tests := []struct {
		name      string
		cm        *corev1.ConfigMap
		namespace string
		want      bool
	}{
		{
			name: "valid GPG keys ConfigMap",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      common.ArgoCDGPGKeysConfigMapName,
					Namespace: "argocd",
				},
				Data: map[string]string{
					"my-key": "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...",
				},
			},
			namespace: "argocd",
			want:      true,
		},
		{
			name: "ConfigMap in wrong namespace",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      common.ArgoCDGPGKeysConfigMapName,
					Namespace: "other-namespace",
				},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "ConfigMap with wrong name",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-other-configmap",
					Namespace: "argocd",
				},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "ConfigMap with wrong name and wrong namespace",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-other-configmap",
					Namespace: "other-namespace",
				},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "valid GPG keys ConfigMap with empty data",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      common.ArgoCDGPGKeysConfigMapName,
					Namespace: "argocd",
				},
				Data: map[string]string{},
			},
			namespace: "argocd",
			want:      true,
		},
		{
			name: "valid GPG keys ConfigMap in custom namespace",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      common.ArgoCDGPGKeysConfigMapName,
					Namespace: "custom-namespace",
				},
			},
			namespace: "custom-namespace",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidGPGKeysConfigMap(tt.cm, tt.namespace)
			assert.Equal(t, tt.want, got, "isValidGPGKeysConfigMap() validation failed")
		})
	}
}

func TestDefaultFilterChain_GPGKeyFiltering(t *testing.T) {
	namespace := "argocd"
	filterChain := DefaultFilterChain(namespace)

	tests := []struct {
		name     string
		cm       *corev1.ConfigMap
		expected bool
		reason   string
	}{
		{
			name: "Valid GPG keys ConfigMap should be admitted",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      common.ArgoCDGPGKeysConfigMapName,
					Namespace: namespace,
				},
				Data: map[string]string{
					"my-key": "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...",
				},
			},
			expected: true,
			reason:   "Valid GPG keys ConfigMap with correct name and namespace",
		},
		{
			name: "ConfigMap with wrong name should be rejected",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-other-configmap",
					Namespace: namespace,
				},
			},
			expected: false,
			reason:   "ConfigMap with wrong name should be rejected",
		},
		{
			name: "ConfigMap in wrong namespace should be rejected",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      common.ArgoCDGPGKeysConfigMapName,
					Namespace: "wrong-namespace",
				},
			},
			expected: false,
			reason:   "GPG keys ConfigMap in wrong namespace should be rejected",
		},
		{
			name: "ConfigMap with nil data should still be admitted",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      common.ArgoCDGPGKeysConfigMapName,
					Namespace: namespace,
				},
				Data: nil,
			},
			expected: true,
			reason:   "GPG keys ConfigMap with nil data should still be admitted based on name/namespace",
		},
		{
			name: "ConfigMap with skip sync label should be rejected",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      common.ArgoCDGPGKeysConfigMapName,
					Namespace: namespace,
					Labels:    map[string]string{config.SkipSyncLabel: "true"},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterChain.Admit(tt.cm)
			assert.Equal(t, tt.expected, result, tt.reason)
		})
	}
}
