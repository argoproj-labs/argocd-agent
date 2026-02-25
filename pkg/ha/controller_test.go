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

package ha

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewController(t *testing.T) {
	t.Run("creates controller with defaults", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx)
		require.NoError(t, err)
		require.NotNil(t, c)
		assert.Equal(t, StateRecovering, c.State())
		assert.False(t, c.options.Enabled)
	})

	t.Run("creates controller with HA enabled", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
			WithPreferredRole("primary"),
		)
		require.NoError(t, err)
		require.NotNil(t, c)
		assert.True(t, c.options.Enabled)
		assert.Equal(t, RolePrimary, c.options.PreferredRole)
		assert.Equal(t, "peer.example.com:8404", c.options.PeerAddress)
	})

	t.Run("fails with invalid options - replica without peer", func(t *testing.T) {
		ctx := context.Background()
		_, err := NewController(ctx,
			WithEnabled(true),
			WithPreferredRole("replica"),
			// Missing peer address
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "peer address is required for replica")
	})

	t.Run("primary without peer address succeeds", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPreferredRole("primary"),
		)
		require.NoError(t, err)
		require.NotNil(t, c)
		assert.True(t, c.options.Enabled)
		assert.Equal(t, RolePrimary, c.options.PreferredRole)
		assert.Empty(t, c.options.PeerAddress)
	})

	t.Run("fails with invalid role", func(t *testing.T) {
		ctx := context.Background()
		_, err := NewController(ctx,
			WithPreferredRole("invalid"),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown role")
	})
}

func TestControllerStart_Disabled(t *testing.T) {
	ctx := context.Background()
	c, err := NewController(ctx)
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)

	assert.Equal(t, StateActive, c.State())
}

func TestControllerStart_PrimaryNoPeerAddress(t *testing.T) {
	ctx := context.Background()
	c, err := NewController(ctx,
		WithEnabled(true),
		WithPreferredRole("primary"),
	)
	require.NoError(t, err)

	var activeCalled atomic.Bool
	c.SetOnBecomeActive(func(ctx context.Context) {
		activeCalled.Store(true)
	})

	err = c.Start()
	require.NoError(t, err)
	assert.Equal(t, StateActive, c.State())
	assert.True(t, activeCalled.Load())
}

func TestControllerStart_PrimaryWithPeer(t *testing.T) {
	ctx := context.Background()
	c, err := NewController(ctx,
		WithEnabled(true),
		WithPeerAddress("peer.example.com:8404"),
		WithPreferredRole("primary"),
	)
	require.NoError(t, err)

	var activeCalled atomic.Bool
	c.SetOnBecomeActive(func(ctx context.Context) {
		activeCalled.Store(true)
	})

	err = c.Start()
	require.NoError(t, err)
	assert.Equal(t, StateActive, c.State())
	assert.True(t, activeCalled.Load())
}

func TestControllerStart_Replica(t *testing.T) {
	ctx := context.Background()
	c, err := NewController(ctx,
		WithEnabled(true),
		WithPeerAddress("peer.example.com:8404"),
		WithPreferredRole("replica"),
	)
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)

	// Replica starts in syncing — replication client handles connectivity
	assert.Equal(t, StateSyncing, c.State())
}

func TestControllerOnAgentConnect(t *testing.T) {
	t.Run("accepts connection in active state", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx)
		require.NoError(t, err)

		c.Start()
		assert.Equal(t, StateActive, c.State())

		err = c.OnAgentConnect("test-agent")
		require.NoError(t, err)
	})

	t.Run("rejects connection in replicating state", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateReplicating
		c.mu.Unlock()

		err = c.OnAgentConnect("test-agent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not accepting connections")
	})

	t.Run("rejects connection in disconnected state", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateDisconnected
		c.mu.Unlock()

		err = c.OnAgentConnect("test-agent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not accepting connections")
	})
}

func TestControllerReplicationCallbacks(t *testing.T) {
	t.Run("OnReplicationConnected transitions from disconnected to replicating", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateDisconnected
		c.mu.Unlock()

		c.OnReplicationConnected()
		assert.Equal(t, StateReplicating, c.State())
	})

	t.Run("OnReplicationConnected transitions from syncing to replicating", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateSyncing
		c.mu.Unlock()

		c.OnReplicationConnected()
		assert.Equal(t, StateReplicating, c.State())
	})

	t.Run("OnReplicationDisconnected transitions to disconnected", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateReplicating
		c.mu.Unlock()

		c.OnReplicationDisconnected()
		assert.Equal(t, StateDisconnected, c.State())
	})

	t.Run("OnSyncComplete transitions from syncing to replicating", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateSyncing
		c.mu.Unlock()

		c.OnSyncComplete()
		assert.Equal(t, StateReplicating, c.State())
	})
}

func TestControllerPromote(t *testing.T) {
	t.Run("promotes from disconnected without force", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateDisconnected
		c.mu.Unlock()

		var activeCalled atomic.Bool
		c.SetOnBecomeActive(func(ctx context.Context) {
			activeCalled.Store(true)
		})

		err = c.Promote(ctx, false)
		require.NoError(t, err)
		assert.Equal(t, StateActive, c.State())

		time.Sleep(50 * time.Millisecond)
		assert.True(t, activeCalled.Load())
	})

	t.Run("promotes from replicating with force", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateReplicating
		c.mu.Unlock()

		err = c.Promote(ctx, true)
		require.NoError(t, err)
		assert.Equal(t, StateActive, c.State())
	})

	t.Run("refuses promote from replicating without force", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateReplicating
		c.mu.Unlock()

		err = c.Promote(ctx, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "still replicating from peer")
	})

	t.Run("refuses if already active", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateActive
		c.mu.Unlock()

		err = c.Promote(ctx, true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already active")
	})

	t.Run("refuses from recovering state", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		err = c.Promote(ctx, true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can only promote from replicating or disconnected")
	})
}

func TestControllerDemote(t *testing.T) {
	t.Run("demotes from active to replicating", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateActive
		c.mu.Unlock()

		var replicaCalled atomic.Bool
		c.SetOnBecomeReplica(func(ctx context.Context) {
			replicaCalled.Store(true)
		})

		err = c.Demote(ctx)
		require.NoError(t, err)
		assert.Equal(t, StateReplicating, c.State())

		time.Sleep(50 * time.Millisecond)
		assert.True(t, replicaCalled.Load())
	})

	t.Run("fails to demote from non-active state", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateReplicating
		c.mu.Unlock()

		err = c.Demote(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can only demote from active")
	})
}

func TestControllerHealthStatus(t *testing.T) {
	t.Run("healthy in active state", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx)
		require.NoError(t, err)
		c.Start()

		assert.True(t, c.IsHealthy())
		assert.True(t, c.ShouldAcceptAgents())
	})

	t.Run("unhealthy in replicating state", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateReplicating
		c.mu.Unlock()

		assert.False(t, c.IsHealthy())
		assert.False(t, c.ShouldAcceptAgents())
	})

	t.Run("unhealthy in disconnected state", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateDisconnected
		c.mu.Unlock()

		assert.False(t, c.IsHealthy())
		assert.False(t, c.ShouldAcceptAgents())
	})
}

func TestControllerGetStatus(t *testing.T) {
	t.Run("active state shows peer reachable false", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
			WithPreferredRole("primary"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateActive
		c.mu.Unlock()

		status := c.GetStatus()
		assert.Equal(t, StateActive, status.State)
		assert.Equal(t, RolePrimary, status.PreferredRole)
		assert.Equal(t, "peer.example.com:8404", status.PeerAddress)
		assert.False(t, status.PeerReachable)
	})

	t.Run("replicating state shows peer reachable true", func(t *testing.T) {
		ctx := context.Background()
		c, err := NewController(ctx,
			WithEnabled(true),
			WithPeerAddress("peer.example.com:8404"),
		)
		require.NoError(t, err)

		c.mu.Lock()
		c.state = StateReplicating
		c.mu.Unlock()

		status := c.GetStatus()
		assert.Equal(t, StateReplicating, status.State)
		assert.True(t, status.PeerReachable)
	})
}

func TestControllerShutdown(t *testing.T) {
	ctx := context.Background()
	c, err := NewController(ctx)
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)

	err = c.Shutdown()
	require.NoError(t, err)

	select {
	case <-c.ctx.Done():
		// Expected
	default:
		t.Error("context should be cancelled after shutdown")
	}
}

func TestStateCallbacks(t *testing.T) {
	ctx := context.Background()
	c, err := NewController(ctx,
		WithEnabled(true),
		WithPeerAddress("peer.example.com:8404"),
	)
	require.NoError(t, err)

	done := make(chan string, 1)
	c.SetOnStateChange(func(from, to State) {
		done <- fmt.Sprintf("%s->%s", from, to)
	})

	c.transitionTo(StateActive)

	select {
	case got := <-done:
		assert.Equal(t, "recovering->active", got)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for state callback")
	}
}
