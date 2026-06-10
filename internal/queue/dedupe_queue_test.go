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

package queue

import (
	"strconv"
	"testing"

	internalevent "github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/stretchr/testify/assert"
)

func newTestEvent(id string, eventType string, resourceID string) *event.Event {
	ev := event.New()
	ev.SetID(id)
	ev.SetType(eventType)
	if resourceID != "" {
		ev.SetExtension("resourceid", resourceID)
	}
	return &ev
}

func TestDedupeQueue_BasicFIFO(t *testing.T) {
	t.Run("Events dequeue in FIFO order", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		ev1 := newTestEvent("1", internalevent.Create.String(), "app-a")
		ev2 := newTestEvent("2", internalevent.Create.String(), "app-b")
		ev3 := newTestEvent("3", internalevent.Delete.String(), "app-c")

		q.Add(ev1)
		q.Add(ev2)
		q.Add(ev3)

		assert.Equal(t, 3, q.Len())

		got, shutdown := q.Get()
		assert.False(t, shutdown)
		assert.Equal(t, "1", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "2", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "3", got.ID())

		assert.Equal(t, 0, q.Len())
	})
}

func TestDedupeQueue_SpecUpdateDedup(t *testing.T) {
	t.Run("Newer SpecUpdate replaces older for same resource", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		ev1 := newTestEvent("v1", internalevent.SpecUpdate.String(), "app-x_uid1")
		ev2 := newTestEvent("v2", internalevent.SpecUpdate.String(), "app-x_uid1")
		ev3 := newTestEvent("v3", internalevent.SpecUpdate.String(), "app-x_uid1")

		q.Add(ev1)
		q.Add(ev2)
		q.Add(ev3)

		assert.Equal(t, 1, q.Len())

		got, _ := q.Get()
		assert.Equal(t, "v3", got.ID())
	})

	t.Run("SpecUpdates for different resources are not deduped", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		ev1 := newTestEvent("v1", internalevent.SpecUpdate.String(), "app-x_uid1")
		ev2 := newTestEvent("v2", internalevent.SpecUpdate.String(), "app-y_uid2")

		q.Add(ev1)
		q.Add(ev2)

		assert.Equal(t, 2, q.Len())

		got, _ := q.Get()
		assert.Equal(t, "v1", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "v2", got.ID())
	})
}

func TestDedupeQueue_StatusUpdateDedup(t *testing.T) {
	t.Run("Newer StatusUpdate replaces older for same resource", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		ev1 := newTestEvent("s1", internalevent.StatusUpdate.String(), "app-x_uid1")
		ev2 := newTestEvent("s2", internalevent.StatusUpdate.String(), "app-x_uid1")

		q.Add(ev1)
		q.Add(ev2)

		assert.Equal(t, 1, q.Len())

		got, _ := q.Get()
		assert.Equal(t, "s2", got.ID())
	})
}

func TestDedupeQueue_DifferentTypesNotDeduped(t *testing.T) {
	t.Run("SpecUpdate and StatusUpdate for same resource are both kept (different types)", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		evSpec := newTestEvent("spec1", internalevent.SpecUpdate.String(), "app-x_uid1")
		evStatus := newTestEvent("status1", internalevent.StatusUpdate.String(), "app-x_uid1")

		q.Add(evSpec)
		q.Add(evStatus)

		assert.Equal(t, 2, q.Len())

		got, _ := q.Get()
		assert.Equal(t, "spec1", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "status1", got.ID())
	})

	t.Run("Create event is never deduped even for same resource", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		evCreate := newTestEvent("c1", internalevent.Create.String(), "app-x_uid1")
		evSpec := newTestEvent("spec1", internalevent.SpecUpdate.String(), "app-x_uid1")

		q.Add(evCreate)
		q.Add(evSpec)

		assert.Equal(t, 2, q.Len())

		got, _ := q.Get()
		assert.Equal(t, "c1", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "spec1", got.ID())
	})

	t.Run("Delete event is never deduped", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		evDel1 := newTestEvent("d1", internalevent.Delete.String(), "app-x_uid1")
		evDel2 := newTestEvent("d2", internalevent.Delete.String(), "app-x_uid1")

		q.Add(evDel1)
		q.Add(evDel2)

		assert.Equal(t, 2, q.Len())

		got, _ := q.Get()
		assert.Equal(t, "d1", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "d2", got.ID())
	})
}

func TestDedupeQueue_MoveToTail(t *testing.T) {
	t.Run("Deduped event moves to tail preserving order", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")

		evSpecX := newTestEvent("spec-x-v1", internalevent.SpecUpdate.String(), "app-x_uid1")
		evCreate := newTestEvent("create-y", internalevent.Create.String(), "app-y_uid2")
		evSpecXv2 := newTestEvent("spec-x-v2", internalevent.SpecUpdate.String(), "app-x_uid1")

		q.Add(evSpecX)
		q.Add(evCreate)
		// This should remove evSpecX from position 0 and append evSpecXv2 at tail
		q.Add(evSpecXv2)

		assert.Equal(t, 2, q.Len())

		got, _ := q.Get()
		assert.Equal(t, "create-y", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "spec-x-v2", got.ID())
	})
}

func TestDedupeQueue_NonDedupEligibleEvents(t *testing.T) {
	t.Run("Heartbeat events are never deduped", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		ev1 := newTestEvent("hb1", internalevent.Ping.String(), "uuid-1")
		ev2 := newTestEvent("hb2", internalevent.Ping.String(), "uuid-2")

		q.Add(ev1)
		q.Add(ev2)

		assert.Equal(t, 2, q.Len())
	})

	t.Run("Events with empty resourceID are not deduped", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		ev1 := newTestEvent("1", internalevent.SpecUpdate.String(), "")
		ev2 := newTestEvent("2", internalevent.SpecUpdate.String(), "")

		q.Add(ev1)
		q.Add(ev2)

		assert.Equal(t, 2, q.Len())
	})
}

func TestDedupeQueue_MaxSize(t *testing.T) {
	t.Run("Oldest item is dropped when max size is exceeded", func(t *testing.T) {
		maxSize := 5
		q := newDedupeQueue(maxSize, "test")

		for i := 1; i <= maxSize; i++ {
			q.Add(newTestEvent(strconv.Itoa(i), internalevent.Create.String(), "app-"+strconv.Itoa(i)))
		}

		assert.Equal(t, maxSize, q.Len())

		q.Add(newTestEvent("6", internalevent.Create.String(), "app-6"))

		assert.Equal(t, maxSize, q.Len())

		got, _ := q.Get()
		assert.Equal(t, "2", got.ID())
	})
}

func TestDedupeQueue_Done(t *testing.T) {
	t.Run("Done is a no-op", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		ev := newTestEvent("1", internalevent.Create.String(), "app-a")
		q.Add(ev)

		got, _ := q.Get()
		q.Done(got) // should not panic
		assert.Equal(t, 0, q.Len())
	})
}

func TestDedupeQueue_ShutDown(t *testing.T) {
	t.Run("Get returns shutdown signal after ShutDown", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		q.ShutDown()

		_, shutdown := q.Get()
		assert.True(t, shutdown)
	})

	t.Run("Double ShutDown does not panic", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		q.ShutDown()
		assert.NotPanics(t, func() { q.ShutDown() })
	})

	t.Run("Add after ShutDown does not panic and is ignored", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")
		q.ShutDown()
		assert.NotPanics(t, func() {
			q.Add(newTestEvent("1", internalevent.SpecUpdate.String(), "app-x"))
		})
		assert.Equal(t, 0, q.Len())
	})
}

func TestDedupeQueue_MixedScenario(t *testing.T) {
	t.Run("Mixed dedup-eligible and non-dedup-eligible events", func(t *testing.T) {
		q := newDedupeQueue(1000, "test")

		q.Add(newTestEvent("create-x", internalevent.Create.String(), "app-x_uid1"))
		q.Add(newTestEvent("spec-x-v1", internalevent.SpecUpdate.String(), "app-x_uid1"))
		q.Add(newTestEvent("status-y-v1", internalevent.StatusUpdate.String(), "app-y_uid2"))
		q.Add(newTestEvent("spec-x-v2", internalevent.SpecUpdate.String(), "app-x_uid1"))
		q.Add(newTestEvent("status-y-v2", internalevent.StatusUpdate.String(), "app-y_uid2"))
		q.Add(newTestEvent("delete-z", internalevent.Delete.String(), "app-z_uid3"))

		// Expected order:
		// create-x (non-dedup-eligible, stays at original position)
		// spec-x-v2 (replaced spec-x-v1, moved to tail of where spec-x-v1 was... no, moved to tail)
		// status-y-v2 (replaced status-y-v1, moved to tail)
		// delete-z (non-dedup-eligible, appended)

		assert.Equal(t, 4, q.Len())

		got, _ := q.Get()
		assert.Equal(t, "create-x", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "spec-x-v2", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "status-y-v2", got.ID())

		got, _ = q.Get()
		assert.Equal(t, "delete-z", got.ID())

		assert.Equal(t, 0, q.Len())
	})
}
