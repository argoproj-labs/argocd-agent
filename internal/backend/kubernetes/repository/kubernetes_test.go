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
	"testing"

	"github.com/argoproj/argo-cd/v3/common"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_isValidRepositorySecret(t *testing.T) {
	tests := []struct {
		name      string
		secret    *corev1.Secret
		namespace string
		want      bool
	}{
		{
			name: "valid repository secret",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			namespace: "argocd",
			want:      true,
		},
		{
			name: "secret in wrong namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "other-namespace",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "secret without labels",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					// No labels
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "secret with empty labels",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels:    map[string]string{},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "secret with wrong secret type label",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeCluster,
					},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "secret with missing secret type label",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels: map[string]string{
						"other-label": "other-value",
					},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "secret with nil data",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: nil,
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "secret with empty data",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "secret missing project field",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"url":      []byte("https://github.com/example/repo.git"),
					"username": []byte("myuser"),
					"password": []byte("mypass"),
				},
			},
			namespace: "argocd",
			want:      false,
		},
		{
			name: "secret with empty project field",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"project": []byte(""),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			namespace: "argocd",
			want:      false, // Empty project value is not allowed
		},
		{
			name: "secret with additional valid fields",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
						"additional-label":        "additional-value",
					},
					Annotations: map[string]string{
						"annotation-key": "annotation-value",
					},
				},
				Data: map[string][]byte{
					"project":       []byte("my-project"),
					"url":           []byte("https://github.com/example/repo.git"),
					"username":      []byte("myuser"),
					"password":      []byte("mypass"),
					"sshPrivateKey": []byte("-----BEGIN PRIVATE KEY-----\n..."),
				},
			},
			namespace: "argocd",
			want:      true,
		},
		{
			name: "different namespace scenarios",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "repo-in-custom-ns",
					Namespace: "custom-namespace",
					Labels: map[string]string{
						common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
					},
				},
				Data: map[string][]byte{
					"project": []byte("custom-project"),
					"url":     []byte("https://github.com/example/custom.git"),
				},
			},
			namespace: "custom-namespace",
			want:      true,
		},
		{
			name: "case sensitivity test - wrong case in secret type",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					Labels: map[string]string{
						common.LabelKeySecretType: "Repository", // Wrong case
					},
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			namespace: "argocd",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidRepositorySecret(tt.secret, tt.namespace)
			assert.Equal(t, tt.want, got, "isValidRepositorySecret() validation failed")
		})
	}
}
