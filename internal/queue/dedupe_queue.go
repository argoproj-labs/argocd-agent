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
	"time"

	internalevent "github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/cloudevents/sdk-go/v2/event"
)

// dedupeQueue is a bounded FIFO queue that de-duplicates resource events. When a new event
// arrives for a resource of the same type, the older events are removed and the
// newer event is appended to the tail of the queue.
type dedupeQueue struct {
	mu         sync.Mutex
	items      []*queueItem
	notify     chan struct{}
	name       string
	maxSize    int
	isShutdown bool
	metrics    *metrics.DedupeQueueMetrics
}

type queueItem struct {
	event      *event.Event
	enqueuedAt time.Time
}

// NewDedupeQueueForTest creates a dedupeQueue for use in tests.
func NewDedupeQueueForTest(maxSize int) *dedupeQueue {
	return newDedupeQueue(maxSize, "test")
}

func newDedupeQueue(maxSize int, name string) *dedupeQueue {
	if maxSize <= 0 {
		panic(fmt.Sprintf("maxSize must be positive, got %d", maxSize))
	}

	return &dedupeQueue{
		items:   make([]*queueItem, 0),
		notify:  make(chan struct{}, 10),
		name:    name,
		maxSize: maxSize,
		metrics: metrics.GetDedupeQueueMetrics(),
	}
}

// canDedupe returns true if the event type supports de-duplication.
func canDedupe(ev *event.Event) bool {
	evType := ev.Type()
	return evType == internalevent.SpecUpdate.String() || evType == internalevent.StatusUpdate.String()
}

// dedupeKey returns a composite key of resourceID and event type for
// identifying duplicate events.
func dedupeKey(ev *event.Event) (string, string) {
	return internalevent.ResourceID(ev), ev.Type()
}

func (dq *dedupeQueue) Add(item *event.Event) {
	dq.mu.Lock()
	defer dq.mu.Unlock()

	if item == nil || dq.isShutdown {
		return
	}

	if canDedupe(item) {
		dq.removeDuplicates(item)
	}

	if len(dq.items) >= dq.maxSize {
		dq.pop()
	}

	dq.items = append(dq.items, &queueItem{
		event:      item,
		enqueuedAt: time.Now(),
	})

	if dq.metrics != nil {
		dq.metrics.Adds.WithLabelValues(dq.name).Inc()
		dq.metrics.Depth.WithLabelValues(dq.name).Set(float64(len(dq.items)))
	}

	select {
	case dq.notify <- struct{}{}:
	default:
	}
}

func (dq *dedupeQueue) Get() (*event.Event, bool) {
	dq.mu.Lock()
	if dq.isShutdown && len(dq.items) == 0 {
		dq.mu.Unlock()
		return nil, true
	}

	if len(dq.items) > 0 {
		item := dq.pop()

		if dq.metrics != nil {
			dq.metrics.Depth.WithLabelValues(dq.name).Set(float64(len(dq.items)))
			dq.metrics.Duration.WithLabelValues(dq.name).Observe(time.Since(item.enqueuedAt).Seconds())
		}

		dq.mu.Unlock()
		return item.event, false
	}
	dq.mu.Unlock()

	// Block until an item is available or shutdown
	for range dq.notify {
		dq.mu.Lock()
		if len(dq.items) > 0 {
			item := dq.pop()

			if dq.metrics != nil {
				dq.metrics.Depth.WithLabelValues(dq.name).Set(float64(len(dq.items)))
				dq.metrics.Duration.WithLabelValues(dq.name).Observe(time.Since(item.enqueuedAt).Seconds())
			}

			dq.mu.Unlock()
			return item.event, false
		}

		if dq.isShutdown {
			dq.mu.Unlock()
			return nil, true
		}
		dq.mu.Unlock()
	}

	// notify channel was closed (shutdown)
	return nil, true
}

func (dq *dedupeQueue) Done(_ *event.Event) {
	// No-op: the dedupe queue does not track in-flight items.
}

func (dq *dedupeQueue) Len() int {
	dq.mu.Lock()
	defer dq.mu.Unlock()
	return len(dq.items)
}

func (dq *dedupeQueue) ShutDown() {
	dq.mu.Lock()
	defer dq.mu.Unlock()
	if dq.isShutdown {
		return
	}
	dq.isShutdown = true
	close(dq.notify)
}

// removeDuplicates removes all queued events matching the same resourceID and
// event type as item. Must be called with dq.mu held.
func (dq *dedupeQueue) removeDuplicates(incoming *event.Event) {
	resID, evType := dedupeKey(incoming)
	if resID == "" {
		return
	}

	for i := len(dq.items) - 1; i >= 0; i-- {
		existingID, existingEvType := dedupeKey(dq.items[i].event)
		if existingID == resID && existingEvType == evType {
			dq.items[i] = nil
			dq.items = append(dq.items[:i], dq.items[i+1:]...)
			if dq.metrics != nil {
				dq.metrics.EventsDeduped.WithLabelValues(dq.name).Inc()
			}
		}
	}
}

// pop the first item from the queue.
// Must be called with queue lock held.
func (dq *dedupeQueue) pop() *queueItem {
	if len(dq.items) == 0 {
		return nil
	}
	item := dq.items[0]
	dq.items[0] = nil
	dq.items = dq.items[1:]
	return item
}
