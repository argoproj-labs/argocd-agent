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
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalevent "github.com/argoproj-labs/argocd-agent/internal/event"
	cloudevents "github.com/cloudevents/sdk-go/v2/event"
	"k8s.io/client-go/util/workqueue"
)

// ---------------------------------------------------------------------------
// Old boundedQueue (from pre-dedupe era) re-created here for benchmarking.
// Wraps a rate-limiting workqueue keyed by *event.Event pointers — every Add
// is unique, there is no deduplication. The cap is set high enough that no
// events are dropped, so the consumer must process every single event.
// ---------------------------------------------------------------------------

type oldBoundedQueue struct {
	workqueue.TypedRateLimitingInterface[*cloudevents.Event]
	maxSize int
}

var _ WorkQueue = (*oldBoundedQueue)(nil)

func newOldBoundedQueue(maxSize int, name string) *oldBoundedQueue {
	rateLimiter := workqueue.DefaultTypedControllerRateLimiter[*cloudevents.Event]()
	return &oldBoundedQueue{
		TypedRateLimitingInterface: workqueue.NewTypedRateLimitingQueueWithConfig(rateLimiter,
			workqueue.TypedRateLimitingQueueConfig[*cloudevents.Event]{Name: name}),
		maxSize: maxSize,
	}
}

func (bq *oldBoundedQueue) Add(item *cloudevents.Event) {
	if bq.Len() >= bq.maxSize {
		old, _ := bq.Get()
		bq.Done(old)
	}
	bq.TypedRateLimitingInterface.Add(item)
}

// ---------------------------------------------------------------------------
// Event helpers
// ---------------------------------------------------------------------------

func benchEvent(resourceID, data string) *cloudevents.Event {
	ev := cloudevents.New()
	ev.SetType(internalevent.SpecUpdate.String())
	ev.SetExtension("resourceid", resourceID)
	ev.SetExtension("eventid", fmt.Sprintf("%s_%s", resourceID, data))
	_ = ev.SetData(cloudevents.TextPlain, data)
	return &ev
}

func pregenMultiResourceDedupEvents(n, numResources int) []*cloudevents.Event {
	events := make([]*cloudevents.Event, n)
	for i := range events {
		rid := fmt.Sprintf("app%d_uid%d", i%numResources, i%numResources)
		events[i] = benchEvent(rid, fmt.Sprintf("v%d", i))
	}
	return events
}

// ---------------------------------------------------------------------------
// Simulation harness
//
// Models the real agent/principal pattern:
//
//   Producer goroutine blasts events as fast as possible (inbound gRPC rate).
//   Consumer goroutine calls Get(), sleeps to simulate reconciliation work,
//   then calls Done().
//
// The queue sits between them absorbing the rate mismatch. We measure:
//
//   ns/op          – wall-clock time until every meaningful event is processed
//   events-consumed – how many events the consumer actually had to handle
//   peak-queue-depth – max items waiting in the queue (= memory pressure)
//   queue-mem-KB   – live-heap delta at peak queue load
//   B/op, allocs/op – total allocations (via -benchmem)
// ---------------------------------------------------------------------------

type simResult struct {
	consumed  int64
	peakDepth int64
	heapDelta int64
}

// runSimulation runs a single producer-consumer simulation and returns stats.
func runSimulation(q WorkQueue, events []*cloudevents.Event, processTime time.Duration) simResult {
	// Capture heap baseline (after GC so we see only live objects).
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	var consumed int64
	var peakDepth int64
	var consumerWg sync.WaitGroup

	// Consumer: Get → sleep (fake work) → Done, loop until shutdown.
	consumerWg.Add(1)
	go func() {
		defer consumerWg.Done()
		for {
			ev, shutdown := q.Get()
			if shutdown || ev == nil {
				return
			}
			time.Sleep(processTime)
			q.Done(ev)
			atomic.AddInt64(&consumed, 1)
		}
	}()

	// Producer: blast all events as fast as possible.
	for _, ev := range events {
		q.Add(ev)
		if d := int64(q.Len()); d > peakDepth {
			peakDepth = d
		}
	}

	// Snapshot live heap immediately after the burst (no GC — the consumer
	// has barely started draining so the queue is near its peak).
	var memPeak runtime.MemStats
	runtime.ReadMemStats(&memPeak)
	heapDelta := int64(memPeak.HeapInuse) - int64(memBefore.HeapInuse)
	if heapDelta < 0 {
		heapDelta = 0
	}

	// Wait for consumer to drain the queue.
	for q.Len() > 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(processTime * 2)
	q.ShutDown()
	consumerWg.Wait()

	return simResult{
		consumed:  atomic.LoadInt64(&consumed),
		peakDepth: peakDepth,
		heapDelta: heapDelta,
	}
}

func reportSim(b *testing.B, r simResult) {
	b.ReportMetric(float64(r.consumed), "events-consumed")
	b.ReportMetric(float64(r.peakDepth), "peak-queue-depth")
	b.ReportMetric(float64(r.heapDelta)/1024, "queue-mem-KB")
}

// ---------------------------------------------------------------------------
// Simulation: high inbound rate, dedupable events
//
// 2000 spec-update events spread across numResources apps.
// Consumer takes 1 ms per event (scaled from 100 ms — ratios are identical).
//
// OldBoundedQueue (cap = totalEvents, no drops):
//   Every event stays in the queue → consumer must grind through all 2000 →
//   high wait-memory, long wall-clock time.
//
// NewDedupeQueue:
//   Coalesces to numResources items → consumer processes only the latest
//   version of each resource → low wait-memory, fast completion.
// ---------------------------------------------------------------------------

func BenchmarkSimulation_DedupableEvents(b *testing.B) {
	const (
		totalEvents = 2000
		processTime = 1 * time.Millisecond
	)

	for _, numResources := range []int{10, 100} {
		events := pregenMultiResourceDedupEvents(totalEvents, numResources)

		b.Run(fmt.Sprintf("OldBoundedQueue/%d_resources", numResources), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				q := newOldBoundedQueue(totalEvents, "bench")
				r := runSimulation(q, events, processTime)
				reportSim(b, r)
			}
		})

		b.Run(fmt.Sprintf("NewDedupeQueue/%d_resources", numResources), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				q := NewDedupeQueue("bench")
				r := runSimulation(q, events, processTime)
				reportSim(b, r)
			}
		})
	}
}
