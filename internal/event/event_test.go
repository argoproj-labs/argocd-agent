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
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
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
