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

package agent

import (
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/kube"
	fakekube "github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fixtures creates a fake Kubernetes clientset and ResourceTrackingReader
// with a ConfigMap containing the specified tracking method.
func fixtures(namespace string, trackingMethod v1alpha1.TrackingMethod) (*kube.KubernetesClient, *ResourceTrackingReader) {
	data := map[string]string{}

	if trackingMethod != "" {
		data["application.resourceTrackingMethod"] = string(trackingMethod)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      "argocd-cm",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "argocd",
			},
		},
		Data: data,
	}

	kubeClient := fakekube.NewDynamicFakeClient(cm)
	reader := NewResourceTrackingReader(kubeClient, namespace)

	return kubeClient, reader
}

func TestResourceTrackingReader_GetConfig(t *testing.T) {
	t.Run("should use default annotation method when not configured", func(t *testing.T) {
		_, reader := fixtures("argocd", "")

		config := reader.GetConfig()
		// Should return default (annotation) when not set
		assert.Equal(t, v1alpha1.TrackingMethodAnnotation, config.Method)
		assert.Equal(t, "argocd.argoproj.io/tracking-id", config.AnnotationKey)
	})

	t.Run("should read annotation tracking method", func(t *testing.T) {
		_, reader := fixtures("argocd", v1alpha1.TrackingMethodAnnotation)

		config := reader.GetConfig()
		assert.Equal(t, v1alpha1.TrackingMethodAnnotation, config.Method)
	})

	t.Run("should read label tracking method", func(t *testing.T) {
		_, reader := fixtures("argocd", v1alpha1.TrackingMethodLabel)

		config := reader.GetConfig()
		assert.Equal(t, v1alpha1.TrackingMethodLabel, config.Method)
	})

	t.Run("should read annotation+label tracking method", func(t *testing.T) {
		_, reader := fixtures("argocd", v1alpha1.TrackingMethodAnnotationAndLabel)

		config := reader.GetConfig()
		assert.Equal(t, v1alpha1.TrackingMethodAnnotationAndLabel, config.Method)
	})

	t.Run("should fall back to default for unknown method", func(t *testing.T) {
		_, reader := fixtures("argocd", "unknown-method")

		config := reader.GetConfig()
		// Should fall back to default (annotation)
		assert.Equal(t, v1alpha1.TrackingMethodAnnotation, config.Method)
	})
}

func TestResourceTrackingConfig_IsResourceTracked(t *testing.T) {
	tests := []struct {
		name        string
		config      *ResourceTrackingConfig
		labels      map[string]string
		annotations map[string]string
		expected    bool
	}{
		{
			name: "Annotation method - tracked with annotation",
			config: &ResourceTrackingConfig{
				Method:        v1alpha1.TrackingMethodAnnotation,
				AnnotationKey: "argocd.argoproj.io/tracking-id",
			},
			labels: nil,
			annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:namespace/Kind:name",
			},
			expected: true,
		},
		{
			name: "Annotation method - not tracked with only label",
			config: &ResourceTrackingConfig{
				Method:        v1alpha1.TrackingMethodAnnotation,
				AnnotationKey: "argocd.argoproj.io/tracking-id",
			},
			labels: map[string]string{
				"app.kubernetes.io/instance": "my-app",
			},
			annotations: nil,
			expected:    false,
		},
		{
			name: "Label method - tracked with label",
			config: &ResourceTrackingConfig{
				Method:   v1alpha1.TrackingMethodLabel,
				LabelKey: "app.kubernetes.io/instance",
			},
			labels: map[string]string{
				"app.kubernetes.io/instance": "my-app",
			},
			annotations: nil,
			expected:    true,
		},
		{
			name: "Label method - not tracked with only annotation",
			config: &ResourceTrackingConfig{
				Method:   v1alpha1.TrackingMethodLabel,
				LabelKey: "app.kubernetes.io/instance",
			},
			labels: nil,
			annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:namespace/Kind:name",
			},
			expected: false,
		},
		{
			name: "Annotation+Label method - not tracked with only annotation",
			config: &ResourceTrackingConfig{
				Method:        v1alpha1.TrackingMethodAnnotationAndLabel,
				AnnotationKey: "argocd.argoproj.io/tracking-id",
				LabelKey:      "app.kubernetes.io/instance",
			},
			labels: nil,
			annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:namespace/Kind:name",
			},
			expected: false,
		},
		{
			name: "Annotation+Label method - not tracked with only label",
			config: &ResourceTrackingConfig{
				Method:        v1alpha1.TrackingMethodAnnotationAndLabel,
				AnnotationKey: "argocd.argoproj.io/tracking-id",
				LabelKey:      "app.kubernetes.io/instance",
			},
			labels: map[string]string{
				"app.kubernetes.io/instance": "my-app",
			},
			annotations: nil,
			expected:    false,
		},
		{
			name: "Annotation+Label method - tracked with both",
			config: &ResourceTrackingConfig{
				Method:        v1alpha1.TrackingMethodAnnotationAndLabel,
				AnnotationKey: "argocd.argoproj.io/tracking-id",
				LabelKey:      "app.kubernetes.io/instance",
			},
			labels: map[string]string{
				"app.kubernetes.io/instance": "my-app",
			},
			annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:namespace/Kind:name",
			},
			expected: true,
		},
		{
			name: "Annotation+Label method - not tracked with neither",
			config: &ResourceTrackingConfig{
				Method:        v1alpha1.TrackingMethodAnnotationAndLabel,
				AnnotationKey: "argocd.argoproj.io/tracking-id",
				LabelKey:      "app.kubernetes.io/instance",
			},
			labels:      nil,
			annotations: nil,
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsResourceTracked(tt.labels, tt.annotations)
			assert.Equal(t, tt.expected, result)
		})
	}
}
