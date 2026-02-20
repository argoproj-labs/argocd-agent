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

import "fmt"

// State represents the current HA state of a principal
type State string

const (
	// StateReplicating indicates the principal is connected to the primary and receiving replication events
	StateReplicating State = "replicating"

	// StateDisconnected indicates the principal lost connection to the primary
	StateDisconnected State = "disconnected"

	// StateActive indicates the principal is actively serving agent connections
	StateActive State = "active"

	// StateRecovering indicates the principal just started and is determining its role
	StateRecovering State = "recovering"

	// StateSyncing indicates the principal is catching up to the current primary after recovering
	StateSyncing State = "syncing"
)

// Role represents the preferred role of a principal
type Role string

const (
	// RolePrimary indicates this principal prefers to be the primary
	RolePrimary Role = "primary"

	// RoleReplica indicates this principal prefers to be a replica
	RoleReplica Role = "replica"
)

// String returns the string representation of the state
func (s State) String() string {
	return string(s)
}

// String returns the string representation of the role
func (r Role) String() string {
	return string(r)
}

// IsHealthy returns true if the state should report healthy to GSLB
func (s State) IsHealthy() bool {
	return s == StateActive
}

// ShouldAcceptAgents returns true if the state allows accepting agent connections
func (s State) ShouldAcceptAgents() bool {
	return s == StateActive
}

// IsReplicating returns true if the state indicates active replication
func (s State) IsReplicating() bool {
	return s == StateReplicating || s == StateSyncing
}

// ParseRole parses a string into a Role
func ParseRole(s string) (Role, error) {
	switch s {
	case "primary":
		return RolePrimary, nil
	case "replica":
		return RoleReplica, nil
	default:
		return "", fmt.Errorf("unknown role: %s", s)
	}
}

// ValidTransition checks if transitioning from one state to another is valid
func ValidTransition(from, to State) bool {
	validTransitions := map[State][]State{
		StateRecovering:   {StateActive, StateSyncing, StateReplicating, StateDisconnected},
		StateReplicating:  {StateDisconnected, StateActive},
		StateDisconnected: {StateReplicating, StateActive},
		StateActive:       {StateReplicating},
		StateSyncing:      {StateReplicating},
	}

	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}

	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}
