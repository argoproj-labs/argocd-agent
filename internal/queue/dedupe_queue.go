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
	"sync"

	internalevent "github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/cloudevents/sdk-go/v2/event"
	"k8s.io/client-go/util/workqueue"
)

// EventKey is the key that goes into the workqueue
type EventKey struct {
	ResourceID string
	EventType  string
	EventID    string
}

// reorderQueue is used to customize the functionality of the default workqueue.
// When a duplicate item is added (via Touch), it removes the existing entry and
// appends the item to the tail of the queue.
type reorderQueue[T comparable] struct {
	items []T
	// index is a map of item to its index in the queue
	index map[T]int
}

func newReorderQueue[T comparable]() workqueue.Queue[T] {
	return &reorderQueue[T]{
		index: make(map[T]int),
	}
}

func (q *reorderQueue[T]) Push(item T) {
	q.index[item] = len(q.items)
	q.items = append(q.items, item)
}

func (q *reorderQueue[T]) Pop() (item T) {
	item = q.items[0]
	delete(q.index, item)

	q.items[0] = *new(T)
	q.items = q.items[1:]

	// Rebuild indices after shift
	for i, it := range q.items {
		q.index[it] = i
	}
	return item
}

// Touch is a hook that is invoked when queue.Add is called with an item that already exists in the queue.
// It is only called when the item is not being processed i.e in dirty set and not in processing set.
func (q *reorderQueue[T]) Touch(item T) {
	idx, ok := q.index[item]
	if !ok {
		return
	}
	// Remove from current position
	q.items = append(q.items[:idx], q.items[idx+1:]...)
	// Re-add at the end
	q.items = append(q.items, item)
	// Rebuild indices from the moved position onward
	for i := idx; i < len(q.items); i++ {
		q.index[q.items[i]] = i
	}
}

func (q *reorderQueue[T]) Len() int {
	return len(q.items)
}

type dedupeQueue struct {
	queue workqueue.TypedInterface[EventKey]

	mu           sync.Mutex
	latestEvents map[EventKey]*event.Event
	eventKeys    map[*event.Event]EventKey

	notify chan struct{}
}

func NewDedupeQueue(name string) WorkQueue {
	return &dedupeQueue{
		queue: workqueue.NewTypedWithConfig(workqueue.TypedQueueConfig[EventKey]{
			Name: name,
			// Use a custom internal queue that removes duplicates and reorders new items to the tail.
			Queue: newReorderQueue[EventKey](),
		}),
		latestEvents: make(map[EventKey]*event.Event),
		eventKeys:    make(map[*event.Event]EventKey),
		notify:       make(chan struct{}, 10),
	}
}

func getKey(ev *event.Event) EventKey {
	resID := internalevent.ResourceID(ev)
	evType := ev.Type()

	if canDedupe(ev) {
		return EventKey{
			ResourceID: resID,
			EventType:  evType,
		}
	}

	// Non-dedupable events get a unique key
	return EventKey{
		ResourceID: resID,
		EventType:  evType,
		EventID:    internalevent.EventID(ev),
	}
}

func (q *dedupeQueue) Add(item *event.Event) {
	key := getKey(item)

	q.mu.Lock()
	oldEvent := q.latestEvents[key]
	q.latestEvents[key] = item
	q.eventKeys[item] = key
	if oldEvent != nil && oldEvent != item {
		delete(q.eventKeys, oldEvent)
	}
	q.mu.Unlock()

	q.queue.Add(key)
	select {
	case q.notify <- struct{}{}:
	default:
		return
	}
}

func (q *dedupeQueue) Get() (*event.Event, bool) {
	for {
		key, shutdown := q.queue.Get()
		if shutdown {
			return nil, true
		}

		q.mu.Lock()
		ev := q.latestEvents[key]
		delete(q.latestEvents, key)
		q.mu.Unlock()

		if ev != nil {
			return ev, false
		}

		// Phantom key: the workqueue's dirty-set re-pushed this key after
		// Done(), but the event data was already consumed by a prior Get()
		// that raced with a concurrent Add() for the same dedup key.
		// Release the key from the processing set so it doesn't get stuck.
		q.queue.Done(key)
	}
}

func (q *dedupeQueue) Done(item *event.Event) {
	q.mu.Lock()
	key, ok := q.eventKeys[item]
	if ok {
		delete(q.eventKeys, item)
		// Don't remove the latest item if Done is called for an older event
		if q.latestEvents[key] == item {
			delete(q.latestEvents, key)
		}
	}
	q.mu.Unlock()

	if ok {
		q.queue.Done(key)
	}
}

func (q *dedupeQueue) ShutDown() {
	q.queue.ShutDown()
}

func (q *dedupeQueue) Len() int {
	return q.queue.Len()
}

// deduplicationDisabled controls whether event deduplication is globally
// disabled. Set via SetDeduplicationDisabled before any queues are created.
var deduplicationDisabled bool

// SetDeduplicationDisabled is used to globally disable queue-level event deduplication.
func SetDeduplicationDisabled(disabled bool) {
	deduplicationDisabled = disabled
}

// canDedupe returns true if the event type supports de-duplication.
func canDedupe(ev *event.Event) bool {
	if deduplicationDisabled {
		return false
	}

	evType := ev.Type()
	return evType == internalevent.SpecUpdate.String() || evType == internalevent.StatusUpdate.String()
}
