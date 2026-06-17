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
	"fmt"
	"sync"
	"testing"

	internalevent "github.com/argoproj-labs/argocd-agent/internal/event"
	cloudevents "github.com/cloudevents/sdk-go/v2/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDedupableEvent(resourceID, data string) *cloudevents.Event {
	ev := cloudevents.New()
	ev.SetType(internalevent.SpecUpdate.String())
	ev.SetExtension("resourceid", resourceID)
	ev.SetExtension("eventid", fmt.Sprintf("%s_%s", resourceID, data))
	_ = ev.SetData(cloudevents.TextPlain, data)
	return &ev
}

func newStatusUpdateEvent(resourceID, data string) *cloudevents.Event {
	ev := cloudevents.New()
	ev.SetType(internalevent.StatusUpdate.String())
	ev.SetExtension("resourceid", resourceID)
	ev.SetExtension("eventid", fmt.Sprintf("%s_%s", resourceID, data))
	_ = ev.SetData(cloudevents.TextPlain, data)
	return &ev
}

func newNonDedupableEvent(resourceID, eventID, data string) *cloudevents.Event {
	ev := cloudevents.New()
	ev.SetType(internalevent.Create.String())
	ev.SetExtension("resourceid", resourceID)
	ev.SetExtension("eventid", eventID)
	_ = ev.SetData(cloudevents.TextPlain, data)
	return &ev
}

func TestDedupeQueue_DifferentResourcesNotDeduplicated(t *testing.T) {
	q := NewDedupeQueue("test", 100)

	ev1 := newDedupableEvent("app1_uid1", "v1")
	ev2 := newDedupableEvent("app2_uid2", "v1")

	q.Add(ev1)
	q.Add(ev2)

	assert.Equal(t, 2, q.Len())
}

func TestDedupeQueue_DifferentTypesNotDeduplicated(t *testing.T) {
	q := NewDedupeQueue("test", 100)

	ev1 := newDedupableEvent("app1_uid1", "v1")
	ev2 := newStatusUpdateEvent("app1_uid1", "v1")

	q.Add(ev1)
	q.Add(ev2)

	assert.Equal(t, 2, q.Len())
}

func TestDedupeQueue_NonDedupableEventsNeverCoalesce(t *testing.T) {
	q := NewDedupeQueue("test", 100)

	ev1 := newNonDedupableEvent("app1_uid1", "evt-1", "data1")
	ev2 := newNonDedupableEvent("app1_uid1", "evt-2", "data2")
	ev3 := newNonDedupableEvent("app1_uid1", "evt-3", "data3")

	q.Add(ev1)
	q.Add(ev2)
	q.Add(ev3)

	assert.Equal(t, 3, q.Len())
}

func TestDedupeQueue_FIFOOrdering(t *testing.T) {
	q := NewDedupeQueue("test", 100)

	ev1 := newNonDedupableEvent("app1", "evt-1", "first")
	ev2 := newNonDedupableEvent("app2", "evt-2", "second")
	ev3 := newNonDedupableEvent("app3", "evt-3", "third")

	q.Add(ev1)
	q.Add(ev2)
	q.Add(ev3)

	got1, _ := q.Get()
	q.Done(got1)
	got2, _ := q.Get()
	q.Done(got2)
	got3, _ := q.Get()
	q.Done(got3)

	var d1, d2, d3 string
	_ = got1.DataAs(&d1)
	_ = got2.DataAs(&d2)
	_ = got3.DataAs(&d3)
	assert.Equal(t, "first", d1)
	assert.Equal(t, "second", d2)
	assert.Equal(t, "third", d3)
}

func TestDedupeQueue_DedupeMovesToBack(t *testing.T) {
	q := NewDedupeQueue("test", 100)

	ev1 := newDedupableEvent("app1_uid1", "v1")
	ev2 := newDedupableEvent("app2_uid2", "v1")

	q.Add(ev1)
	q.Add(ev2)

	// Re-add app1 with new data — should move to back
	ev1Updated := newDedupableEvent("app1_uid1", "v2")
	q.Add(ev1Updated)

	assert.Equal(t, 2, q.Len())

	// First out should be app2 (app1 moved to back)
	got, _ := q.Get()
	q.Done(got)
	assert.Equal(t, "app2_uid2", internalevent.ResourceID(got))

	got, _ = q.Get()
	q.Done(got)
	assert.Equal(t, "app1_uid1", internalevent.ResourceID(got))
	var data string
	_ = got.DataAs(&data)
	assert.Equal(t, "v2", data, "should have latest payload")
}

func TestDedupeQueue_BoundedEviction(t *testing.T) {
	maxSize := 5
	q := NewDedupeQueue("test", maxSize)

	for i := 0; i < maxSize; i++ {
		ev := newNonDedupableEvent(fmt.Sprintf("app%d", i), fmt.Sprintf("evt-%d", i), fmt.Sprintf("data%d", i))
		q.Add(ev)
	}
	assert.Equal(t, maxSize, q.Len())

	// Adding one more should evict the oldest
	overflow := newNonDedupableEvent("appNew", "evt-new", "new-data")
	q.Add(overflow)
	assert.Equal(t, maxSize, q.Len())

	// The oldest (app0) should have been evicted; first available is app1
	got, _ := q.Get()
	q.Done(got)
	var data string
	_ = got.DataAs(&data)
	assert.Equal(t, "data1", data)
}

func TestDedupeQueue_DuplicateDoesNotEvict(t *testing.T) {
	maxSize := 3
	q := NewDedupeQueue("test", maxSize)

	ev1 := newDedupableEvent("app1_uid1", "v1")
	ev2 := newDedupableEvent("app2_uid2", "v1")
	ev3 := newDedupableEvent("app3_uid3", "v1")

	q.Add(ev1)
	q.Add(ev2)
	q.Add(ev3)
	assert.Equal(t, maxSize, q.Len())

	// Re-adding app1 (deduplicate) should NOT evict anything
	ev1Updated := newDedupableEvent("app1_uid1", "v2")
	q.Add(ev1Updated)
	assert.Equal(t, maxSize, q.Len())

	// All three resources should still be present
	got1, _ := q.Get()
	q.Done(got1)
	got2, _ := q.Get()
	q.Done(got2)
	got3, _ := q.Get()
	q.Done(got3)

	resources := []string{
		internalevent.ResourceID(got1),
		internalevent.ResourceID(got2),
		internalevent.ResourceID(got3),
	}
	assert.Contains(t, resources, "app1_uid1")
	assert.Contains(t, resources, "app2_uid2")
	assert.Contains(t, resources, "app3_uid3")
}

func TestDedupeQueue_GetReturnsLatestAfterMultipleUpdates(t *testing.T) {
	q := NewDedupeQueue("test", 100)

	for i := 0; i < 10; i++ {
		ev := newDedupableEvent("app1_uid1", fmt.Sprintf("version-%d", i))
		q.Add(ev)
	}

	assert.Equal(t, 1, q.Len())

	got, _ := q.Get()
	var data string
	_ = got.DataAs(&data)
	assert.Equal(t, "version-9", data)
}

func TestDedupeQueue_Done(t *testing.T) {
	q := NewDedupeQueue("test", 100)

	ev := newDedupableEvent("app1_uid1", "v1")
	q.Add(ev)

	got, _ := q.Get()
	assert.Equal(t, 0, q.Len())
	q.Done(got)

	// After Done, adding a new event for the same resource should work fresh
	ev2 := newDedupableEvent("app1_uid1", "v2")
	q.Add(ev2)
	assert.Equal(t, 1, q.Len())
}

func TestDedupeQueue_ShutDown(t *testing.T) {
	q := NewDedupeQueue("test", 100)

	ev := newDedupableEvent("app1_uid1", "v1")
	q.Add(ev)

	q.ShutDown()

	// Pending items are still returned after shutdown
	got, shutdown := q.Get()
	assert.False(t, shutdown)
	assert.NotNil(t, got)
	q.Done(got)

	// Once drained, Get signals shutdown
	_, shutdown = q.Get()
	assert.True(t, shutdown)
}

func TestDedupeQueue_EmptyQueueLen(t *testing.T) {
	q := NewDedupeQueue("test", 100)
	assert.Equal(t, 0, q.Len())
}

func TestDedupeQueue_MixedDedupableAndNonDedupable(t *testing.T) {
	q := NewDedupeQueue("test", 100)

	spec1 := newDedupableEvent("app1_uid1", "spec-v1")
	spec2 := newDedupableEvent("app1_uid1", "spec-v2")
	create1 := newNonDedupableEvent("app1_uid1", "create-1", "create-data")

	q.Add(spec1)
	q.Add(create1)
	q.Add(spec2)

	// spec events coalesce (1 slot), create is separate (1 slot) = 2 total
	assert.Equal(t, 2, q.Len())
}

func TestDedupeQueue_NotifyOnAdd(t *testing.T) {
	dq := NewDedupeQueue("test", 100).(*dedupeQueue)

	ev := newDedupableEvent("app1_uid1", "v1")
	q := dq
	q.Add(ev)

	select {
	case <-dq.notify:
		// expected
	default:
		t.Fatal("expected notification on Add")
	}
}

func TestDedupeQueue_ConcurrentAdds(t *testing.T) {
	q := NewDedupeQueue("test", 1000)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev := newDedupableEvent(fmt.Sprintf("app%d_uid%d", i, i), fmt.Sprintf("v%d", i))
			q.Add(ev)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 100, q.Len())
}

func TestDedupeQueue_ConcurrentDeduplication(t *testing.T) {
	q := NewDedupeQueue("test", 1000)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev := newDedupableEvent("app1_uid1", fmt.Sprintf("v%d", i))
			q.Add(ev)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 1, q.Len())

	got, _ := q.Get()
	require.NotNil(t, got)
}

func TestReorderQueue_Push_Pop(t *testing.T) {
	q := newReorderQueue[string]()

	q.Push("a")
	q.Push("b")
	q.Push("c")

	assert.Equal(t, 3, q.Len())
	assert.Equal(t, "a", q.Pop())
	assert.Equal(t, "b", q.Pop())
	assert.Equal(t, "c", q.Pop())
	assert.Equal(t, 0, q.Len())
}

func TestReorderQueue_Touch_MovesToBack(t *testing.T) {
	q := newReorderQueue[string]()

	q.Push("a")
	q.Push("b")
	q.Push("c")

	q.Touch("a")

	assert.Equal(t, 3, q.Len())
	assert.Equal(t, "b", q.Pop())
	assert.Equal(t, "c", q.Pop())
	assert.Equal(t, "a", q.Pop())
}

func TestReorderQueue_Touch_MiddleElement(t *testing.T) {
	q := newReorderQueue[string]()

	q.Push("a")
	q.Push("b")
	q.Push("c")
	q.Push("d")

	q.Touch("b")

	assert.Equal(t, "a", q.Pop())
	assert.Equal(t, "c", q.Pop())
	assert.Equal(t, "d", q.Pop())
	assert.Equal(t, "b", q.Pop())
}

func TestReorderQueue_Touch_LastElement(t *testing.T) {
	q := newReorderQueue[string]()

	q.Push("a")
	q.Push("b")
	q.Push("c")

	q.Touch("c")

	// "c" is already last — order shouldn't change
	assert.Equal(t, "a", q.Pop())
	assert.Equal(t, "b", q.Pop())
	assert.Equal(t, "c", q.Pop())
}

func TestReorderQueue_Touch_NonExistent(t *testing.T) {
	q := newReorderQueue[string]()

	q.Push("a")
	q.Push("b")

	q.Touch("z") // should be a no-op

	assert.Equal(t, 2, q.Len())
	assert.Equal(t, "a", q.Pop())
	assert.Equal(t, "b", q.Pop())
}
