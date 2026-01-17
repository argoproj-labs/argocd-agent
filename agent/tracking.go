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
	"context"

	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/util/settings"
)

// ResourceTrackingConfig holds the configuration for how Argo CD tracks resources
type ResourceTrackingConfig struct {
	// Method is the tracking method used by Argo CD
	Method v1alpha1.TrackingMethod
	// AnnotationKey is the annotation key used for tracking (if applicable)
	AnnotationKey string
	// LabelKey is the label key used for tracking (if applicable)
	LabelKey string
}

// ResourceTrackingReader reads the resource tracking configuration
// from Argo CD's SettingsManager using an informer-based approach
type ResourceTrackingReader struct {
	settingsManager *settings.SettingsManager
}

// NewResourceTrackingReader creates a new ResourceTrackingReader
// It uses Argo CD's SettingsManager which maintains a shared informer cache
// for near real-time updates when the argocd-cm ConfigMap changes
func NewResourceTrackingReader(kubeClient *kube.KubernetesClient, namespace string) *ResourceTrackingReader {
	// Create SettingsManager which uses an informer for the argocd-cm ConfigMap
	settingsMgr := settings.NewSettingsManager(context.Background(), kubeClient.Clientset, namespace)

	return &ResourceTrackingReader{
		settingsManager: settingsMgr,
	}
}

// GetConfig returns the current tracking configuration
// The configuration is read from SettingsManager which uses an informer,
// so it reflects near real-time state of the argocd-cm ConfigMap
func (r *ResourceTrackingReader) GetConfig() *ResourceTrackingConfig {

	// Get tracking method from SettingsManager
	// This reads from the informer cache, so it's efficient and up-to-date
	trackingMethodStr, err := r.settingsManager.GetTrackingMethod()
	if err != nil {
		// If there's an error reading the tracking method, use the default (annotation)
		return &ResourceTrackingConfig{
			Method:        v1alpha1.TrackingMethodAnnotation,
			AnnotationKey: "argocd.argoproj.io/tracking-id",
			LabelKey:      "app.kubernetes.io/instance",
		}
	}

	// Convert string to TrackingMethod type
	var trackingMethod v1alpha1.TrackingMethod
	switch trackingMethodStr {
	case string(v1alpha1.TrackingMethodAnnotation):
		trackingMethod = v1alpha1.TrackingMethodAnnotation
	case string(v1alpha1.TrackingMethodLabel):
		trackingMethod = v1alpha1.TrackingMethodLabel
	case string(v1alpha1.TrackingMethodAnnotationAndLabel):
		trackingMethod = v1alpha1.TrackingMethodAnnotationAndLabel
	default:
		// Unknown or empty, use default (annotation-based)
		trackingMethod = v1alpha1.TrackingMethodAnnotation
	}

	return &ResourceTrackingConfig{
		Method:        trackingMethod,
		AnnotationKey: "argocd.argoproj.io/tracking-id",
		LabelKey:      "app.kubernetes.io/instance",
	}
}

// IsResourceTracked checks if a resource has Argo CD tracking information
// based on the current tracking configuration
func (r *ResourceTrackingConfig) IsResourceTracked(labels map[string]string, annotations map[string]string) bool {
	if labels == nil {
		labels = make(map[string]string)
	}
	if annotations == nil {
		annotations = make(map[string]string)
	}

	switch r.Method {
	case v1alpha1.TrackingMethodAnnotation:
		// Only check annotation
		_, hasAnnotation := annotations[r.AnnotationKey]
		return hasAnnotation

	case v1alpha1.TrackingMethodLabel:
		// Only check label
		_, hasLabel := labels[r.LabelKey]
		return hasLabel

	case v1alpha1.TrackingMethodAnnotationAndLabel:
		// Check both annotation and label
		_, hasAnnotation := annotations[r.AnnotationKey]
		_, hasLabel := labels[r.LabelKey]
		return hasAnnotation && hasLabel

	default:
		// Unknown method, default to annotation-based tracking
		_, hasAnnotation := annotations[r.AnnotationKey]
		return hasAnnotation
	}
}
