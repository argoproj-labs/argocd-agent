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

package replication

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/event/targets"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/cloudevents/sdk-go/binding/format/protobuf/v2/pb"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeReplicaStream is a ReplicaStream that records what is sent to it.
type fakeReplicaStream struct {
	ctx     context.Context
	sendErr error
	sent    chan *ReplicatedEvent
}

func newFakeReplicaStream(ctx context.Context) *fakeReplicaStream {
	return &fakeReplicaStream{ctx: ctx, sent: make(chan *ReplicatedEvent, 16)}
}

func (s *fakeReplicaStream) Send(ev *ReplicatedEvent) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent <- ev
	return nil
}

func (s *fakeReplicaStream) Context() context.Context { return s.ctx }

// reregisteringStream runs onContext the first time its context is asked for.
type reregisteringStream struct {
	*fakeReplicaStream
	once      sync.Once
	onContext func()
}

func (s *reregisteringStream) Context() context.Context {
	s.once.Do(s.onContext)
	return s.ctx
}

func newTestEvent(t *testing.T) *event.Event {
	t.Helper()
	app := &v1alpha1.Application{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "argocd", UID: "uid-1", ResourceVersion: "1"}}
	return event.New(event.NewEventSource("test").ApplicationEvent(event.SpecUpdate, app), targets.Application)
}

// deltaOf returns a function reporting how much a metric changed since deltaOf
// was called. The forwarder metrics are process-wide, so tests compare deltas,
// which means these tests must not run in parallel.
func deltaOf(c prometheus.Collector) func() float64 {
	before := testutil.ToFloat64(c)
	return func() float64 { return testutil.ToFloat64(c) - before }
}

func recvReplicated(t *testing.T, ch <-chan *ReplicatedEvent) *ReplicatedEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a replicated event")
		return nil
	}
}

// takePending takes the next event off the forwarder's queue without blocking.
func takePending(t *testing.T, f *Forwarder) *ReplicatedEvent {
	t.Helper()
	select {
	case ev := <-f.pendingEvents:
		return ev
	default:
		t.Fatal("expected an event in the queue")
		return nil
	}
}

func requireDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the forwarder to stop")
	}
}

// newForwarderAt returns a forwarder whose sequence number is n, with n events
// waiting in its queue.
func newForwarderAt(t *testing.T, n int) *Forwarder {
	t.Helper()
	f := NewForwarder(WithBufferSize(n + 1))
	ev := newTestEvent(t)
	for range n {
		f.Forward(ev, "agent-a", DirectionInbound)
	}
	return f
}

func TestNewForwarder(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		f := NewForwarder()

		require.Equal(t, 1000, cap(f.pendingEvents))
		require.NotNil(t, f.metrics)
		require.Zero(t, f.CurrentSequenceNum())
		require.Zero(t, f.PendingEventCount())
		require.Zero(t, f.ReplicaCount())
	})

	t.Run("buffer size option", func(t *testing.T) {
		f := NewForwarder(WithBufferSize(5))

		require.Equal(t, 5, cap(f.pendingEvents))
	})

	t.Run("metrics are shared by every forwarder", func(t *testing.T) {
		require.Same(t, NewForwarderMetrics(), NewForwarder().metrics)
	})
}

func TestForwarder_Forward(t *testing.T) {
	t.Run("queues the event with its replication metadata", func(t *testing.T) {
		f := NewForwarder(WithBufferSize(4))
		queued := deltaOf(f.metrics.EventsQueued)
		queueSize := deltaOf(f.metrics.QueueSize)
		ev := newTestEvent(t)

		before := time.Now().Unix()
		f.Forward(ev, "agent-a", DirectionInbound)
		after := time.Now().Unix()

		require.Equal(t, 1, f.PendingEventCount())
		require.Equal(t, uint64(1), f.CurrentSequenceNum())
		got := takePending(t, f)
		require.Same(t, ev, got.Event)
		require.Equal(t, "agent-a", got.AgentName)
		require.Equal(t, DirectionInbound, got.Direction)
		require.Equal(t, uint64(1), got.SequenceNum)
		require.GreaterOrEqual(t, got.ProcessedAtUnix, before)
		require.LessOrEqual(t, got.ProcessedAtUnix, after)
		require.Equal(t, 1.0, queued())
		require.Equal(t, 1.0, queueSize())
		require.Equal(t, 1.0, testutil.ToFloat64(f.metrics.LastSequenceNumber))
	})

	t.Run("sequence numbers increase monotonically", func(t *testing.T) {
		f := NewForwarder(WithBufferSize(4))
		ev := newTestEvent(t)

		f.Forward(ev, "agent-a", DirectionInbound)
		f.Forward(ev, "agent-b", DirectionOutbound)
		f.Forward(ev, "agent-c", DirectionInbound)

		require.Equal(t, 3.0, testutil.ToFloat64(f.metrics.LastSequenceNumber))
		for want := uint64(1); want <= 3; want++ {
			require.Equal(t, want, takePending(t, f).SequenceNum)
		}
		require.Equal(t, uint64(3), f.CurrentSequenceNum())
	})

	t.Run("drops the event when the queue is full", func(t *testing.T) {
		f := NewForwarder(WithBufferSize(1))
		dropped := deltaOf(f.metrics.EventsDropped)
		queued := deltaOf(f.metrics.EventsQueued)
		ev := newTestEvent(t)

		f.Forward(ev, "agent-a", DirectionInbound)
		f.Forward(ev, "agent-a", DirectionInbound)

		require.Equal(t, 1, f.PendingEventCount())
		require.Equal(t, uint64(2), f.CurrentSequenceNum(), "a dropped event still consumes a sequence number")
		require.Equal(t, 1.0, dropped())
		require.Equal(t, 1.0, queued())
		require.Equal(t, uint64(1), takePending(t, f).SequenceNum, "the event that was already queued is kept")
	})
}

func TestForwarder_replicaRegistration(t *testing.T) {
	ev := &ReplicatedEvent{SequenceNum: 1}

	t.Run("registering adds a replica", func(t *testing.T) {
		f := NewForwarder()
		connected := deltaOf(f.metrics.ReplicasConnected)

		f.RegisterReplica("r1", newFakeReplicaStream(t.Context()))

		require.Equal(t, 1, f.ReplicaCount())
		require.Equal(t, 1.0, connected())
	})

	t.Run("registering the same id again replaces the stream without counting twice", func(t *testing.T) {
		f := NewForwarder()
		connected := deltaOf(f.metrics.ReplicasConnected)
		first := newFakeReplicaStream(t.Context())
		second := newFakeReplicaStream(t.Context())

		f.RegisterReplica("r1", first)
		f.RegisterReplica("r1", second)

		require.Equal(t, 1, f.ReplicaCount())
		require.Equal(t, 1.0, connected())

		f.broadcastToReplicas(ev)
		require.Len(t, second.sent, 1)
		require.Empty(t, first.sent)
	})

	t.Run("unregistering removes the replica", func(t *testing.T) {
		f := NewForwarder()
		f.RegisterReplica("r1", newFakeReplicaStream(t.Context()))
		connected := deltaOf(f.metrics.ReplicasConnected)

		f.UnregisterReplica("r1")

		require.Zero(t, f.ReplicaCount())
		require.Equal(t, -1.0, connected())
	})

	t.Run("unregistering an unknown replica changes nothing", func(t *testing.T) {
		f := NewForwarder()
		connected := deltaOf(f.metrics.ReplicasConnected)

		f.UnregisterReplica("unknown")

		require.Zero(t, f.ReplicaCount())
		require.Zero(t, connected())
	})
}

func TestForwarder_broadcastToReplicas(t *testing.T) {
	ev := &ReplicatedEvent{SequenceNum: 7, AgentName: "agent-a", Direction: DirectionOutbound}

	t.Run("delivers the event to every replica", func(t *testing.T) {
		f := NewForwarder()
		r1 := newFakeReplicaStream(t.Context())
		r2 := newFakeReplicaStream(t.Context())
		f.RegisterReplica("r1", r1)
		f.RegisterReplica("r2", r2)
		forwarded := deltaOf(f.metrics.EventsForwarded)

		f.broadcastToReplicas(ev)

		require.Same(t, ev, recvReplicated(t, r1.sent))
		require.Same(t, ev, recvReplicated(t, r2.sent))
		require.Equal(t, 2.0, forwarded())
	})

	t.Run("a replica that fails to receive does not stop the others and stays registered", func(t *testing.T) {
		f := NewForwarder()
		failing := newFakeReplicaStream(t.Context())
		failing.sendErr = errors.New("stream broken")
		healthy := newFakeReplicaStream(t.Context())
		f.RegisterReplica("failing", failing)
		f.RegisterReplica("healthy", healthy)
		forwarded := deltaOf(f.metrics.EventsForwarded)
		forwardErrors := deltaOf(f.metrics.ForwardingErrors)

		f.broadcastToReplicas(ev)

		require.Same(t, ev, recvReplicated(t, healthy.sent))
		require.Equal(t, 1.0, forwarded())
		require.Equal(t, 1.0, forwardErrors())
		require.Equal(t, 2, f.ReplicaCount())
	})

	t.Run("a replica whose stream is done is removed instead of receiving the event", func(t *testing.T) {
		f := NewForwarder()
		deadCtx, cancel := context.WithCancel(t.Context())
		cancel()
		dead := newFakeReplicaStream(deadCtx)
		alive := newFakeReplicaStream(t.Context())
		f.RegisterReplica("dead", dead)
		f.RegisterReplica("alive", alive)
		connected := deltaOf(f.metrics.ReplicasConnected)
		forwarded := deltaOf(f.metrics.EventsForwarded)

		f.broadcastToReplicas(ev)

		require.Same(t, ev, recvReplicated(t, alive.sent))
		require.Empty(t, dead.sent)
		require.Equal(t, 1, f.ReplicaCount())
		require.Equal(t, -1.0, connected())
		require.Equal(t, 1.0, forwarded())
	})

	t.Run("a replica that re-registers while it is being removed is kept", func(t *testing.T) {
		f := NewForwarder()
		deadCtx, cancel := context.WithCancel(t.Context())
		cancel()
		alive := newFakeReplicaStream(t.Context())
		reregistered := make(chan struct{})
		// While the broadcast holds the read lock and inspects the dead stream,
		// the same replica id reconnects. The registration blocks on the write
		// lock until the broadcast lets go of the read lock, which is before the
		// broadcast takes the write lock to remove the dead replicas.
		dead := &reregisteringStream{
			fakeReplicaStream: newFakeReplicaStream(deadCtx),
			onContext: func() {
				go func() {
					defer close(reregistered)
					f.RegisterReplica("r1", alive)
				}()
				// A pending writer makes TryRLock fail, so once it does the
				// registration is queued behind the broadcast's read lock.
				for f.mu.TryRLock() {
					f.mu.RUnlock()
					runtime.Gosched()
				}
			},
		}
		f.RegisterReplica("r1", dead)
		connected := deltaOf(f.metrics.ReplicasConnected)

		f.broadcastToReplicas(ev)
		requireDone(t, reregistered)

		require.Equal(t, 1, f.ReplicaCount())
		require.Zero(t, connected())
		f.broadcastToReplicas(ev)
		require.Same(t, ev, recvReplicated(t, alive.sent))
	})

	t.Run("an event with no replicas is not counted as forwarded or failed", func(t *testing.T) {
		f := NewForwarder()
		forwarded := deltaOf(f.metrics.EventsForwarded)
		forwardErrors := deltaOf(f.metrics.ForwardingErrors)

		f.broadcastToReplicas(ev)

		require.Zero(t, forwarded())
		require.Zero(t, forwardErrors())
	})
}

func TestForwarder_Run(t *testing.T) {
	t.Run("forwards the queued events to the replicas in order", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		f := NewForwarder()
		replica := newFakeReplicaStream(ctx)
		f.RegisterReplica("r1", replica)
		queueSize := deltaOf(f.metrics.QueueSize)
		ev := newTestEvent(t)

		done := make(chan struct{})
		go func() {
			defer close(done)
			f.Run(ctx)
		}()

		f.Forward(ev, "agent-a", DirectionInbound)
		f.Forward(ev, "agent-b", DirectionOutbound)

		first := recvReplicated(t, replica.sent)
		second := recvReplicated(t, replica.sent)
		require.Equal(t, uint64(1), first.SequenceNum)
		require.Equal(t, "agent-a", first.AgentName)
		require.Equal(t, uint64(2), second.SequenceNum)
		require.Equal(t, "agent-b", second.AgentName)
		require.Equal(t, DirectionOutbound, second.Direction)

		cancel()
		requireDone(t, done)
		require.Zero(t, f.PendingEventCount())
		require.Zero(t, queueSize(), "every queued event was taken off the queue")
	})

	t.Run("takes events off the queue even when no replica is there to receive them", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		f := NewForwarder()
		queueSize := deltaOf(f.metrics.QueueSize)
		ev := newTestEvent(t)
		f.Forward(ev, "agent-a", DirectionInbound)
		f.Forward(ev, "agent-a", DirectionInbound)

		done := make(chan struct{})
		go func() {
			defer close(done)
			f.Run(ctx)
		}()

		require.Eventually(t, func() bool { return queueSize() == 0 }, 5*time.Second, 5*time.Millisecond)
		cancel()
		requireDone(t, done)
		require.Zero(t, f.PendingEventCount())
	})

	t.Run("stops when the context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		f := NewForwarder()

		done := make(chan struct{})
		go func() {
			defer close(done)
			f.Run(ctx)
		}()

		cancel()
		requireDone(t, done)
	})
}

func TestForwarder_replicaLag(t *testing.T) {
	t.Run("lag is the distance between the current and the acknowledged sequence", func(t *testing.T) {
		f := newForwarderAt(t, 5)
		f.RegisterReplica("r1", newFakeReplicaStream(t.Context()))
		f.RegisterReplica("r2", newFakeReplicaStream(t.Context()))

		f.UpdateReplicaAck("r1", 2)

		require.Equal(t, uint64(3), f.GetReplicaLag("r1"))
		require.Equal(t, uint64(5), f.GetReplicaLag("r2"), "a replica that acknowledged nothing lags by the whole sequence")
	})

	t.Run("an acknowledgement ahead of the sequence is no lag rather than an underflow", func(t *testing.T) {
		f := newForwarderAt(t, 5)
		f.RegisterReplica("r1", newFakeReplicaStream(t.Context()))

		f.UpdateReplicaAck("r1", 9)

		require.Zero(t, f.GetReplicaLag("r1"))
	})

	t.Run("an unknown replica has no lag and acknowledging it changes nothing", func(t *testing.T) {
		f := newForwarderAt(t, 5)

		f.UpdateReplicaAck("unknown", 3)

		require.Zero(t, f.GetReplicaLag("unknown"))
		require.Zero(t, f.ReplicaCount())
	})
}

func TestForwarder_GetStatus(t *testing.T) {
	t.Run("aggregates the sequence, the queue and the lag of every replica", func(t *testing.T) {
		f := newForwarderAt(t, 3)
		f.RegisterReplica("r1", newFakeReplicaStream(t.Context()))
		f.RegisterReplica("r2", newFakeReplicaStream(t.Context()))
		f.UpdateReplicaAck("r1", 1)
		f.UpdateReplicaAck("r2", 5)

		status := f.GetStatus()

		require.Equal(t, uint64(3), status.CurrentSequenceNum)
		require.Equal(t, 3, status.PendingEvents)
		require.Equal(t, 2, status.ConnectedReplicas)
		require.Equal(t, map[string]uint64{"r1": 2, "r2": 0}, status.ReplicaLags)
	})

	t.Run("an idle forwarder reports an empty status", func(t *testing.T) {
		status := NewForwarder().GetStatus()

		require.Zero(t, status.CurrentSequenceNum)
		require.Zero(t, status.PendingEvents)
		require.Zero(t, status.ConnectedReplicas)
		require.NotNil(t, status.ReplicaLags)
		require.Empty(t, status.ReplicaLags)
	})
}

func TestReplicatedEvent_ToProto(t *testing.T) {
	t.Run("a replicated event without an event converts to nothing", func(t *testing.T) {
		got, err := (&ReplicatedEvent{}).ToProto()

		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("an event without a cloud event converts to nothing", func(t *testing.T) {
		got, err := (&ReplicatedEvent{Event: event.New(nil, targets.Application)}).ToProto()

		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("maps the replication metadata and carries the event", func(t *testing.T) {
		ev := newTestEvent(t)
		replicated := &ReplicatedEvent{
			Event:           ev,
			AgentName:       "agent-a",
			Direction:       DirectionOutbound,
			SequenceNum:     42,
			ProcessedAtUnix: 1700000000,
		}

		got, err := replicated.ToProto()

		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "agent-a", got.AgentName)
		require.Equal(t, "outbound", got.Direction)
		require.Equal(t, uint64(42), got.SequenceNum)
		require.Equal(t, int64(1700000000), got.ProcessedAtUnix)

		pev, ok := got.Event.(*pb.CloudEvent)
		require.True(t, ok, "the event is carried as a protobuf CloudEvent")
		back, err := format.FromProto(pev)
		require.NoError(t, err)
		require.Equal(t, event.EventID(ev.CloudEvent()), event.EventID(back))
		require.Equal(t, ev.CloudEvent().Type(), back.Type())
	})
}
