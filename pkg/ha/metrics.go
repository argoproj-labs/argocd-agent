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
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsOnce     sync.Once
	metricsInstance *Metrics
)

// Metrics contains Prometheus metrics for the HA controller
type Metrics struct {
	// StateGauge reports the current HA state (1 for active state, 0 for others)
	StateGauge *prometheus.GaugeVec

	// TransitionCounter counts state transitions by target state
	TransitionCounter *prometheus.CounterVec

	// ReplicationLagGauge reports the replication lag in seconds
	ReplicationLagGauge prometheus.Gauge

	// ReplicationEventsTotal counts total replication events received
	ReplicationEventsTotal prometheus.Counter

	// ReplicationErrorsTotal counts replication errors
	ReplicationErrorsTotal prometheus.Counter

	// FailoverTotal counts total failover events
	FailoverTotal prometheus.Counter

	// FailbackTotal counts total failback events
	FailbackTotal prometheus.Counter

	// AgentConnectionsRejected counts agent connections rejected due to HA state
	AgentConnectionsRejected *prometheus.CounterVec
}

// NewMetrics creates new HA metrics. It returns a singleton instance to avoid
// duplicate metric registration when multiple controllers are created (e.g., in tests).
func NewMetrics() *Metrics {
	metricsOnce.Do(func() {
		metricsInstance = &Metrics{
			StateGauge: promauto.NewGaugeVec(prometheus.GaugeOpts{
				Name: "argocd_agent_ha_state",
				Help: "Current HA state: 1 for the active state, 0 for all others",
			}, []string{"state"}),
			TransitionCounter: promauto.NewCounterVec(prometheus.CounterOpts{
				Name: "argocd_agent_ha_transitions_total",
				Help: "Total number of HA state transitions",
			}, []string{"from_state", "to_state"}),
			ReplicationLagGauge: promauto.NewGauge(prometheus.GaugeOpts{
				Name: "argocd_agent_ha_replication_lag_seconds",
				Help: "Replication lag in seconds",
			}),
			ReplicationEventsTotal: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_ha_replication_events_total",
				Help: "Total number of replication events received",
			}),
			ReplicationErrorsTotal: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_ha_replication_errors_total",
				Help: "Total number of replication errors",
			}),
			FailoverTotal: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_ha_failovers_total",
				Help: "Total number of failover events",
			}),
			FailbackTotal: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_ha_failbacks_total",
				Help: "Total number of failback events",
			}),
			AgentConnectionsRejected: promauto.NewCounterVec(prometheus.CounterOpts{
				Name: "argocd_agent_ha_connections_rejected_total",
				Help: "Total number of agent connections rejected due to HA state",
			}, []string{"state", "reason"}),
		}
	})
	return metricsInstance
}

// allStates enumerates every HA state for metric initialization.
var allStates = []State{StateReplicating, StateDisconnected, StateActive, StateRecovering, StateSyncing}

// RecordStateTransition records a state transition in metrics
func (m *Metrics) RecordStateTransition(from, to State) {
	for _, s := range allStates {
		if s == to {
			m.StateGauge.WithLabelValues(s.String()).Set(1)
		} else {
			m.StateGauge.WithLabelValues(s.String()).Set(0)
		}
	}
	m.TransitionCounter.WithLabelValues(from.String(), to.String()).Inc()
}

// RecordReplicationLag records the current replication lag
func (m *Metrics) RecordReplicationLag(lagSeconds float64) {
	m.ReplicationLagGauge.Set(lagSeconds)
}

// RecordReplicationEvent records a replication event
func (m *Metrics) RecordReplicationEvent() {
	m.ReplicationEventsTotal.Inc()
}

// RecordReplicationError records a replication error
func (m *Metrics) RecordReplicationError() {
	m.ReplicationErrorsTotal.Inc()
}

// RecordFailover records a failover event
func (m *Metrics) RecordFailover() {
	m.FailoverTotal.Inc()
}

// RecordFailback records a failback event
func (m *Metrics) RecordFailback() {
	m.FailbackTotal.Inc()
}

// RecordRejectedConnection records a rejected agent connection
func (m *Metrics) RecordRejectedConnection(state State, reason string) {
	m.AgentConnectionsRejected.WithLabelValues(state.String(), reason).Inc()
}
