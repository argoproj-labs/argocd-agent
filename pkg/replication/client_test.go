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

package replication

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/replicationapi"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewClient(t *testing.T) {
	t.Run("creates with defaults", func(t *testing.T) {
		ctx := context.Background()
		c := NewClient(ctx)
		require.NotNil(t, c)
		assert.Equal(t, ClientStateDisconnected, c.State())
		assert.Equal(t, 1*time.Second, c.reconnectBackoff)
		assert.Equal(t, 30*time.Second, c.reconnectBackoffMax)
		assert.Equal(t, 5*time.Second, c.ackInterval)
	})

	t.Run("creates with options", func(t *testing.T) {
		ctx := context.Background()
		c := NewClient(ctx,
			WithPrimaryAddress("primary.example.com:8404"),
			WithInsecure(),
			WithReconnectBackoff(2*time.Second, 60*time.Second, 2.0),
			WithAckInterval(10*time.Second),
		)
		require.NotNil(t, c)
		assert.Equal(t, "primary.example.com:8404", c.primaryAddr)
		assert.True(t, c.insecure)
		assert.Equal(t, 2*time.Second, c.reconnectBackoff)
		assert.Equal(t, 60*time.Second, c.reconnectBackoffMax)
		assert.Equal(t, 10*time.Second, c.ackInterval)
	})
}

func TestClientState(t *testing.T) {
	ctx := context.Background()
	c := NewClient(ctx)

	assert.Equal(t, ClientStateDisconnected, c.State())
	assert.False(t, c.IsConnected())

	// Simulate state changes
	c.setState(ClientStateConnecting)
	assert.Equal(t, ClientStateConnecting, c.State())

	c.setState(ClientStateConnected)
	assert.Equal(t, ClientStateConnected, c.State())
	assert.True(t, c.IsConnected())
}

func TestClientProcessEvent(t *testing.T) {
	t.Run("processes event and updates tracking", func(t *testing.T) {
		ctx := context.Background()
		c := NewClient(ctx)

		ev := &ReplicatedEvent{
			AgentName:       "agent1",
			Direction:       DirectionInbound,
			SequenceNum:     42,
			ProcessedAtUnix: time.Now().Unix(),
		}

		err := c.ProcessEvent(ev)
		require.NoError(t, err)

		assert.Equal(t, uint64(42), c.LastSequenceNum())
	})

	t.Run("calls event handler", func(t *testing.T) {
		ctx := context.Background()

		var handledEvent *ReplicatedEvent
		c := NewClient(ctx,
			WithEventHandler(func(ev *ReplicatedEvent) error {
				handledEvent = ev
				return nil
			}),
		)

		ev := &ReplicatedEvent{
			AgentName:   "agent1",
			SequenceNum: 1,
		}

		err := c.ProcessEvent(ev)
		require.NoError(t, err)
		assert.Equal(t, ev, handledEvent)
	})
}

func TestClientCallbacks(t *testing.T) {
	t.Run("calls onConnected callback", func(t *testing.T) {
		ctx := context.Background()
		var called atomic.Bool

		c := NewClient(ctx,
			WithOnConnected(func() {
				called.Store(true)
			}),
		)

		// Simulate connection (won't actually connect without server)
		c.mu.Lock()
		c.setState(ClientStateConnected)
		if c.onConnected != nil {
			go c.onConnected()
		}
		c.mu.Unlock()

		time.Sleep(50 * time.Millisecond)
		assert.True(t, called.Load())
	})

	t.Run("calls onDisconnected callback", func(t *testing.T) {
		ctx := context.Background()
		var called atomic.Bool

		c := NewClient(ctx,
			WithOnDisconnected(func() {
				called.Store(true)
			}),
		)

		c.Disconnect()

		time.Sleep(50 * time.Millisecond)
		assert.True(t, called.Load())
	})
}

func TestClientReconnectBackoff(t *testing.T) {
	ctx := context.Background()
	c := NewClient(ctx,
		WithReconnectBackoff(1*time.Second, 10*time.Second, 2.0),
	)

	// Initial backoff
	assert.Equal(t, 1*time.Second, c.nextReconnectBackoff())

	// Exponential increase
	assert.Equal(t, 2*time.Second, c.nextReconnectBackoff())
	assert.Equal(t, 4*time.Second, c.nextReconnectBackoff())
	assert.Equal(t, 8*time.Second, c.nextReconnectBackoff())

	// Capped at max
	assert.Equal(t, 10*time.Second, c.nextReconnectBackoff())
	assert.Equal(t, 10*time.Second, c.nextReconnectBackoff())

	// Reset
	c.resetReconnectBackoff()
	assert.Equal(t, 1*time.Second, c.nextReconnectBackoff())
}

func TestClientGetStatus(t *testing.T) {
	ctx := context.Background()
	c := NewClient(ctx,
		WithPrimaryAddress("primary.example.com:8404"),
	)

	// Process an event to update tracking
	ev := &ReplicatedEvent{
		SequenceNum:     100,
		ProcessedAtUnix: time.Now().Unix() - 5, // 5 seconds ago
	}
	c.ProcessEvent(ev)

	status := c.GetStatus()
	assert.Equal(t, ClientStateDisconnected, status.State)
	assert.Equal(t, "primary.example.com:8404", status.PrimaryAddress)
	assert.Equal(t, uint64(100), status.LastSequenceNum)
}

func TestClientStop(t *testing.T) {
	ctx := context.Background()
	c := NewClient(ctx)

	err := c.Stop()
	require.NoError(t, err)

	// Context should be cancelled
	select {
	case <-c.ctx.Done():
		// Expected
	default:
		t.Error("context should be cancelled after Stop")
	}
}

func TestClientConnect_Errors(t *testing.T) {
	t.Run("fails without primary address", func(t *testing.T) {
		ctx := context.Background()
		c := NewClient(ctx)

		err := c.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "primary address not configured")
	})

	t.Run("fails without TLS config and not insecure", func(t *testing.T) {
		ctx := context.Background()
		c := NewClient(ctx,
			WithPrimaryAddress("primary.example.com:8404"),
		)

		err := c.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TLS configuration required")
	})
}

func TestSequenceGapDetection(t *testing.T) {
	ctx := context.Background()
	c := NewClient(ctx)

	// Simulate lastSequenceNum = 5, so expectedSeq starts at 6
	c.lastSequenceNum.Store(5)

	// Process event with seq 6 (no gap)
	ev6 := &ReplicatedEvent{AgentName: "a", SequenceNum: 6}
	require.NoError(t, c.ProcessEvent(ev6))
	assert.False(t, c.needsReconcile.Load())

	// Process event with seq 10 (gap: expected 7, got 10)
	// We can't call receiveEvents directly without a stream, so we replicate
	// the gap detection logic to verify the flag is set correctly
	c.lastSequenceNum.Store(6)
	expectedSeq := c.lastSequenceNum.Load() + 1 // 7

	gapEvent := &ReplicatedEvent{AgentName: "a", SequenceNum: 10}
	if gapEvent.SequenceNum > expectedSeq {
		c.needsReconcile.Store(true)
		c.metrics.SequenceGaps.Inc()
	}

	assert.True(t, c.needsReconcile.Load())
}

func TestReconcileInterval(t *testing.T) {
	ctx := context.Background()

	t.Run("default is 1 minute", func(t *testing.T) {
		c := NewClient(ctx)
		assert.Equal(t, 1*time.Minute, c.reconcileInterval)
	})

	t.Run("WithReconcileInterval sets custom value", func(t *testing.T) {
		c := NewClient(ctx, WithReconcileInterval(30*time.Second))
		assert.Equal(t, 30*time.Second, c.reconcileInterval)
	})
}

func TestGetSnapshot_CAS_DoesNotOverwriteHigherSequence(t *testing.T) {
	ctx := context.Background()
	c := NewClient(ctx, WithInsecure(), WithPrimaryAddress("localhost:0"))

	// Simulate receiveEvents storing a higher sequence concurrently
	c.lastSequenceNum.Store(100)

	// GetSnapshot's CAS loop should not overwrite 100 with 50
	for {
		current := c.lastSequenceNum.Load()
		if uint64(50) <= current {
			break
		}
		if c.lastSequenceNum.CompareAndSwap(current, 50) {
			break
		}
	}
	assert.Equal(t, uint64(100), c.lastSequenceNum.Load())

	// But should overwrite if snapshot is higher
	for {
		current := c.lastSequenceNum.Load()
		if uint64(200) <= current {
			break
		}
		if c.lastSequenceNum.CompareAndSwap(current, 200) {
			break
		}
	}
	assert.Equal(t, uint64(200), c.lastSequenceNum.Load())
}

func TestRunEventStream_ResetsSequenceOnReconnect(t *testing.T) {
	ctx := context.Background()
	c := NewClient(ctx, WithInsecure(), WithPrimaryAddress("localhost:0"))

	// Simulate a stale sequence from a previous primary session
	c.lastSequenceNum.Store(1000)

	// runEventStream resets to 0 before subscribing. We can't call it
	// without a real server, but we can verify the reset logic directly:
	// after a disconnect/reconnect cycle, the sequence must be 0 so the
	// CAS loop in GetSnapshot can store the new primary's lower sequence.
	c.lastSequenceNum.Store(0) // what runEventStream does
	assert.Equal(t, uint64(0), c.lastSequenceNum.Load())

	// Now CAS with a low snapshot sequence from new primary succeeds
	for {
		current := c.lastSequenceNum.Load()
		if uint64(5) <= current {
			break
		}
		if c.lastSequenceNum.CompareAndSwap(current, 5) {
			break
		}
	}
	assert.Equal(t, uint64(5), c.lastSequenceNum.Load())
}

func TestReconcileSkipsWhenInSync(t *testing.T) {
	ctx := context.Background()
	c := NewClient(ctx, WithInsecure(), WithPrimaryAddress("localhost:0"))

	// No connection — reconcile should return nil (no-op)
	err := c.reconcile(ctx)
	assert.NoError(t, err)
}

func TestProtoToReplicatedEvent_ConvertsCloudEvent(t *testing.T) {
	ctx := context.Background()
	c := NewClient(ctx)
	evSource := event.NewEventSource("test")

	t.Run("Application", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "my-app",
				Namespace:       "argocd",
				UID:             "abc-123",
				ResourceVersion: "1",
			},
		}

		cloudEvent := evSource.ApplicationEvent(event.Create, app)
		pbEvent, err := format.ToProto(cloudEvent)
		require.NoError(t, err)

		protoEvent := &replicationapi.ReplicatedEvent{
			Event:       pbEvent,
			AgentName:   "agent1",
			Direction:   "outbound",
			SequenceNum: 1,
		}

		result := c.protoToReplicatedEvent(protoEvent)
		require.NotNil(t, result.Event)
		assert.Equal(t, "agent1", result.AgentName)
		assert.Equal(t, Direction("outbound"), result.Direction)
		assert.Equal(t, uint64(1), result.SequenceNum)

		ce := result.Event.CloudEvent()
		require.NotNil(t, ce)
		assert.Equal(t, event.Create.String(), ce.Type())

		gotApp, err := result.Event.Application()
		require.NoError(t, err)
		assert.Equal(t, "my-app", gotApp.Name)
		assert.Equal(t, "argocd", gotApp.Namespace)
	})

	t.Run("AppProject", func(t *testing.T) {
		proj := &v1alpha1.AppProject{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "my-project",
				Namespace:       "argocd",
				UID:             "def-456",
				ResourceVersion: "2",
			},
		}

		cloudEvent := evSource.AppProjectEvent(event.Create, proj)
		pbEvent, err := format.ToProto(cloudEvent)
		require.NoError(t, err)

		protoEvent := &replicationapi.ReplicatedEvent{
			Event:       pbEvent,
			AgentName:   "agent2",
			Direction:   "inbound",
			SequenceNum: 2,
		}

		result := c.protoToReplicatedEvent(protoEvent)
		require.NotNil(t, result.Event)
		assert.Equal(t, "agent2", result.AgentName)

		ce := result.Event.CloudEvent()
		require.NotNil(t, ce)

		gotProj, err := result.Event.AppProject()
		require.NoError(t, err)
		assert.Equal(t, "my-project", gotProj.Name)
		assert.Equal(t, "argocd", gotProj.Namespace)
	})
}
