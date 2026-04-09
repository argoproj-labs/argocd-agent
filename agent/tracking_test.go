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

package agent

import (
	"context"
	"testing"

	fakekube "github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newTestTrackingReaderWithLabelKey creates a ResourceTrackingReader with the specified tracking method and label key.
func newTestTrackingReaderWithLabelKey(trackingMethod v1alpha1.TrackingMethod, labelKey string) *ResourceTrackingReader {
	data := map[string]string{}
	if trackingMethod != "" {
		data["application.resourceTrackingMethod"] = string(trackingMethod)
	}
	if labelKey != "" {
		data["application.instanceLabelKey"] = labelKey
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      "argocd-cm",
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "argocd",
			},
		},
		Data: data,
	}
	kubeClient := fakekube.NewDynamicFakeClient(cm)
	return NewResourceTrackingReader(context.Background(), kubeClient, "argocd")
}

// newTestTrackingReader creates a ResourceTrackingReader with the specified tracking method.
func newTestTrackingReader(trackingMethod v1alpha1.TrackingMethod) *ResourceTrackingReader {
	return newTestTrackingReaderWithLabelKey(trackingMethod, "")
}

func TestResourceTrackingReader_IsResourceTracked(t *testing.T) {
	tests := []struct {
		name           string
		trackingMethod v1alpha1.TrackingMethod
		labelKey       string
		labels         map[string]string
		annotations    map[string]string
		expected       bool
	}{
		{
			name:           "Annotation method - tracked with annotation",
			trackingMethod: v1alpha1.TrackingMethodAnnotation,
			labels:         nil,
			annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:namespace/Kind:name",
			},
			expected: true,
		},
		{
			name:           "Annotation method - not tracked with only label",
			trackingMethod: v1alpha1.TrackingMethodAnnotation,
			labels: map[string]string{
				"app.kubernetes.io/instance": "my-app",
			},
			annotations: nil,
			expected:    false,
		},
		{
			name:           "Label method - tracked with label",
			trackingMethod: v1alpha1.TrackingMethodLabel,
			labels: map[string]string{
				"app.kubernetes.io/instance": "my-app",
			},
			annotations: nil,
			expected:    true,
		},
		{
			name:           "Label method - not tracked with only annotation",
			trackingMethod: v1alpha1.TrackingMethodLabel,
			labels:         nil,
			annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:namespace/Kind:name",
			},
			expected: false,
		},
		{
			name:           "Annotation+Label method - tracked with only annotation",
			trackingMethod: v1alpha1.TrackingMethodAnnotationAndLabel,
			labels:         nil,
			annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:namespace/Kind:name",
			},
			expected: true,
		},
		{
			name:           "Annotation+Label method - not tracked with only label",
			trackingMethod: v1alpha1.TrackingMethodAnnotationAndLabel,
			labels: map[string]string{
				"app.kubernetes.io/instance": "my-app",
			},
			annotations: nil,
			expected:    false,
		},
		{
			name:           "Annotation+Label method - tracked with both",
			trackingMethod: v1alpha1.TrackingMethodAnnotationAndLabel,
			labels: map[string]string{
				"app.kubernetes.io/instance": "my-app",
			},
			annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:namespace/Kind:name",
			},
			expected: true,
		},
		{
			name:           "Label method - tracked with custom label key",
			trackingMethod: v1alpha1.TrackingMethodLabel,
			labelKey:       "argocd.argoproj.io/instance",
			labels: map[string]string{
				"argocd.argoproj.io/instance": "my-app",
			},
			annotations: nil,
			expected:    true,
		},
		{
			name:           "Annotation+Label method - not tracked with neither",
			trackingMethod: v1alpha1.TrackingMethodAnnotationAndLabel,
			labels:         nil,
			annotations:    nil,
			expected:       false,
		},
		{
			name:           "Default method - uses annotation when method is empty",
			trackingMethod: "",
			labels:         nil,
			annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:namespace/Kind:name",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := newTestTrackingReaderWithLabelKey(tt.trackingMethod, tt.labelKey)
			result, err := reader.IsResourceTracked(tt.labels, tt.annotations)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
