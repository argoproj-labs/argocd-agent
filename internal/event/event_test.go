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
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEventWriter(t *testing.T) {
	es := NewEventSource("test")

	app1 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "app1",
			Namespace:       "test",
			ResourceVersion: "1",
			UID:             "1234",
		},
	}

	app2 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "app2",
			Namespace:       "test",
			ResourceVersion: "1",
			UID:             "1234",
		},
	}

	t.Run("should prioritize terminate and defer latest spec-update", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		// Add a spec-update first
		app1.ResourceVersion = "10"
		specEv := es.ApplicationEvent(SpecUpdate, app1)
		evSender.Add(specEv)

		// Then add terminate; it should become current and previous spec should be deferred
		app1.ResourceVersion = "11"
		termEv := es.ApplicationEvent(TerminateOperation, app1)
		evSender.Add(termEv)

		latest := evSender.Get(createResourceID(app1.ObjectMeta))
		require.NotNil(t, latest)
		latest.mu.RLock()
		require.Equal(t, TerminateOperation.String(), latest.event.Type())
		require.NotNil(t, latest.deferredEvent)
		require.Equal(t, SpecUpdate.String(), latest.deferredEvent.Type())
		latest.mu.RUnlock()

		// When we ACK the terminate, the deferred spec should be promoted
		evSender.Remove(termEv)
		latest = evSender.Get(createResourceID(app1.ObjectMeta))
		require.NotNil(t, latest)
		latest.mu.RLock()
		require.Equal(t, SpecUpdate.String(), latest.event.Type())
		latest.mu.RUnlock()
	})

	t.Run("should keep only the newest deferred spec-update while terminate is current", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		// Current is terminate
		app1.ResourceVersion = "20"
		termEv := es.ApplicationEvent(TerminateOperation, app1)
		evSender.Add(termEv)

		// Multiple spec-updates arrive while terminate is pending
		app1.ResourceVersion = "21"
		oldSpec := es.ApplicationEvent(SpecUpdate, app1)
		evSender.Add(oldSpec)
		app1.ResourceVersion = "22"
		newSpec := es.ApplicationEvent(SpecUpdate, app1)
		evSender.Add(newSpec)

		latest := evSender.Get(createResourceID(app1.ObjectMeta))
		require.NotNil(t, latest)
		latest.mu.RLock()
		require.Equal(t, TerminateOperation.String(), latest.event.Type())
		require.NotNil(t, latest.deferredEvent)
		require.Equal(t, EventID(newSpec), EventID(latest.deferredEvent))
		latest.mu.RUnlock()

		// ACK terminate; latest spec should be promoted
		evSender.Remove(termEv)
		latest = evSender.Get(createResourceID(app1.ObjectMeta))
		require.NotNil(t, latest)
		latest.mu.RLock()
		require.Equal(t, SpecUpdate.String(), latest.event.Type())
		require.Equal(t, EventID(newSpec), EventID(latest.event))
		latest.mu.RUnlock()
	})

	t.Run("should handle consecutive terminates by upgrading current terminate", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		// First terminate
		app1.ResourceVersion = "30"
		term1 := es.ApplicationEvent(TerminateOperation, app1)
		evSender.Add(term1)

		// Second terminate arrives; it should replace current
		app1.ResourceVersion = "31"
		term2 := es.ApplicationEvent(TerminateOperation, app1)
		evSender.Add(term2)

		latest := evSender.Get(createResourceID(app1.ObjectMeta))
		require.NotNil(t, latest)
		latest.mu.RLock()
		require.Equal(t, TerminateOperation.String(), latest.event.Type())
		require.Equal(t, EventID(term2), EventID(latest.event))
		latest.mu.RUnlock()

		// ACK for the latest terminate should clear (no deferred set)
		evSender.Remove(term2)
		require.Nil(t, evSender.Get(createResourceID(app1.ObjectMeta)))
	})

	t.Run("should add/update/remove events from the queue", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		ev := es.ApplicationEvent(Create, app1)
		evSender.Add(ev)

		latestEvent := evSender.Get(ResourceID(ev))
		require.NotNil(t, latestEvent)
		require.Equal(t, latestEvent.event, ev)
		require.Nil(t, latestEvent.retryAfter)
		now := time.Now()
		latestEvent.retryAfter = &now

		// Add an Update event for the same resource
		app1.ResourceVersion = "2"
		ev = es.ApplicationEvent(Update, app1)
		evSender.Add(ev)

		latestEvent = evSender.Get(ResourceID(ev))
		require.NotNil(t, latestEvent)
		require.Equal(t, latestEvent.event, ev)
		require.Nil(t, latestEvent.retryAfter)

		// Try removing an event with the same resourceID but different eventID.
		app1.ResourceVersion = "3"
		newEv := es.ApplicationEvent(Update, app1)
		evSender.Remove(newEv)

		// The old event should not removed from the queue.
		latestEvent = evSender.Get(ResourceID(newEv))
		require.NotNil(t, latestEvent)
		require.Equal(t, latestEvent.event, ev)

		// The event will be removed only if both the resourceID and eventID matches.
		evSender.Remove(ev)
		latestEvent = evSender.Get(ResourceID(ev))
		require.Nil(t, latestEvent)
	})

	t.Run("should handle events from multiple resources", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		app1Events := []EventType{Create, Update, Delete}
		app2Events := []EventType{Create, Update, Update, Delete}

		for v, e := range app2Events {
			app2.ResourceVersion = fmt.Sprintf("%d", v)
			evSender.Add(es.ApplicationEvent(e, app2))
		}

		for v, e := range app1Events {
			app1.ResourceVersion = fmt.Sprintf("%d", v)
			evSender.Add(es.ApplicationEvent(e, app1))
		}

		require.Len(t, evSender.latestEvents, 2)
		latestApp1Event := evSender.Get(createResourceID(app1.ObjectMeta))
		require.NotNil(t, latestApp1Event)
		require.Equal(t, createEventID(app1.ObjectMeta), EventID(latestApp1Event.event))

		latestApp2Event := evSender.Get(createResourceID(app2.ObjectMeta))
		require.NotNil(t, latestApp2Event)
		require.Equal(t, createEventID(app2.ObjectMeta), EventID(latestApp2Event.event))
	})

	t.Run("should send waiting events to the stream", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// shouldn't send an event that is not being tracked
		evSender.sendEvent("random-id")
		require.Len(t, fs.events, 0)

		// shouldn't send an event that isn't past the retryAfter time.
		latestEvent := evSender.Get(resID)
		require.NotNil(t, latestEvent)
		retryAfter := time.Now().Add(1 * time.Hour)
		latestEvent.retryAfter = &retryAfter

		evSender.sendEvent(resID)
		require.Len(t, fs.events, 0)

		// should send a valid event to the stream
		retryAfter = time.Now().Add(-10 * time.Second)
		latestEvent.retryAfter = &retryAfter
		evSender.sendEvent(resID)
		require.Len(t, fs.events, 1)
		require.Equal(t, []string{createEventID(app1.ObjectMeta)}, fs.events[resID])
	})
}

type fakeStream struct {
	mu     sync.RWMutex
	events map[string][]string
}

func (fs *fakeStream) Send(event *eventstreamapi.Event) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	ev, err := FromWire(event.Event)
	if err != nil {
		return err
	}

	if fs.events == nil {
		fs.events = map[string][]string{}
	}

	fs.events[ResourceID(ev.event)] = append(fs.events[ResourceID(ev.event)], EventID(ev.event))
	return nil
}

func (fs *fakeStream) Context() context.Context {
	return context.Background()
}

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
