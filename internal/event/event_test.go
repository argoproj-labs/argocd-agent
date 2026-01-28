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

package event

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResourceRequest_IsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		request  ResourceRequest
		expected bool
	}{
		{
			name:     "empty request",
			request:  ResourceRequest{},
			expected: true,
		},
		{
			name: "request with only name",
			request: ResourceRequest{
				Name: "test-name",
			},
			expected: false,
		},
		{
			name: "request with only namespace",
			request: ResourceRequest{
				Namespace: "test-namespace",
			},
			expected: false,
		},
		{
			name: "request with only group",
			request: ResourceRequest{
				GroupVersionResource: metav1.GroupVersionResource{
					Group: "test-group",
				},
			},
			expected: false,
		},
		{
			name: "request with only version",
			request: ResourceRequest{
				GroupVersionResource: metav1.GroupVersionResource{
					Version: "v1",
				},
			},
			expected: false,
		},
		{
			name: "request with only resource",
			request: ResourceRequest{
				GroupVersionResource: metav1.GroupVersionResource{
					Resource: "test-resource",
				},
			},
			expected: false,
		},
		{
			name: "fully populated request",
			request: ResourceRequest{
				UUID:      "test-uuid",
				Name:      "test-name",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "test-group",
					Version:  "v1",
					Resource: "test-resource",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.request.IsEmpty()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestResourceRequest_IsList(t *testing.T) {
	tests := []struct {
		name string
		r    *ResourceRequest
		want bool
	}{
		{
			name: "list request - empty resource and name",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "",
				},
				Name: "",
			},
			want: true,
		},
		{
			name: "not a list request - has resource and name",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				Name: "test-deployment",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.IsList(); got != tt.want {
				t.Errorf("ResourceRequest.IsList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResourceRequest_IsResource(t *testing.T) {
	tests := []struct {
		name string
		r    *ResourceRequest
		want bool
	}{
		{
			name: "is resource - has resource and name",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				Name: "test-deployment",
			},
			want: true,
		},
		{
			name: "not a resource - missing resource",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "",
				},
				Name: "test-deployment",
			},
			want: false,
		},
		{
			name: "not a resource - missing name",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				Name: "",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.IsResource(); got != tt.want {
				t.Errorf("ResourceRequest.IsResource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResourceRequest_IsClusterScoped(t *testing.T) {
	tests := []struct {
		name string
		r    *ResourceRequest
		want bool
	}{
		{
			name: "cluster scoped - empty namespace",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				Name: "test-deployment",
			},
			want: true,
		},
		{
			name: "not cluster scoped - has namespace",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				Name: "test-deployment",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.IsClusterScoped(); got != tt.want {
				t.Errorf("ResourceRequest.IsClusterScoped() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResourceRequest_IsNamespaced(t *testing.T) {
	tests := []struct {
		name string
		r    *ResourceRequest
		want bool
	}{
		{
			name: "namespaced - has namespace",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				Name: "test-deployment",
			},
			want: true,
		},
		{
			name: "not namespaced - empty namespace",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				Name: "test-deployment",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.IsNamespaced(); got != tt.want {
				t.Errorf("ResourceRequest.IsNamespaced() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResourceRequest_IsValid(t *testing.T) {
	tests := []struct {
		name string
		r    *ResourceRequest
		want bool
	}{
		{
			name: "valid - is resource request",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				Name: "test-deployment",
			},
			want: true,
		},
		{
			name: "valid - is list request",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "",
				},
				Name: "",
			},
			want: true,
		},
		{
			name: "invalid - has resource but no name",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				Name: "",
			},
			want: false,
		},
		{
			name: "invalid - has name but no resource",
			r: &ResourceRequest{
				UUID:      "test-uuid",
				Namespace: "test-namespace",
				GroupVersionResource: metav1.GroupVersionResource{
					Group:    "apps",
					Version:  "v1",
					Resource: "",
				},
				Name: "test-deployment",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.IsValid(); got != tt.want {
				t.Errorf("ResourceRequest.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewTerminalRequestEvent(t *testing.T) {
	es := NewEventSource("test-source")

	t.Run("creates valid terminal request event", func(t *testing.T) {
		terminalReq := &ContainerTerminalRequest{
			UUID:          "test-uuid-123",
			Namespace:     "test-namespace",
			PodName:       "test-pod",
			ContainerName: "test-container",
			Command:       []string{"/bin/sh", "-c", "ls"},
			TTY:           true,
			Stdin:         true,
			Stdout:        true,
			Stderr:        true,
		}

		ev, err := es.NewTerminalRequestEvent(terminalReq)
		require.NoError(t, err)
		require.NotNil(t, ev)

		// Verify event type and schema
		require.Equal(t, TerminalRequest.String(), ev.Type())
		require.Equal(t, TargetTerminal.String(), ev.DataSchema())

		// Verify extensions
		resID, err := ev.Context.GetExtension(resourceID)
		require.NoError(t, err)
		require.Equal(t, "test-uuid-123", resID)

		evtID, err := ev.Context.GetExtension(eventID)
		require.NoError(t, err)
		require.Equal(t, "test-uuid-123", evtID)

		// Verify source
		require.Equal(t, "test-source", ev.Source())
	})

	t.Run("creates terminal request event with minimal fields", func(t *testing.T) {
		terminalReq := &ContainerTerminalRequest{
			UUID:      "minimal-uuid",
			Namespace: "ns",
			PodName:   "pod",
		}

		ev, err := es.NewTerminalRequestEvent(terminalReq)
		require.NoError(t, err)
		require.NotNil(t, ev)

		require.Equal(t, TerminalRequest.String(), ev.Type())
	})
}

func TestTerminalRequestFromEvent(t *testing.T) {
	es := NewEventSource("test-source")

	t.Run("extracts terminal request from event", func(t *testing.T) {
		originalReq := &ContainerTerminalRequest{
			UUID:          "extract-test-uuid",
			Namespace:     "extract-namespace",
			PodName:       "extract-pod",
			ContainerName: "extract-container",
			Command:       []string{"/bin/bash"},
			TTY:           true,
			Stdin:         true,
			Stdout:        true,
			Stderr:        false,
		}

		cloudEvent, err := es.NewTerminalRequestEvent(originalReq)
		require.NoError(t, err)

		// Wrap in Event type
		ev := New(cloudEvent, TargetTerminal)

		// Extract terminal request
		extractedReq, err := ev.TerminalRequest()
		require.NoError(t, err)
		require.NotNil(t, extractedReq)

		// Verify all fields match
		require.Equal(t, originalReq.UUID, extractedReq.UUID)
		require.Equal(t, originalReq.Namespace, extractedReq.Namespace)
		require.Equal(t, originalReq.PodName, extractedReq.PodName)
		require.Equal(t, originalReq.ContainerName, extractedReq.ContainerName)
		require.Equal(t, originalReq.Command, extractedReq.Command)
		require.Equal(t, originalReq.TTY, extractedReq.TTY)
		require.Equal(t, originalReq.Stdin, extractedReq.Stdin)
		require.Equal(t, originalReq.Stdout, extractedReq.Stdout)
		require.Equal(t, originalReq.Stderr, extractedReq.Stderr)
	})

	t.Run("handles empty command slice", func(t *testing.T) {
		originalReq := &ContainerTerminalRequest{
			UUID:      "empty-cmd-uuid",
			Namespace: "ns",
			PodName:   "pod",
			Command:   []string{},
		}

		cloudEvent, err := es.NewTerminalRequestEvent(originalReq)
		require.NoError(t, err)

		ev := New(cloudEvent, TargetTerminal)
		extractedReq, err := ev.TerminalRequest()
		require.NoError(t, err)
		require.Empty(t, extractedReq.Command)
	})
}

func TestTargetTerminal(t *testing.T) {
	es := NewEventSource("test-source")

	t.Run("Target returns TargetTerminal for terminal request event", func(t *testing.T) {
		terminalReq := &ContainerTerminalRequest{
			UUID:      "target-test-uuid",
			Namespace: "ns",
			PodName:   "pod",
		}

		ev, err := es.NewTerminalRequestEvent(terminalReq)
		require.NoError(t, err)

		target := Target(ev)
		require.Equal(t, TargetTerminal, target)
	})

	t.Run("TargetTerminal string representation", func(t *testing.T) {
		require.Equal(t, "terminal", TargetTerminal.String())
	})

	t.Run("TerminalRequest event type string representation", func(t *testing.T) {
		require.Equal(t, "io.argoproj.argocd-agent.event.terminal-request", TerminalRequest.String())
	})
}

func TestContainerTerminalRequestFields(t *testing.T) {
	t.Run("all fields are serializable", func(t *testing.T) {
		req := ContainerTerminalRequest{
			UUID:          "full-test-uuid",
			Namespace:     "full-namespace",
			PodName:       "full-pod",
			ContainerName: "full-container",
			Command:       []string{"/bin/sh", "-c", "echo hello"},
			TTY:           true,
			Stdin:         true,
			Stdout:        true,
			Stderr:        true,
		}

		// Verify the struct can be used
		require.Equal(t, "full-test-uuid", req.UUID)
		require.Equal(t, "full-namespace", req.Namespace)
		require.Equal(t, "full-pod", req.PodName)
		require.Equal(t, "full-container", req.ContainerName)
		require.Len(t, req.Command, 3)
		require.True(t, req.TTY)
		require.True(t, req.Stdin)
		require.True(t, req.Stdout)
		require.True(t, req.Stderr)
	})
}
