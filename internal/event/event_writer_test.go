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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		ev := es.ApplicationEvent(Create, app1)
		evSender.Add(ev)

		latestEvent := evSender.Get(ResourceID(ev))
		require.NotNil(t, latestEvent)
		require.Equal(t, latestEvent.event, ev)
		require.Nil(t, latestEvent.retryAfter)

		// Add a SpecUpdate event for the same resource
		app1.ResourceVersion = "2"
		newEv := es.ApplicationEvent(SpecUpdate, app1)
		evSender.Add(newEv)

		latestEvent = evSender.Get(ResourceID(ev))
		require.NotNil(t, latestEvent)
		require.Equal(t, latestEvent.event, ev)
		require.Nil(t, latestEvent.retryAfter)

		// Try removing an event with the same resourceID but different eventID.
		app1.ResourceVersion = "3"
		newEv = es.ApplicationEvent(SpecUpdate, app1)
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
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		app1Events := []EventType{Create, SpecUpdate, Delete}
		app2Events := []EventType{Create, SpecUpdate, SpecUpdate, Delete}

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
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// shouldn't send an event that is not being tracked
		evSender.sendEvent("random-id", gen)
		require.Len(t, fs.events, 0)

		// shouldn't send an event that isn't past the retryAfter time.
		latestEvent := evSender.Get(resID)
		require.NotNil(t, latestEvent)
		retryAfter := time.Now().Add(1 * time.Hour)
		latestEvent.retryAfter = &retryAfter

		evSender.retrySentEvent(resID, latestEvent, gen)
		require.Len(t, fs.events, 0)

		// should send a valid event to the stream
		retryAfter = time.Now().Add(-10 * time.Second)
		latestEvent.retryAfter = &retryAfter
		evSender.sendEvent(resID, gen)
		require.Len(t, fs.events, 1)
		require.Equal(t, []string{createEventID(app1.ObjectMeta)}, fs.events[resID])
	})

	t.Run("should prioritize DELETE events and clear queue", func(t *testing.T) {
		fs := &fakeStream{}
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		// Add Create and SpecUpdate events
		ev1 := es.ApplicationEvent(Create, app1)
		evSender.Add(ev1)

		app1.ResourceVersion = "2"
		ev2 := es.ApplicationEvent(SpecUpdate, app1)
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
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

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
					ev := es.ApplicationEvent(SpecUpdate, app)
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
					ev := es.ApplicationEvent(SpecUpdate, app)
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
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// Event should be in unsent queue
		require.Contains(t, evSender.unsentEvents, resID)
		require.NotContains(t, evSender.sentEvents, resID)

		// Send the event
		evSender.sendEvent(resID, gen)

		// Event should now be in sent map and removed from unsent
		require.NotContains(t, evSender.unsentEvents, resID)
		require.Contains(t, evSender.sentEvents, resID)
	})

	t.Run("should retry sent events with exponential backoff", func(t *testing.T) {
		fs := &fakeStream{}
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// Send the event once
		evSender.sendEvent(resID, gen)
		require.Len(t, fs.events[resID], 1)

		// Get the sent event
		sentMsg := evSender.sentEvents[resID]
		require.NotNil(t, sentMsg)
		require.NotNil(t, sentMsg.retryAfter)
		require.Equal(t, 0, sentMsg.retryCount)

		// Try to retry immediately - should not send
		evSender.retrySentEvent(resID, sentMsg, gen)
		require.Len(t, fs.events[resID], 1)

		// Set retryAfter to past time
		pastTime := time.Now().Add(-1 * time.Second)
		sentMsg.retryAfter = &pastTime

		// Now retry should work
		evSender.retrySentEvent(resID, sentMsg, gen)
		require.Len(t, fs.events[resID], 2)
		require.Equal(t, 1, sentMsg.retryCount)
	})

	t.Run("should give up after max retries", func(t *testing.T) {
		fs := &fakeStream{}
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// Send the event
		evSender.sendEvent(resID, gen)
		sentMsg := evSender.sentEvents[resID]
		require.NotNil(t, sentMsg)

		// Exhaust retries
		for i := 0; i <= maxEventRetries; i++ {
			pastTime := time.Now().Add(-1 * time.Second)
			sentMsg.retryAfter = &pastTime
			evSender.retrySentEvent(resID, sentMsg, gen)
		}

		// After max retries, event should be removed from sentEvents
		require.NotContains(t, evSender.sentEvents, resID)
	})

	t.Run("should not send ACK events to sentEvents", func(t *testing.T) {
		fs := &fakeStream{}
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

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
		evSender.sendEvent(resID, gen)

		// ACK should not be in sentEvents (doesn't need ACK confirmation)
		require.NotContains(t, evSender.sentEvents, resID)
		require.Len(t, fs.events[resID], 1)
	})

	t.Run("should not send heartbeat events to sentEvents (fire-and-forget)", func(t *testing.T) {
		fs := &fakeStream{}
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		// Create a heartbeat event using EventSource helper
		heartbeatEv := es.HeartbeatEvent(Ping)
		resID := ResourceID(heartbeatEv)

		evSender.Add(heartbeatEv)

		// Send the heartbeat event
		evSender.sendEvent(resID, gen)

		// Heartbeat should not be in sentEvents (fire-and-forget, no ACK tracking)
		require.NotContains(t, evSender.sentEvents, resID)
		// But it should have been sent to the stream
		require.Len(t, fs.events[resID], 1)
	})

	t.Run("heartbeat events should not accumulate in sentEvents over time", func(t *testing.T) {
		fs := &fakeStream{}
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		// Simulate multiple heartbeats being sent (like a real heartbeat interval)
		for i := 0; i < 10; i++ {
			heartbeatEv := es.HeartbeatEvent(Ping)
			resID := ResourceID(heartbeatEv)
			evSender.Add(heartbeatEv)
			evSender.sendEvent(resID, gen)
		}

		// sentEvents should be empty - no heartbeats should accumulate
		require.Empty(t, evSender.sentEvents, "Heartbeat events should not accumulate in sentEvents")
	})

	t.Run("should handle empty resource ID gracefully", func(t *testing.T) {
		fs := &fakeStream{}
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

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
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		result := evSender.Get("non-existent-resource-id")
		require.Nil(t, result)
	})

	t.Run("should return sent event before checking unsent queue in Get", func(t *testing.T) {
		fs := &fakeStream{}
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		// Reset app1 to version 1
		app1.ResourceVersion = "1"
		ev := es.ApplicationEvent(Create, app1)
		resID := createResourceID(app1.ObjectMeta)
		evSender.Add(ev)

		// Send the event (moves to sentEvents)
		evSender.sendEvent(resID, gen)

		// Verify the sent event has version 1
		sentEventID := EventID(ev)
		require.Contains(t, sentEventID, "_1")

		// Add another event for the same resource with version 2
		app1.ResourceVersion = "2"
		ev2 := es.ApplicationEvent(SpecUpdate, app1)
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
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		ev := es.ApplicationEvent(Create, app1)
		evSender.Add(ev)

		ctx, cancel := context.WithCancel(context.Background())

		// Start sending in background
		done := make(chan bool)
		go func() {
			evSender.SendWaitingEvents(ctx, gen)
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
		evSender, gen := NewEventWriter("test", fs)
		_ = gen

		resID := createResourceID(app1.ObjectMeta)

		// Add multiple updates
		for i := 1; i <= 5; i++ {
			app1.ResourceVersion = fmt.Sprintf("%d", i)
			ev := es.ApplicationEvent(SpecUpdate, app1)
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
	// sendErr, if set, is returned from every Send() call instead of
	// recording the event. Useful for simulating a dead transport.
	sendErr error
}

func (fs *fakeStream) Send(event *eventstreamapi.Event) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.sendErr != nil {
		return fs.sendErr
	}
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

func TestEventWriter_Depth(t *testing.T) {
	es := NewEventSource("test")
	app1 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "app1", Namespace: "test", UID: types.UID("u1")},
	}
	app2 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "app2", Namespace: "test", UID: types.UID("u2")},
	}

	t.Run("counts unsent + sent events", func(t *testing.T) {
		fs := &fakeStream{}
		ew, gen := NewEventWriter("test", fs)
		_ = gen
		require.Equal(t, 0, ew.Depth())

		ew.Add(es.ApplicationEvent(Create, app1))
		ew.Add(es.ApplicationEvent(Create, app2))
		require.Equal(t, 2, ew.Depth())

		// Send moves one app's event from unsent → sent (still counted).
		ew.sendEvent(ResourceID(es.ApplicationEvent(Create, app1)), gen)
		require.Equal(t, 2, ew.Depth())
	})
}

func TestEventWriter_SendErrorObserver(t *testing.T) {
	es := NewEventSource("test")
	app1 := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "app1", Namespace: "test", UID: types.UID("u1")},
	}

	t.Run("invokes observer with classified reason on transport-closing", func(t *testing.T) {
		fs := &fakeStream{
			sendErr: status.Error(codes.Unavailable, "transport is closing"),
		}
		ew, gen := NewEventWriter("test", fs)
		_ = gen

		var got SendErrorReason
		var calls int
		ew.SetSendErrorObserver(func(reason SendErrorReason, err error) {
			got = reason
			calls++
		})

		ev := es.ApplicationEvent(Create, app1)
		ew.Add(ev)
		ew.sendEvent(ResourceID(ev), gen)

		require.Equal(t, 1, calls)
		require.Equal(t, SendErrorTransportClosing, got)
	})

	t.Run("UpdateTarget clears observer", func(t *testing.T) {
		fs := &fakeStream{
			sendErr: status.Error(codes.Unavailable, "transport is closing"),
		}
		ew, _ := NewEventWriter("test", fs)

		var calls int
		ew.SetSendErrorObserver(func(reason SendErrorReason, err error) {
			calls++
		})

		// Swap target — observer must be cleared so it doesn't fire for the
		// new owner's stream. Use the new gen so Send actually executes.
		newGen := ew.UpdateTarget(&fakeStream{sendErr: status.Error(codes.Unavailable, "transport is closing")})

		ev := es.ApplicationEvent(Create, app1)
		ew.Add(ev)
		ew.sendEvent(ResourceID(ev), newGen)

		require.Equal(t, 0, calls)
	})

	t.Run("classifies context.Canceled separately", func(t *testing.T) {
		fs := &fakeStream{sendErr: context.Canceled}
		ew, gen := NewEventWriter("test", fs)
		_ = gen

		var got SendErrorReason
		ew.SetSendErrorObserver(func(reason SendErrorReason, _ error) {
			got = reason
		})

		ev := es.ApplicationEvent(Create, app1)
		ew.Add(ev)
		ew.sendEvent(ResourceID(ev), gen)
		require.Equal(t, SendErrorContextCanceled, got)
	})

	t.Run("does not notify observer if target was replaced after Send started", func(t *testing.T) {
		// Simulate the duplicate-Subscribe race: an old goroutine Sends
		// against the prior target, then UpdateTarget swaps in a new
		// stream + observer before the Send error reaches notifySendError.
		// The new observer must NOT be invoked for the old stream's failure.
		ew, oldGen := NewEventWriter("test", &fakeStream{})

		// The new owner (after reconnect) installs an observer.
		newGen := ew.UpdateTarget(&fakeStream{})
		require.NotEqual(t, oldGen, newGen)

		var calls int
		ew.SetSendErrorObserver(func(SendErrorReason, error) {
			calls++
		})

		// A slow Send goroutine that fired against the prior target tries
		// to surface its error, but oldGen is no longer current.
		ew.notifySendError(oldGen, status.Error(codes.Unavailable, "transport is closing"))
		require.Equal(t, 0, calls,
			"observer must not fire for a Send that targeted a replaced stream")

		// Sanity: notifySendError with the current gen does fire.
		ew.notifySendError(newGen, status.Error(codes.Unavailable, "transport is closing"))
		require.Equal(t, 1, calls)
	})

	t.Run("retrySentEvent skips retry-state mutation when gen is stale", func(t *testing.T) {
		// A stale send loop must NOT advance retryCount or push retryAfter
		// into the future after UpdateTarget moved the gen — that would
		// undo the immediate-retry timer reset UpdateTarget did for the
		// new owner.
		fs := &fakeStream{}
		ew, oldGen := NewEventWriter("test", fs)

		ev := es.ApplicationEvent(Create, app1)
		resID := ResourceID(ev)
		ew.Add(ev)
		ew.sendEvent(resID, oldGen)
		require.Contains(t, ew.sentEvents, resID)
		sentMsg := ew.sentEvents[resID]
		preCount := sentMsg.retryCount

		// New owner takes over: bumps gen and resets retryAfter to "now"
		// for immediate resend.
		_ = ew.UpdateTarget(&fakeStream{})
		// Make the entry due so a non-stale loop would actually retry.
		past := time.Now().Add(-time.Second)
		sentMsg.retryAfter = &past

		// Old loop tries to retry against its (now stale) gen.
		ew.retrySentEvent(resID, sentMsg, oldGen)

		require.Equal(t, preCount, sentMsg.retryCount,
			"stale loop must not advance retryCount")
		// retryAfter should still be the "now" we set above (old loop must
		// not push it forward).
		require.Equal(t, past, *sentMsg.retryAfter,
			"stale loop must not extend retryAfter into backoff")
	})

	t.Run("SendWaitingEvents stops once a successor takes over", func(t *testing.T) {
		// An old SendWaitingEvents loop must exit once UpdateTarget moves
		// the gen — otherwise it would race with the new owner's loop.
		fs := &fakeStream{}
		ew, oldGen := NewEventWriter("test", fs)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		done := make(chan struct{})
		go func() {
			ew.SendWaitingEvents(ctx, oldGen)
			close(done)
		}()

		// Successor swaps in a new stream.
		_ = ew.UpdateTarget(&fakeStream{})

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("SendWaitingEvents did not exit after UpdateTarget bumped gen")
		}
	})

	t.Run("ACK re-enqueued when gen advances before pop", func(t *testing.T) {
		// Gen advances before sendUnsentEvent enters: opening gen check fires
		// and the ACK stays in the unsent queue untouched.
		ew, oldGen := NewEventWriter("test", &fakeStream{})

		cev := cloudevents.NewEvent()
		cev.SetSource("test")
		cev.SetType(EventProcessed.String())
		cev.SetDataSchema(TargetEventAck.String())
		cev.SetExtension(eventID, "ack-1")
		cev.SetExtension(resourceID, "ack-resource")
		ew.Add(&cev)

		_ = ew.UpdateTarget(&fakeStream{})
		ew.sendUnsentEvent("ack-resource", oldGen)

		ew.mu.RLock()
		q, exists := ew.unsentEvents["ack-resource"]
		ew.mu.RUnlock()
		require.True(t, exists)
		require.False(t, q.isEmpty(), "ACK must remain for new owner")
	})

	t.Run("ACK re-enqueued when gen advances after pop but before Send", func(t *testing.T) {
		// The dangerous window: gen is current when the ACK is popped, but
		// UpdateTarget races between ew.mu.Unlock() and snapshotTargetForGen.
		// snapshotTargetForGen then returns false — the ACK must be re-enqueued
		// rather than silently dropped.
		//
		// testHookAfterUnlock fires after ew.mu is released and before
		// snapshotTargetForGen, giving us a deterministic synchronization point.

		hookReached := make(chan struct{})
		genAdvanced := make(chan struct{})

		ew, lease := NewEventWriter("test", &fakeStream{})
		ew.testHookAfterUnlock = func() {
			close(hookReached) // signal: pop done, lock released
			<-genAdvanced      // wait: UpdateTarget has advanced the gen
		}

		cev := cloudevents.NewEvent()
		cev.SetSource("test")
		cev.SetType(EventProcessed.String())
		cev.SetDataSchema(TargetEventAck.String())
		cev.SetExtension(eventID, "ack-2")
		cev.SetExtension(resourceID, "ack-resource2")
		ew.Add(&cev)

		done := make(chan struct{})
		go func() {
			ew.sendUnsentEvent("ack-resource2", lease)
			close(done)
		}()

		<-hookReached
		_ = ew.UpdateTarget(&fakeStream{}) // advances gen; snapshotTargetForGen will return false
		close(genAdvanced)
		<-done

		ew.mu.RLock()
		q, exists := ew.unsentEvents["ack-resource2"]
		ew.mu.RUnlock()
		require.True(t, exists, "ACK queue must exist after re-enqueue")
		require.False(t, q.isEmpty(), "ACK must be re-enqueued after post-pop gen advance")
	})

	t.Run("retrySentEvent also notifies observer", func(t *testing.T) {
		// First Send succeeds, then flip to failure to test the retry path.
		fs := &fakeStream{}
		ew, gen := NewEventWriter("test", fs)
		_ = gen

		ev := es.ApplicationEvent(Create, app1)
		resID := ResourceID(ev)
		ew.Add(ev)
		ew.sendEvent(resID, gen)
		require.Contains(t, ew.sentEvents, resID)

		// Now switch to failure mode and force a retry.
		fs.mu.Lock()
		fs.sendErr = status.Error(codes.Unavailable, "transport is closing")
		fs.mu.Unlock()

		var calls int
		ew.SetSendErrorObserver(func(reason SendErrorReason, _ error) {
			require.Equal(t, SendErrorTransportClosing, reason)
			calls++
		})

		sentMsg := ew.sentEvents[resID]
		past := time.Now().Add(-time.Second)
		sentMsg.retryAfter = &past
		ew.retrySentEvent(resID, sentMsg, gen)

		require.Equal(t, 1, calls)
	})
}
