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
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event/targets"
	"github.com/argoproj-labs/argocd-agent/internal/resources"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ktypes "k8s.io/apimachinery/pkg/types"
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
		require.Equal(t, targets.Terminal.String(), ev.DataSchema())

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
		ev := New(cloudEvent, targets.Terminal)

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

		ev := New(cloudEvent, targets.Terminal)
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
		require.Equal(t, targets.Terminal, target)
	})

	t.Run("TargetTerminal string representation", func(t *testing.T) {
		require.Equal(t, "terminal", targets.Terminal.String())
	})

	t.Run("TerminalRequest event type string representation", func(t *testing.T) {
		require.Equal(t, "io.argoproj.argocd-agent.event.terminal-request", TerminalRequest.String())
	})
}

func TestSentAt(t *testing.T) {
	t.Run("SentAt returns nil when extension not set", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		require.Nil(t, SentAt(&ev))
	})

	t.Run("SetSentAt and SentAt round-trip", func(t *testing.T) {
		before := time.Now().UTC().Truncate(time.Nanosecond)
		ev := cloudevents.NewEvent()
		SetSentAt(&ev)
		after := time.Now().UTC()

		ts := SentAt(&ev)
		require.NotNil(t, ts)
		require.False(t, ts.Before(before), "sentat should not be before SetSentAt was called")
		require.False(t, ts.After(after), "sentat should not be after SetSentAt returned")
	})

	t.Run("SentAt returns nil for malformed extension", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetExtension(sentAt, "not-a-timestamp")
		require.Nil(t, SentAt(&ev))
	})
}

func TestApplicationSetEventRoundtrip(t *testing.T) {
	es := NewEventSource("test-source")
	appSet := &v1alpha1.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-appset",
			Namespace: "argocd",
			UID:       ktypes.UID("test-uid-123"),
		},
		Spec: v1alpha1.ApplicationSetSpec{
			Generators: []v1alpha1.ApplicationSetGenerator{},
		},
	}

	t.Run("Create event roundtrip", func(t *testing.T) {
		cev := es.ApplicationSetEvent(Create, appSet)
		require.NotNil(t, cev)
		require.Equal(t, Create.String(), cev.Type())
		require.Equal(t, targets.ApplicationSet.String(), cev.DataSchema())

		wrapped := New(cev, targets.ApplicationSet)
		require.Equal(t, targets.ApplicationSet, wrapped.Target())

		decoded, err := wrapped.ApplicationSet()
		require.NoError(t, err)
		require.Equal(t, "my-appset", decoded.Name)
		require.Equal(t, "argocd", decoded.Namespace)
	})

	t.Run("Delete event roundtrip", func(t *testing.T) {
		cev := es.ApplicationSetEvent(Delete, appSet)
		require.NotNil(t, cev)
		require.Equal(t, Delete.String(), cev.Type())

		wrapped := New(cev, targets.ApplicationSet)
		decoded, err := wrapped.ApplicationSet()
		require.NoError(t, err)
		require.Equal(t, appSet.Name, decoded.Name)
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

func TestSetPrincipalUID(t *testing.T) {
	ev := cloudevents.NewEvent()
	SetPrincipalUID(&ev, "test-uid-123")
	val, ok := ev.Extensions()[principalUID].(string)
	require.True(t, ok)
	require.Equal(t, "test-uid-123", val)
}

func TestPrincipalUID(t *testing.T) {
	t.Run("returns uid that was set", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		SetPrincipalUID(&ev, "abc-def")
		require.Equal(t, "abc-def", PrincipalUID(&ev))
	})

	t.Run("returns empty when not set", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		require.Equal(t, "", PrincipalUID(&ev))
	})
}

// requireMatchingRequestIDs asserts that an event carries a generated event ID
// that is identical to its resource ID, as request events do.
func requireMatchingRequestIDs(t *testing.T, cev *cloudevents.Event) {
	t.Helper()
	require.NotEmpty(t, EventID(cev))
	require.Equal(t, EventID(cev), ResourceID(cev))
}

// requireResponseIDs asserts that a response is correlated to its request through
// the event ID, and carries a freshly generated UUID of its own as resource ID.
func requireResponseIDs(t *testing.T, cev *cloudevents.Event, reqUUID string) {
	t.Helper()
	require.Equal(t, reqUUID, EventID(cev))
	_, err := uuid.Parse(ResourceID(cev))
	require.NoError(t, err, "the resource ID of a response should be a generated UUID")
	require.NotEqual(t, reqUUID, ResourceID(cev))
}

func TestEventSource_resourceEvents(t *testing.T) {
	evs := NewEventSource("test-source")
	meta := metav1.ObjectMeta{Name: "obj", Namespace: "ns", UID: ktypes.UID("uid-1"), ResourceVersion: "7"}

	tests := []struct {
		name      string
		evType    EventType
		target    targets.EventTarget
		build     func() *cloudevents.Event
		roundTrip func(t *testing.T, ev *Event)
	}{
		{
			name:   "appproject",
			evType: Create,
			target: targets.AppProject,
			build: func() *cloudevents.Event {
				return evs.AppProjectEvent(Create, &v1alpha1.AppProject{ObjectMeta: meta})
			},
			roundTrip: func(t *testing.T, ev *Event) {
				proj, err := ev.AppProject()
				require.NoError(t, err)
				require.Equal(t, "obj", proj.Name)
			},
		},
		{
			name:   "repository",
			evType: SpecUpdate,
			target: targets.Repository,
			build: func() *cloudevents.Event {
				return evs.RepositoryEvent(SpecUpdate, &corev1.Secret{ObjectMeta: meta})
			},
			roundTrip: func(t *testing.T, ev *Event) {
				repo, err := ev.Repository()
				require.NoError(t, err)
				require.Equal(t, "obj", repo.Name)
			},
		},
		{
			name:   "gpgkey",
			evType: Delete,
			target: targets.GPGKey,
			build: func() *cloudevents.Event {
				return evs.GPGKeyEvent(Delete, &corev1.ConfigMap{ObjectMeta: meta})
			},
			roundTrip: func(t *testing.T, ev *Event) {
				cm, err := ev.GPGKey()
				require.NoError(t, err)
				require.Equal(t, "obj", cm.Name)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cev := tt.build()

			require.Equal(t, "test-source", cev.Source())
			require.Equal(t, "1.0", cev.SpecVersion())
			require.Equal(t, tt.evType.String(), cev.Type())
			require.Equal(t, tt.target.String(), cev.DataSchema())
			require.Equal(t, "ns/obj", cev.Subject())
			require.Equal(t, "obj_uid-1_7", EventID(cev))
			require.Equal(t, "obj_uid-1", ResourceID(cev))
			require.Equal(t, tt.target, Target(cev))

			tt.roundTrip(t, New(cev, tt.target))
		})
	}
}

func TestEventSource_ClusterCacheInfoUpdateEvent(t *testing.T) {
	evs := NewEventSource("test-source")
	info := &ClusterCacheInfo{ApplicationsCount: 3, APIsCount: 5, ResourcesCount: 120}

	cev := evs.ClusterCacheInfoUpdateEvent(ClusterCacheInfoUpdate, info)

	require.Equal(t, ClusterCacheInfoUpdate.String(), cev.Type())
	require.Equal(t, targets.ClusterCacheInfoUpdate.String(), cev.DataSchema())
	requireMatchingRequestIDs(t, cev)

	got := &ClusterCacheInfo{}
	require.NoError(t, cev.DataAs(got))
	require.Equal(t, info, got)
}

func TestEventSource_HeartbeatEvent(t *testing.T) {
	evs := NewEventSource("test-source")

	for _, evType := range []EventType{Ping, Pong} {
		t.Run(evType.String(), func(t *testing.T) {
			cev := evs.HeartbeatEvent(evType)

			require.Equal(t, evType.String(), cev.Type())
			require.Equal(t, targets.Heartbeat.String(), cev.DataSchema())
			requireMatchingRequestIDs(t, cev)
			require.Empty(t, cev.Data())
		})
	}

	t.Run("every heartbeat gets a fresh ID", func(t *testing.T) {
		require.NotEqual(t, EventID(evs.HeartbeatEvent(Ping)), EventID(evs.HeartbeatEvent(Ping)))
	})
}

func TestEventSource_redisEvents(t *testing.T) {
	evs := NewEventSource("test-source")

	t.Run("request", func(t *testing.T) {
		body := RedisCommandBody{Get: &RedisCommandBodyGet{Key: "app|managed-resources|agent_app"}}

		cev, err := evs.NewRedisRequestEvent("conn-1", body)
		require.NoError(t, err)

		require.Equal(t, RedisGenericRequest.String(), cev.Type())
		require.Equal(t, targets.Redis.String(), cev.DataSchema())
		requireMatchingRequestIDs(t, cev)

		req, err := New(cev, targets.Redis).RedisRequest()
		require.NoError(t, err)
		require.Equal(t, EventID(cev), req.UUID)
		require.Equal(t, "conn-1", req.ConnectionUUID)
		require.Equal(t, body, req.Body)
	})

	t.Run("response is correlated to the request by event ID", func(t *testing.T) {
		body := RedisResponseBody{Get: &RedisResponseBodyGet{Bytes: []byte("value"), CacheHit: true}}

		cev := evs.NewRedisResponseEvent("req-uuid", "conn-1", body)

		require.Equal(t, RedisGenericResponse.String(), cev.Type())
		require.Equal(t, targets.Redis.String(), cev.DataSchema())
		requireResponseIDs(t, cev, "req-uuid")

		resp := &RedisResponse{}
		require.NoError(t, cev.DataAs(resp))
		require.Equal(t, "req-uuid", resp.UUID)
		require.Equal(t, "conn-1", resp.ConnectionUUID)
		require.Equal(t, body, resp.Body)
	})
}

func TestEventSource_resourceRequestAndResponse(t *testing.T) {
	evs := NewEventSource("test-source")

	t.Run("request", func(t *testing.T) {
		gvr := metav1.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
		params := map[string]string{"watch": "true"}

		cev, err := evs.NewResourceRequestEvent(gvr, "argocd", "app", "status", http.MethodPatch, []byte(`{"a":1}`), params)
		require.NoError(t, err)

		require.Equal(t, http.MethodPatch, cev.Type())
		require.Equal(t, targets.Resource.String(), cev.DataSchema())
		requireMatchingRequestIDs(t, cev)

		req, err := New(cev, targets.Resource).ResourceRequest()
		require.NoError(t, err)
		require.Equal(t, EventID(cev), req.UUID)
		require.Equal(t, "argocd", req.Namespace)
		require.Equal(t, "app", req.Name)
		require.Equal(t, "status", req.Subresource)
		require.Equal(t, http.MethodPatch, req.Method)
		require.Equal(t, []byte(`{"a":1}`), req.Body)
		require.Equal(t, params, req.Params)
		require.Equal(t, gvr, req.GroupVersionResource)
	})

	t.Run("response is correlated to the request by event ID", func(t *testing.T) {
		cev := evs.NewResourceResponseEvent("req-uuid", http.StatusNotFound, `{"kind":"Status"}`)

		require.Equal(t, GetResponse.String(), cev.Type())
		require.Equal(t, targets.Resource.String(), cev.DataSchema())
		requireResponseIDs(t, cev, "req-uuid")

		resp, err := New(cev, targets.Resource).ResourceResponse()
		require.NoError(t, err)
		require.Equal(t, "req-uuid", resp.UUID)
		require.Equal(t, http.StatusNotFound, resp.Status)
		require.Equal(t, `{"kind":"Status"}`, resp.Resource)
	})
}

func TestEventSource_responsesGetFreshResourceIDs(t *testing.T) {
	evs := NewEventSource("test-source")

	t.Run("redis", func(t *testing.T) {
		a := evs.NewRedisResponseEvent("req-uuid", "conn-1", RedisResponseBody{})
		b := evs.NewRedisResponseEvent("req-uuid", "conn-1", RedisResponseBody{})
		require.NotEqual(t, ResourceID(a), ResourceID(b))
	})

	t.Run("resource", func(t *testing.T) {
		a := evs.NewResourceResponseEvent("req-uuid", http.StatusOK, "{}")
		b := evs.NewResourceResponseEvent("req-uuid", http.StatusOK, "{}")
		require.NotEqual(t, ResourceID(a), ResourceID(b))
	})
}

func TestHTTPStatusFromError(t *testing.T) {
	gr := schema.GroupResource{Group: "argoproj.io", Resource: "applications"}

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "no error is OK", err: nil, want: http.StatusOK},
		{name: "a plain error is an internal server error", err: errors.New("boom"), want: http.StatusInternalServerError},
		{name: "not found", err: apierrors.NewNotFound(gr, "app"), want: http.StatusNotFound},
		{name: "conflict", err: apierrors.NewConflict(gr, "app", errors.New("changed")), want: http.StatusConflict},
		{
			name: "a wrapped API status error keeps its code",
			err:  fmt.Errorf("while patching: %w", apierrors.NewForbidden(gr, "app", errors.New("denied"))),
			want: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, HTTPStatusFromError(tt.err))
		})
	}
}

func TestEventErrors(t *testing.T) {
	discarded := NewEventDiscardedErr("event %s discarded", "abc")
	notAllowed := NewEventNotAllowedErr("mode %q", "autonomous")

	t.Run("discarded", func(t *testing.T) {
		require.ErrorIs(t, discarded, ErrEventDiscarded)
		require.True(t, IsEventDiscarded(discarded))
		require.False(t, IsEventNotAllowed(discarded))
		require.EqualError(t, discarded, "event discarded: event abc discarded")
	})

	t.Run("not allowed", func(t *testing.T) {
		require.ErrorIs(t, notAllowed, ErrEventNotAllowed)
		require.True(t, IsEventNotAllowed(notAllowed))
		require.False(t, IsEventDiscarded(notAllowed))
		require.EqualError(t, notAllowed, `event not allowed in this agent mode: mode "autonomous"`)
	})

	t.Run("unrelated and nil errors are neither", func(t *testing.T) {
		require.False(t, IsEventDiscarded(errors.New("other")))
		require.False(t, IsEventNotAllowed(errors.New("other")))
		require.False(t, IsEventDiscarded(nil))
		require.False(t, IsEventNotAllowed(nil))
	})
}

func TestEventSource_ProcessedEvent(t *testing.T) {
	app := &v1alpha1.Application{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "ns", UID: "uid-1", ResourceVersion: "1"}}
	original := NewEventSource("origin").ApplicationEvent(Create, app)
	SetPrincipalUID(original, "principal-uid")
	SetSentAt(original)

	processed := NewEventSource("ack-source").ProcessedEvent(EventProcessed, New(original, targets.Application))

	require.Equal(t, EventProcessed.String(), processed.Type())
	require.Equal(t, "ack-source", processed.Source())
	require.Equal(t, targets.EventAck.String(), processed.DataSchema())
	require.Equal(t, EventID(original), EventID(processed))
	require.Equal(t, ResourceID(original), ResourceID(processed))
	require.Equal(t, "principal-uid", PrincipalUID(processed))
	require.NotNil(t, SentAt(processed))
	require.Equal(t, *SentAt(original), *SentAt(processed))
	require.Empty(t, processed.Data(), "an acknowledgement does not carry the original payload")
}

func TestEventSource_resyncEvents(t *testing.T) {
	evs := NewEventSource("test-source")

	requireResyncEnvelope := func(t *testing.T, cev *cloudevents.Event, evType EventType) {
		t.Helper()
		require.Equal(t, evType.String(), cev.Type())
		require.Equal(t, targets.ResourceResync.String(), cev.DataSchema())
		requireMatchingRequestIDs(t, cev)
	}

	t.Run("request synced resource list", func(t *testing.T) {
		cev, err := evs.RequestSyncedResourceListEvent([]byte("checksum"))
		require.NoError(t, err)
		requireResyncEnvelope(t, cev, SyncedResourceList)

		got, err := New(cev, targets.ResourceResync).RequestSyncedResourceList()
		require.NoError(t, err)
		require.Equal(t, []byte("checksum"), got.Checksum)
	})

	t.Run("synced resource", func(t *testing.T) {
		key := resources.ResourceKey{Name: "app", Namespace: "ns", Kind: "Application", UID: "uid-1"}

		cev, err := evs.SyncedResourceEvent(key)
		require.NoError(t, err)
		requireResyncEnvelope(t, cev, ResponseSyncedResource)

		got, err := New(cev, targets.ResourceResync).SyncedResource()
		require.NoError(t, err)
		require.Equal(t, &SyncedResource{Name: "app", Namespace: "ns", UID: "uid-1", Kind: "Application"}, got)
	})

	t.Run("request update", func(t *testing.T) {
		reqUpdate := NewRequestUpdate("app", "ns", "Application", "uid-1", []byte("sum"))
		require.Equal(t, &RequestUpdate{Name: "app", Namespace: "ns", UID: "uid-1", Kind: "Application", Checksum: []byte("sum")}, reqUpdate)

		cev, err := evs.RequestUpdateEvent(reqUpdate)
		require.NoError(t, err)
		requireResyncEnvelope(t, cev, EventRequestUpdate)

		got, err := New(cev, targets.ResourceResync).RequestUpdate()
		require.NoError(t, err)
		require.Equal(t, reqUpdate, got)
	})

	t.Run("request resource resync", func(t *testing.T) {
		cev, err := evs.RequestResourceResyncEvent()
		require.NoError(t, err)
		requireResyncEnvelope(t, cev, EventRequestResourceResync)

		got, err := New(cev, targets.ResourceResync).RequestResourceResync()
		require.NoError(t, err)
		require.NotNil(t, got)
	})
}

func TestTarget(t *testing.T) {
	known := []targets.EventTarget{
		targets.Application, targets.AppProject, targets.Repository, targets.GPGKey,
		targets.Resource, targets.EventAck, targets.ResourceResync, targets.Redis,
		targets.ClusterCacheInfoUpdate, targets.ContainerLog, targets.Heartbeat,
		targets.Terminal, targets.ApplicationSet,
	}

	for _, want := range known {
		t.Run(want.String(), func(t *testing.T) {
			raw := cloudevents.NewEvent()
			raw.SetDataSchema(want.String())
			require.Equal(t, want, Target(&raw))
		})
	}

	t.Run("an unknown data schema has no target", func(t *testing.T) {
		raw := cloudevents.NewEvent()
		raw.SetDataSchema("something-else")
		require.Equal(t, targets.EventTarget(""), Target(&raw))
	})

	t.Run("a missing data schema has no target", func(t *testing.T) {
		raw := cloudevents.NewEvent()
		require.Equal(t, targets.EventTarget(""), Target(&raw))
	})
}

func TestFromWire(t *testing.T) {
	t.Run("an event survives the trip over the wire", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "ns", UID: "uid-1", ResourceVersion: "2"}}
		sent := NewEventSource("test-source").ApplicationEvent(SpecUpdate, app)

		pev, err := format.ToProto(sent)
		require.NoError(t, err)

		ev, err := FromWire(pev)
		require.NoError(t, err)
		require.Equal(t, targets.Application, ev.Target())
		require.Equal(t, SpecUpdate, ev.Type())
		require.Equal(t, EventID(sent), ev.EventID())
		require.Equal(t, ResourceID(sent), ev.ResourceID())

		got, err := ev.Application()
		require.NoError(t, err)
		require.Equal(t, "app", got.Name)
	})

	t.Run("an event with an unknown target is rejected", func(t *testing.T) {
		raw := cloudevents.NewEvent()
		raw.SetID("id-1")
		raw.SetSource("test-source")
		raw.SetType(Create.String())
		raw.SetDataSchema("not-a-target")

		pev, err := format.ToProto(&raw)
		require.NoError(t, err)

		ev, err := FromWire(pev)
		require.Nil(t, ev)
		require.ErrorContains(t, err, "unknown event target")
	})
}

func TestEvent_accessors(t *testing.T) {
	evs := NewEventSource("test-source")
	app := &v1alpha1.Application{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "ns", UID: "uid-1", ResourceVersion: "1"}}
	cev := evs.ApplicationEvent(Create, app)

	ev := New(cev, targets.Application)

	require.Equal(t, targets.Application, ev.Target())
	require.Equal(t, Create, ev.Type())
	require.Same(t, cev, ev.CloudEvent())
	require.Equal(t, "app_uid-1", ev.ResourceID())
	require.Equal(t, "app_uid-1_1", ev.EventID())

	t.Run("SetEvent replaces the wrapped event", func(t *testing.T) {
		replacement := evs.ApplicationEvent(Delete, app)
		ev.SetEvent(replacement)

		require.Equal(t, Delete, ev.Type())
		require.Same(t, replacement, ev.CloudEvent())
	})
}

func TestEventSource_NewLogRequestEvent(t *testing.T) {
	evs := NewEventSource("test-source")
	ptr := func(v int64) *int64 { return &v }

	tests := []struct {
		name   string
		params map[string]string
		want   ContainerLogRequest
	}{
		{
			name:   "no parameters",
			params: nil,
			want:   ContainerLogRequest{Namespace: "ns", PodName: "pod"},
		},
		{
			name: "every parameter",
			params: map[string]string{
				"container":                    "main",
				"follow":                       "true",
				"tailLines":                    "100",
				"sinceSeconds":                 "60",
				"sinceTime":                    "2026-01-01T00:00:00Z",
				"timestamps":                   "true",
				"previous":                     "true",
				"insecureSkipTLSVerifyBackend": "true",
				"limitBytes":                   "2048",
			},
			want: ContainerLogRequest{
				Namespace: "ns", PodName: "pod", Container: "main", Follow: true,
				TailLines: ptr(100), SinceSeconds: ptr(60), SinceTime: "2026-01-01T00:00:00Z",
				Timestamps: true, Previous: true, InsecureSkipTLSVerifyBackend: true, LimitBytes: ptr(2048),
			},
		},
		{
			name:   "follow alone",
			params: map[string]string{"follow": "true"},
			want:   ContainerLogRequest{Namespace: "ns", PodName: "pod", Follow: true},
		},
		{
			name:   "timestamps alone",
			params: map[string]string{"timestamps": "true"},
			want:   ContainerLogRequest{Namespace: "ns", PodName: "pod", Timestamps: true},
		},
		{
			name:   "previous alone",
			params: map[string]string{"previous": "true"},
			want:   ContainerLogRequest{Namespace: "ns", PodName: "pod", Previous: true},
		},
		{
			name:   "insecureSkipTLSVerifyBackend alone",
			params: map[string]string{"insecureSkipTLSVerifyBackend": "true"},
			want:   ContainerLogRequest{Namespace: "ns", PodName: "pod", InsecureSkipTLSVerifyBackend: true},
		},
		{
			name:   "booleans are case-insensitive",
			params: map[string]string{"follow": "TRUE", "timestamps": "True"},
			want:   ContainerLogRequest{Namespace: "ns", PodName: "pod", Follow: true, Timestamps: true},
		},
		{
			name:   "anything but true is false",
			params: map[string]string{"follow": "yes", "previous": "false"},
			want:   ContainerLogRequest{Namespace: "ns", PodName: "pod"},
		},
		{
			name:   "numbers that do not parse are ignored",
			params: map[string]string{"tailLines": "many", "sinceSeconds": "-x", "limitBytes": "1e3"},
			want:   ContainerLogRequest{Namespace: "ns", PodName: "pod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cev, err := evs.NewLogRequestEvent("ns", "pod", http.MethodGet, tt.params)
			require.NoError(t, err)

			require.Equal(t, http.MethodGet, cev.Type())
			require.Equal(t, targets.ContainerLog.String(), cev.DataSchema())
			requireMatchingRequestIDs(t, cev)

			got, err := New(cev, targets.ContainerLog).ContainerLogRequest()
			require.NoError(t, err)

			want := tt.want
			want.UUID = EventID(cev)
			require.Equal(t, want, *got)
		})
	}
}

func TestEventSource_ErrorEvent(t *testing.T) {
	data := &ErrorData{ResourceName: "app", ResourceNamespace: "ns", Message: "boom"}

	cev, err := NewEventSource("test-source").ErrorEvent(targets.Application, data)
	require.NoError(t, err)

	require.Equal(t, Error.String(), cev.Type())
	require.Equal(t, targets.Application.String(), cev.DataSchema())
	requireMatchingRequestIDs(t, cev)

	got := &ErrorData{}
	require.NoError(t, cev.DataAs(got))
	require.Equal(t, data, got)
}

func TestResourceIDAndEventID(t *testing.T) {
	t.Run("are empty when the extensions are not set", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		require.Equal(t, "", ResourceID(&ev))
		require.Equal(t, "", EventID(&ev))
	})

	t.Run("are empty when the extensions are not strings", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetExtension(resourceID, 42)
		ev.SetExtension(eventID, 42)
		require.Equal(t, "", ResourceID(&ev))
		require.Equal(t, "", EventID(&ev))
	})

	t.Run("return the extensions when they are set", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetExtension(resourceID, "res-1")
		ev.SetExtension(eventID, "evt-1")
		require.Equal(t, "res-1", ResourceID(&ev))
		require.Equal(t, "evt-1", EventID(&ev))
	})
}
