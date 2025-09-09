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

package principal

import (
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServer_DefaultAppFilterChain_SkipSyncLabel(t *testing.T) {
	server := &Server{
		options: &ServerOptions{
			namespaces: []string{"argocd", "apps"},
		},
	}

	filterChain := server.defaultAppFilterChain()

	tests := []struct {
		name     string
		app      *v1alpha1.Application
		expected bool
	}{
		{
			name: "Application without skip sync label should be admitted",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "argocd",
					Labels:    map[string]string{},
				},
			},
			expected: true,
		},
		{
			name: "Application with skip sync label set to false should be admitted",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "argocd",
					Labels: map[string]string{
						config.SkipSyncLabel: "false",
					},
				},
			},
			expected: true,
		},
		{
			name: "Application with skip sync label set to empty string should be admitted",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "argocd",
					Labels: map[string]string{
						config.SkipSyncLabel: "",
					},
				},
			},
			expected: true,
		},
		{
			name: "Application with skip sync label set to true should be rejected",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "argocd",
					Labels: map[string]string{
						config.SkipSyncLabel: "true",
					},
				},
			},
			expected: false,
		},
		{
			name: "Application with skip sync label set to TRUE (case sensitive) should be admitted",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "argocd",
					Labels: map[string]string{
						config.SkipSyncLabel: "TRUE",
					},
				},
			},
			expected: true,
		},
		{
			name: "Application with skip sync label and other labels should be rejected when skip sync is true",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "argocd",
					Labels: map[string]string{
						config.SkipSyncLabel:           "true",
						"app.kubernetes.io/name":       "test-app",
						"app.kubernetes.io/instance":   "prod",
						"app.kubernetes.io/managed-by": "argocd",
					},
				},
			},
			expected: false,
		},
		{
			name: "Application in wrong namespace with skip sync label should be rejected (namespace filter takes precedence)",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "wrong-namespace",
					Labels: map[string]string{
						config.SkipSyncLabel: "false",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterChain.Admit(tt.app)
			assert.Equal(t, tt.expected, result, "Filter result should match expected value")
		})
	}
}

func TestServer_DefaultAppFilterChain_NamespaceAndSkipSyncInteraction(t *testing.T) {
	server := &Server{
		options: &ServerOptions{
			namespaces: []string{"argocd", "apps", "staging"},
		},
	}

	filterChain := server.defaultAppFilterChain()

	tests := []struct {
		name     string
		app      *v1alpha1.Application
		expected bool
		reason   string
	}{
		{
			name: "App in allowed namespace without skip sync label should be admitted",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "apps",
				},
			},
			expected: true,
			reason:   "Namespace is allowed, no skip sync label",
		},
		{
			name: "App in allowed namespace with skip sync=true should be rejected",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "staging",
					Labels: map[string]string{
						config.SkipSyncLabel: "true",
					},
				},
			},
			expected: false,
			reason:   "Skip sync label takes effect even in allowed namespace",
		},
		{
			name: "App in disallowed namespace with skip sync=false should be rejected",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "production",
					Labels: map[string]string{
						config.SkipSyncLabel: "false",
					},
				},
			},
			expected: false,
			reason:   "Namespace filter rejects before skip sync filter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterChain.Admit(tt.app)
			assert.Equal(t, tt.expected, result, tt.reason)
		})
	}
}

func TestServer_DefaultAppFilterChain_EdgeCases(t *testing.T) {
	server := &Server{
		options: &ServerOptions{
			namespaces: []string{"argocd"},
		},
	}

	filterChain := server.defaultAppFilterChain()

	tests := []struct {
		name     string
		app      *v1alpha1.Application
		expected bool
		reason   string
	}{
		{
			name: "Application with nil Labels map should be admitted",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "argocd",
					Labels:    nil,
				},
			},
			expected: true,
			reason:   "Nil labels map should not cause issues",
		},
		{
			name: "Application with multiple skip sync related labels (only exact match matters)",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "argocd",
					Labels: map[string]string{
						config.SkipSyncLabel:                   "false",
						config.SkipSyncLabel + "-custom":       "true",
						"custom-" + config.SkipSyncLabel:       "true",
						"argocd-agent.argoproj-labs.io/custom": "true",
					},
				},
			},
			expected: true,
			reason:   "Only exact label match should be considered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterChain.Admit(tt.app)
			assert.Equal(t, tt.expected, result, tt.reason)
		})
	}
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
