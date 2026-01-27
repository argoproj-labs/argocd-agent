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
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/util/settings"
)

const (
	trackingAnnotationKey = "argocd.argoproj.io/tracking-id"
	trackingLabelKey      = "app.kubernetes.io/instance"
)

// ResourceTrackingReader reads the resource tracking configuration
// from Argo CD's SettingsManager using an informer-based approach
type ResourceTrackingReader struct {
	settingsManager *settings.SettingsManager
}

// NewResourceTrackingReader creates a new ResourceTrackingReader
// It uses Argo CD's SettingsManager which maintains a shared informer cache
// for near real-time updates when the argocd-cm ConfigMap changes
func NewResourceTrackingReader(ctx context.Context, kubeClient *kube.KubernetesClient, namespace string) *ResourceTrackingReader {
	// Create SettingsManager which uses an informer for the argocd-cm ConfigMap
	// The context is used to control the lifecycle of the informer
	settingsMgr := settings.NewSettingsManager(ctx, kubeClient.Clientset, namespace)

	return &ResourceTrackingReader{
		settingsManager: settingsMgr,
	}
}

// IsResourceTracked checks if a resource has Argo CD tracking information
// based on the current tracking configuration from the SettingsManager
func (r *ResourceTrackingReader) IsResourceTracked(labels map[string]string, annotations map[string]string) (bool, error) {
	// Get tracking method from SettingsManager
	// This reads from the informer cache, so it's efficient and up-to-date
	trackingMethodStr, err := r.settingsManager.GetTrackingMethod()
	if err != nil {
		return false, err
	}

	// Handle different tracking methods
	switch trackingMethodStr {
	case string(v1alpha1.TrackingMethodLabel):
		_, hasLabel := labels[trackingLabelKey]
		return hasLabel, nil

	case string(v1alpha1.TrackingMethodAnnotationAndLabel):
		_, hasAnnotation := annotations[trackingAnnotationKey]
		_, hasLabel := labels[trackingLabelKey]
		return hasAnnotation && hasLabel, nil

	case string(v1alpha1.TrackingMethodAnnotation), "":
		// Default to annotation-based tracking (when empty or explicitly set)
		_, hasAnnotation := annotations[trackingAnnotationKey]
		return hasAnnotation, nil

	default:
		return false, fmt.Errorf("unknown tracking method: %s", trackingMethodStr)
	}
}
