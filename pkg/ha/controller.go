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
	"sync"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/sirupsen/logrus"
)

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ModuleLogger("ha_controller")
}

// Controller manages the HA state machine for a principal.
// All promotions are operator-triggered (Option B) — no automatic failover.
type Controller struct {
	mu sync.RWMutex

	// Current state
	state State

	// Configuration
	options *Options

	// Lifecycle management
	ctx      context.Context
	cancelFn context.CancelFunc

	// Metrics
	metrics *Metrics

	// Callbacks for integration with principal server
	onStateChange   func(from, to State)
	onBecomeActive  func(ctx context.Context)
	onBecomeReplica func(ctx context.Context)
}

// NewController creates a new HA controller
func NewController(ctx context.Context, opts ...Option) (*Controller, error) {
	options := DefaultOptions()
	for _, opt := range opts {
		if err := opt(options); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
	}

	c := &Controller{
		state:   StateRecovering,
		options: options,
		metrics: NewMetrics(),
	}

	c.ctx, c.cancelFn = context.WithCancel(ctx)

	return c, nil
}

// Start initializes the HA controller and determines the initial role.
// Primary always starts active. Replica starts in syncing — the replication
// client will connect and trigger OnReplicationConnected/OnReplicationDisconnected.
func (c *Controller) Start() error {
	if !c.options.Enabled {
		log().Info("HA is disabled, running as standalone primary")
		c.transitionTo(StateActive)
		return nil
	}

	log().WithField("preferred_role", c.options.PreferredRole).Info("Starting HA controller")

	c.metrics.RecordStateTransition(c.state, c.state)

	if c.options.PreferredRole == RolePrimary {
		log().Info("Starting as primary (active)")
		c.transitionTo(StateActive)
		if c.onBecomeActive != nil {
			c.onBecomeActive(c.ctx)
		}
		return nil
	}

	// Replica: start in syncing state — replication client handles connectivity
	log().Info("Starting as replica (syncing)")
	c.transitionTo(StateSyncing)
	return nil
}

// Shutdown gracefully stops the HA controller
func (c *Controller) Shutdown() error {
	log().Debug("HA controller shutdown requested")
	c.cancelFn()
	return nil
}

// State returns the current HA state
func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// IsHealthy returns true if GSLB should route traffic to this principal
func (c *Controller) IsHealthy() bool {
	return c.State().IsHealthy()
}

// ShouldAcceptAgents returns true if this principal should accept agent connections
func (c *Controller) ShouldAcceptAgents() bool {
	return c.State().ShouldAcceptAgents()
}

// OnAgentConnect is called when an agent attempts to connect.
// Returns an error if the connection should be rejected.
func (c *Controller) OnAgentConnect(agentName string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.state == StateActive {
		return nil
	}

	reason := "not accepting connections"
	c.metrics.RecordRejectedConnection(c.state, reason)
	return fmt.Errorf("not accepting connections in state %s", c.state)
}

// OnReplicationConnected is called when the replication stream is established
func (c *Controller) OnReplicationConnected() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == StateSyncing || c.state == StateDisconnected {
		c.transitionToLocked(StateReplicating)
	}
}

// OnReplicationDisconnected is called when the replication stream breaks
func (c *Controller) OnReplicationDisconnected() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == StateReplicating {
		c.transitionToLocked(StateDisconnected)
	}
}

// OnSyncComplete is called when initial sync from peer is complete
func (c *Controller) OnSyncComplete() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == StateSyncing {
		c.transitionToLocked(StateReplicating)
	}
}

// Promote transitions this principal to ACTIVE.
// Refuses if still replicating (peer is alive) unless force=true.
func (c *Controller) Promote(ctx context.Context, force bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == StateActive {
		return fmt.Errorf("already active")
	}

	if c.state != StateReplicating && c.state != StateDisconnected {
		return fmt.Errorf("can only promote from replicating or disconnected state, current: %s", c.state)
	}

	if !force && c.state == StateReplicating {
		return fmt.Errorf("still replicating from peer — demote peer first, or use --force")
	}

	log().Info("Operator triggered: promoting to active")
	c.transitionToLocked(StateActive)
	c.metrics.FailoverTotal.Inc()
	if c.onBecomeActive != nil {
		go c.onBecomeActive(c.ctx)
	}

	return nil
}

// Demote transitions this principal from ACTIVE to REPLICATING.
func (c *Controller) Demote(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state != StateActive {
		return fmt.Errorf("can only demote from active state, current: %s", c.state)
	}

	log().Info("Operator triggered: demoting to replica")
	c.transitionToLocked(StateReplicating)
	c.metrics.FailbackTotal.Inc()
	if c.onBecomeReplica != nil {
		go c.onBecomeReplica(c.ctx)
	}

	return nil
}

// SetOnStateChange sets the callback for state changes
func (c *Controller) SetOnStateChange(fn func(from, to State)) {
	c.onStateChange = fn
}

// SetOnBecomeActive sets the callback for when this principal becomes active
func (c *Controller) SetOnBecomeActive(fn func(ctx context.Context)) {
	c.onBecomeActive = fn
}

// SetOnBecomeReplica sets the callback for when this principal becomes a replica
func (c *Controller) SetOnBecomeReplica(fn func(ctx context.Context)) {
	c.onBecomeReplica = fn
}

// GetStatus returns the current HA status for the CLI/API
func (c *Controller) GetStatus() *Status {
	c.mu.RLock()
	state := c.state
	c.mu.RUnlock()

	return &Status{
		State:         state,
		PreferredRole: c.options.PreferredRole,
		PeerAddress:   c.options.PeerAddress,
		PeerReachable: state == StateReplicating,
		PeerState:     State(""),
	}
}

// Status represents the current HA status
type Status struct {
	State         State
	PreferredRole Role
	PeerAddress   string
	PeerReachable bool
	PeerState     State
}

// transitionTo transitions to a new state (acquires lock)
func (c *Controller) transitionTo(newState State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transitionToLocked(newState)
}

// transitionToLocked transitions to a new state (caller must hold lock)
func (c *Controller) transitionToLocked(newState State) {
	oldState := c.state

	if oldState == newState {
		return
	}

	if !ValidTransition(oldState, newState) {
		log().WithField("from", oldState).WithField("to", newState).Warn("Invalid state transition attempted")
		return
	}

	c.state = newState
	c.metrics.RecordStateTransition(oldState, newState)

	log().WithField("from", oldState).WithField("to", newState).Info("HA state transition")

	if c.onStateChange != nil {
		go c.onStateChange(oldState, newState)
	}
}

// Options returns the controller options (for testing)
func (c *Controller) Options() *Options {
	return c.options
}

// Metrics returns the controller metrics (for testing)
func (c *Controller) Metrics() *Metrics {
	return c.metrics
}
