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

package repository

import (
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDefaultFilterChain_RepositoryFiltering(t *testing.T) {
	namespace := "argocd"
	filterChain := DefaultFilterChain(namespace)

	tests := []struct {
		name     string
		secret   *corev1.Secret
		expected bool
		reason   string
	}{
		{
			name: "Valid repository secret should be admitted",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: namespace,
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			expected: true,
			reason:   "Valid repository secret with all required fields",
		},
		{
			name: "Repository secret with skip sync label=true should be filtered by informer (not this filter)",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: namespace,
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
						config.SkipSyncLabel:      "true",
					},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			expected: true,
			reason:   "DefaultFilterChain only validates repository format, skip sync filtering happens at informer level",
		},
		{
			name: "Repository secret in wrong namespace should be rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "wrong-namespace",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			expected: false,
			reason:   "Repository secret in wrong namespace should be rejected",
		},
		{
			name: "Secret without repository label should be rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: namespace,
					Labels:    map[string]string{},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			expected: false,
			reason:   "Secret without repository type label should be rejected",
		},
		{
			name: "Repository secret without project data should be rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: namespace,
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"url": []byte("https://github.com/example/repo.git"),
				},
			},
			expected: false,
			reason:   "Repository secret without project data should be rejected",
		},
		{
			name: "Repository secret with empty project should be rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: namespace,
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"project": []byte(""),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			expected: false,
			reason:   "Repository secret with empty project should be rejected",
		},
		{
			name: "Repository secret with nil data should be rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: namespace,
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: nil,
			},
			expected: false,
			reason:   "Repository secret with nil data should be rejected",
		},
		{
			name: "Repository secret with nil labels should be rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: namespace,
					Labels:    nil,
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			expected: false,
			reason:   "Repository secret with nil labels should be rejected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterChain.Admit(tt.secret)
			assert.Equal(t, tt.expected, result, tt.reason)
		})
	}
}

func TestIsValidRepositorySecret(t *testing.T) {
	namespace := "argocd"

	tests := []struct {
		name     string
		secret   *corev1.Secret
		expected bool
		reason   string
	}{
		{
			name: "Valid repository secret",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: namespace,
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"project": []byte("my-project"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			expected: true,
			reason:   "Should accept valid repository secret",
		},
		{
			name: "Repository secret with skip sync label (should still be valid at this level)",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: namespace,
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
						config.SkipSyncLabel:      "true",
					},
				},
				Data: map[string][]byte{
					"project": []byte("my-project"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			expected: true,
			reason:   "isValidRepositorySecret doesn't check skip sync label - that's handled by informer",
		},
		{
			name: "Wrong secret type",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: namespace,
					Labels: map[string]string{
						common.LabelKeySecretType: "cluster",
					},
				},
				Data: map[string][]byte{
					"project": []byte("my-project"),
				},
			},
			expected: false,
			reason:   "Should reject non-repository secrets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidRepositorySecret(tt.secret, namespace)
			assert.Equal(t, tt.expected, result, tt.reason)
		})
	}
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
