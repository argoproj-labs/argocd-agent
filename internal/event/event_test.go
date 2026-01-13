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

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

	t.Run("should add/update/remove events from the queue", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		ev := es.ApplicationEvent(Create, app1)
		evSender.Add(ev)

		latestEvent := evSender.Get(ResourceID(ev))
		require.NotNil(t, latestEvent)
		require.Equal(t, latestEvent.event, ev)
		require.Nil(t, latestEvent.retryAfter)

		// Add an Update event for the same resource
		app1.ResourceVersion = "2"
		newEv := es.ApplicationEvent(Update, app1)
		evSender.Add(newEv)

		latestEvent = evSender.Get(ResourceID(ev))
		require.NotNil(t, latestEvent)
		require.Equal(t, latestEvent.event, ev)
		require.Nil(t, latestEvent.retryAfter)

		// Try removing an event with the same resourceID but different eventID.
		app1.ResourceVersion = "3"
		newEv = es.ApplicationEvent(Update, app1)
		evSender.Remove(newEv)

		// The old event should not removed from the queue.
		latestEvent = evSender.Get(ResourceID(newEv))
		require.NotNil(t, latestEvent)
		require.Equal(t, latestEvent.event, ev)

		// The event will be removed only if both the resourceID and eventID matches.
		resID := ResourceID(ev)
		require.Equal(t, 2, len(evSender.unsentEvents[resID].items))
		evSender.Remove(ev)
		latestEvent = evSender.Get(resID)
		require.Equal(t, 1, len(evSender.unsentEvents[resID].items))
		require.NotEqual(t, ev, latestEvent.event)
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

		require.Len(t, evSender.unsentEvents, 2)
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

		evSender.retrySentEvent(resID, latestEvent)
		require.Len(t, fs.events, 0)

		// should send a valid event to the stream
		retryAfter = time.Now().Add(-10 * time.Second)
		latestEvent.retryAfter = &retryAfter
		evSender.sendEvent(resID)
		require.Len(t, fs.events, 1)
		require.Equal(t, []string{createEventID(app1.ObjectMeta)}, fs.events[resID])
	})

	t.Run("should prioritize DELETE events and clear queue", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		// Add Create and Update events
		ev1 := es.ApplicationEvent(Create, app1)
		evSender.Add(ev1)

		app1.ResourceVersion = "2"
		ev2 := es.ApplicationEvent(Update, app1)
		evSender.Add(ev2)

		resID := createResourceID(app1.ObjectMeta)
		require.NotNil(t, evSender.Get(resID))

		// Add DELETE event - should clear all previous events
		app1.ResourceVersion = "3"
		deleteEv := es.ApplicationEvent(Delete, app1)
		evSender.Add(deleteEv)

		// DELETE should be the only event in the queue
		latestEvent := evSender.Get(resID)
		require.NotNil(t, latestEvent)
		require.Equal(t, 1, len(evSender.unsentEvents))
		require.Equal(t, 1, len(evSender.unsentEvents[resID].items))
		require.Equal(t, Delete.String(), latestEvent.event.Type())

		// Sent events should also be cleared
		require.Empty(t, evSender.sentEvents)
	})

	t.Run("should handle concurrent adds and removes", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		var wg sync.WaitGroup
		numGoroutines := 10
		numOpsPerGoroutine := 100

		// Concurrent adds
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < numOpsPerGoroutine; j++ {
					app := &v1alpha1.Application{
						ObjectMeta: metav1.ObjectMeta{
							Name:            fmt.Sprintf("app-%d", id),
							Namespace:       "test",
							ResourceVersion: fmt.Sprintf("%d", j),
							UID:             types.UID(fmt.Sprintf("%d", id)),
						},
					}
					ev := es.ApplicationEvent(Update, app)
					evSender.Add(ev)
				}
			}(i)
		}

		// Concurrent removes
		for i := 0; i < numGoroutines/2; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				time.Sleep(10 * time.Millisecond)
				for j := 0; j < numOpsPerGoroutine/2; j++ {
					app := &v1alpha1.Application{
						ObjectMeta: metav1.ObjectMeta{
							Name:            fmt.Sprintf("app-%d", id),
							Namespace:       "test",
							ResourceVersion: fmt.Sprintf("%d", j),
							UID:             types.UID(fmt.Sprintf("%d", id)),
						},
					}
					ev := es.ApplicationEvent(Update, app)
					evSender.Remove(ev)
				}
			}(i)
		}

		wg.Wait()

		// Should have events without panics or race conditions
		require.NotEmpty(t, evSender.unsentEvents)
	})

	t.Run("should move events from unsent to sent on first send", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// Event should be in unsent queue
		require.Contains(t, evSender.unsentEvents, resID)
		require.NotContains(t, evSender.sentEvents, resID)

		// Send the event
		evSender.sendEvent(resID)

		// Event should now be in sent map and removed from unsent
		require.NotContains(t, evSender.unsentEvents, resID)
		require.Contains(t, evSender.sentEvents, resID)
	})

	t.Run("should retry sent events with exponential backoff", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// Send the event once
		evSender.sendEvent(resID)
		require.Len(t, fs.events[resID], 1)

		// Get the sent event
		sentMsg := evSender.sentEvents[resID]
		require.NotNil(t, sentMsg)
		require.NotNil(t, sentMsg.retryAfter)
		require.Equal(t, 0, sentMsg.retryCount)

		// Try to retry immediately - should not send
		evSender.retrySentEvent(resID, sentMsg)
		require.Len(t, fs.events[resID], 1)

		// Set retryAfter to past time
		pastTime := time.Now().Add(-1 * time.Second)
		sentMsg.retryAfter = &pastTime

		// Now retry should work
		evSender.retrySentEvent(resID, sentMsg)
		require.Len(t, fs.events[resID], 2)
		require.Equal(t, 1, sentMsg.retryCount)
	})

	t.Run("should give up after max retries", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// Send the event
		evSender.sendEvent(resID)
		sentMsg := evSender.sentEvents[resID]
		require.NotNil(t, sentMsg)

		// Exhaust retries
		maxRetries := sentMsg.backoff.Steps
		for i := 0; i < maxRetries; i++ {
			pastTime := time.Now().Add(-1 * time.Second)
			sentMsg.retryAfter = &pastTime
			evSender.retrySentEvent(resID, sentMsg)
		}

		// After max retries, event should be removed from sentEvents
		require.NotContains(t, evSender.sentEvents, resID)
	})

	t.Run("should not send ACK events to sentEvents", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		// Create an ACK event
		cev := cloudevents.NewEvent()
		cev.SetSource("test")
		cev.SetType(EventProcessed.String())
		cev.SetDataSchema(TargetEventAck.String())
		cev.SetExtension(eventID, "test-ack")
		cev.SetExtension(resourceID, "test-resource")

		evSender.Add(&cev)
		resID := "test-resource"

		// Send the ACK event
		evSender.sendEvent(resID)

		// ACK should not be in sentEvents (doesn't need ACK confirmation)
		require.NotContains(t, evSender.sentEvents, resID)
		require.Len(t, fs.events[resID], 1)
	})

	t.Run("should not send heartbeat events to sentEvents (fire-and-forget)", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		// Create a heartbeat event using EventSource helper
		heartbeatEv := es.HeartbeatEvent(Ping)
		resID := ResourceID(heartbeatEv)

		evSender.Add(heartbeatEv)

		// Send the heartbeat event
		evSender.sendEvent(resID)

		// Heartbeat should not be in sentEvents (fire-and-forget, no ACK tracking)
		require.NotContains(t, evSender.sentEvents, resID)
		// But it should have been sent to the stream
		require.Len(t, fs.events[resID], 1)
	})

	t.Run("heartbeat events should not accumulate in sentEvents over time", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		// Simulate multiple heartbeats being sent (like a real heartbeat interval)
		for i := 0; i < 10; i++ {
			heartbeatEv := es.HeartbeatEvent(Ping)
			resID := ResourceID(heartbeatEv)
			evSender.Add(heartbeatEv)
			evSender.sendEvent(resID)
		}

		// sentEvents should be empty - no heartbeats should accumulate
		require.Empty(t, evSender.sentEvents, "Heartbeat events should not accumulate in sentEvents")
	})

	t.Run("should handle empty resource ID gracefully", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		// Create an event manually with empty resourceID extension
		cev := cloudevents.NewEvent()
		cev.SetSource("test")
		cev.SetType(Create.String())
		cev.SetDataSchema(TargetApplication.String())
		cev.SetExtension(eventID, "test-event-id")
		// Explicitly set resourceID to empty string
		cev.SetExtension(resourceID, "")

		evSender.Add(&cev)

		// Should not add event with empty resourceID
		require.Empty(t, evSender.unsentEvents)
	})

	t.Run("should handle Get for non-existent resource", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		result := evSender.Get("non-existent-resource-id")
		require.Nil(t, result)
	})

	t.Run("should return sent event before checking unsent queue in Get", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		// Reset app1 to version 1
		app1.ResourceVersion = "1"
		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// Send the event (moves to sentEvents)
		evSender.sendEvent(resID)

		// Verify the sent event has version 1
		sentEventID := EventID(ev)
		require.Contains(t, sentEventID, "_1")

		// Add another event for the same resource with version 2
		app1.ResourceVersion = "2"
		ev2 := es.ApplicationEvent(Update, app1)
		evSender.Add(ev2)

		// Get should return the sent event (version 1), not the unsent one (version 2)
		result := evSender.Get(resID)
		require.NotNil(t, result)
		resultEventID := EventID(result.event)
		require.Equal(t, sentEventID, resultEventID, "Should return sent event with version 1")
		require.Contains(t, resultEventID, "_1", "Returned event should be version 1")
	})

	t.Run("should handle SendWaitingEvents with context cancellation", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		ev := es.ApplicationEvent(Create, app1)
		evSender.Add(ev)

		ctx, cancel := context.WithCancel(context.Background())

		// Start sending in background
		done := make(chan bool)
		go func() {
			evSender.SendWaitingEvents(ctx)
			done <- true
		}()

		// Give it time to send
		time.Sleep(200 * time.Millisecond)

		// Cancel context
		cancel()

		// Should return promptly
		select {
		case <-done:
			// Success - function returned
		case <-time.After(1 * time.Second):
			t.Fatal("SendWaitingEvents did not return after context cancellation")
		}
	})

	t.Run("should coalesce multiple updates for same resource", func(t *testing.T) {
		fs := &fakeStream{}
		evSender := NewEventWriter(fs)

		resID := createResourceID(app1.ObjectMeta)

		// Add multiple updates
		for i := 1; i <= 5; i++ {
			app1.ResourceVersion = fmt.Sprintf("%d", i)
			ev := es.ApplicationEvent(Update, app1)
			evSender.Add(ev)
		}

		// Should have coalesced to single event with latest version
		latestEvent := evSender.Get(resID)
		require.NotNil(t, latestEvent)
		eventID := EventID(latestEvent.event)
		require.Contains(t, eventID, "_5") // Should be version 5
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
