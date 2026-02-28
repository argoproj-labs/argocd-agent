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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateReplicating, "replicating"},
		{StateDisconnected, "disconnected"},
		{StateActive, "active"},
		{StateRecovering, "recovering"},
		{StateSyncing, "syncing"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestRoleString(t *testing.T) {
	assert.Equal(t, "primary", RolePrimary.String())
	assert.Equal(t, "replica", RoleReplica.String())
}

func TestStateIsHealthy(t *testing.T) {
	healthyStates := []State{StateActive}
	unhealthyStates := []State{StateReplicating, StateDisconnected, StateRecovering, StateSyncing}

	for _, s := range healthyStates {
		t.Run(s.String()+"_healthy", func(t *testing.T) {
			assert.True(t, s.IsHealthy())
		})
	}

	for _, s := range unhealthyStates {
		t.Run(s.String()+"_unhealthy", func(t *testing.T) {
			assert.False(t, s.IsHealthy())
		})
	}
}

func TestStateShouldAcceptAgents(t *testing.T) {
	acceptingStates := []State{StateActive}
	rejectingStates := []State{StateReplicating, StateDisconnected, StateRecovering, StateSyncing}

	for _, s := range acceptingStates {
		t.Run(s.String()+"_accepts", func(t *testing.T) {
			assert.True(t, s.ShouldAcceptAgents())
		})
	}

	for _, s := range rejectingStates {
		t.Run(s.String()+"_rejects", func(t *testing.T) {
			assert.False(t, s.ShouldAcceptAgents())
		})
	}
}

func TestStateIsReplicating(t *testing.T) {
	replicatingStates := []State{StateReplicating, StateSyncing}
	nonReplicatingStates := []State{StateDisconnected, StateActive, StateRecovering}

	for _, s := range replicatingStates {
		t.Run(s.String()+"_is_replicating", func(t *testing.T) {
			assert.True(t, s.IsReplicating())
		})
	}

	for _, s := range nonReplicatingStates {
		t.Run(s.String()+"_not_replicating", func(t *testing.T) {
			assert.False(t, s.IsReplicating())
		})
	}
}

func TestParseRole(t *testing.T) {
	t.Run("primary", func(t *testing.T) {
		role, err := ParseRole("primary")
		require.NoError(t, err)
		assert.Equal(t, RolePrimary, role)
	})

	t.Run("replica", func(t *testing.T) {
		role, err := ParseRole("replica")
		require.NoError(t, err)
		assert.Equal(t, RoleReplica, role)
	})

	t.Run("unknown", func(t *testing.T) {
		_, err := ParseRole("unknown")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown role")
	})
}

func TestValidTransition(t *testing.T) {
	validTransitions := []struct {
		from State
		to   State
	}{
		// From recovering
		{StateRecovering, StateActive},
		{StateRecovering, StateSyncing},
		{StateRecovering, StateReplicating},
		{StateRecovering, StateDisconnected},

		// From replicating (operator promote or stream break)
		{StateReplicating, StateDisconnected},
		{StateReplicating, StateActive},

		// From disconnected (replication reconnects or operator promote)
		{StateDisconnected, StateReplicating},
		{StateDisconnected, StateActive},

		// From active (operator demote)
		{StateActive, StateReplicating},

		// From syncing
		{StateSyncing, StateReplicating},
	}

	for _, tt := range validTransitions {
		t.Run(tt.from.String()+"->"+tt.to.String(), func(t *testing.T) {
			assert.True(t, ValidTransition(tt.from, tt.to), "expected transition from %s to %s to be valid", tt.from, tt.to)
		})
	}

	invalidTransitions := []struct {
		from State
		to   State
	}{
		// Cannot go from active to anything except replicating
		{StateActive, StateDisconnected},
		{StateActive, StateRecovering},

		// Cannot go from replicating directly to recovering
		{StateReplicating, StateRecovering},

		// Cannot transition to recovering from anywhere
		{StateActive, StateRecovering},

		// Same state is not a valid transition
		{StateActive, StateActive},
	}

	for _, tt := range invalidTransitions {
		t.Run(tt.from.String()+"->"+tt.to.String()+"_invalid", func(t *testing.T) {
			assert.False(t, ValidTransition(tt.from, tt.to), "expected transition from %s to %s to be invalid", tt.from, tt.to)
		})
	}
}
