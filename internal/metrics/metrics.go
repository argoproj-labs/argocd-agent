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

package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type EventProcessingStatus string

const (
	EventProcessingFail       EventProcessingStatus = "failure"
	EventProcessingSuccess    EventProcessingStatus = "success"
	EventProcessingDiscarded  EventProcessingStatus = "discarded"
	EventProcessingNotAllowed EventProcessingStatus = "not-allowed"
)

type InformerMetrics struct {
	ResourcesListed *prometheus.GaugeVec
	ListDuration    *prometheus.GaugeVec
	AddDuration     *prometheus.GaugeVec
	UpdateDuration  *prometheus.GaugeVec
	DeleteDuration  *prometheus.GaugeVec
}

// PrincipalMetrics holds metrics of principal
type PrincipalMetrics struct {
	AgentConnected         prometheus.Gauge
	AvgAgentConnectionTime prometheus.Gauge

	ApplicationCreated prometheus.Counter
	ApplicationUpdated prometheus.Counter
	ApplicationDeleted prometheus.Counter

	AppProjectCreated prometheus.Counter
	AppProjectUpdated prometheus.Counter
	AppProjectDeleted prometheus.Counter

	RepositoryCreated prometheus.Counter
	RepositoryUpdated prometheus.Counter
	RepositoryDeleted prometheus.Counter

	GPGKeyCreated prometheus.Counter
	GPGKeyUpdated prometheus.Counter
	GPGKeyDeleted prometheus.Counter

	EventReceived prometheus.Counter
	EventSent     prometheus.Counter

	EventProcessingTime *prometheus.HistogramVec

	// Hop-by-hop latency for outbound events (principal → agent).
	// SendQueueDwell measures time from SendQ.Add to EventWriter.Add.
	// EventWriterDwell measures time from EventWriter.Add to wire send.
	// AckRoundtrip measures time from wire send to ACK received back.
	SendQueueDwell   *prometheus.HistogramVec
	EventWriterDwell *prometheus.HistogramVec
	AckRoundtrip     *prometheus.HistogramVec

	PrincipalErrors *prometheus.CounterVec
}

// AgentMetrics holds metrics of agent
type AgentMetrics struct {
	EventReceived       prometheus.Counter
	EventSent           prometheus.Counter
	EventProcessingTime *prometheus.HistogramVec
	PropagationLatency  *prometheus.HistogramVec
	AgentErrors         *prometheus.CounterVec
}

func NewInformerMetrics(label string) *InformerMetrics {
	im := &InformerMetrics{
		ResourcesListed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "argocd_agent_informer_list_num_resources",
			Help: "The number of resources seen by the informer",
		}, []string{label}),
		ListDuration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "argocd_agent_informer_list_duration",
			Help: "The time it took to list resources (in seconds)",
		}, []string{label}),
	}
	return im
}

func NewPrincipalMetrics() *PrincipalMetrics {
	return &PrincipalMetrics{
		AgentConnected: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "agent_connected_with_principal",
			Help: "The total number of agents connected with principal",
		}),
		AvgAgentConnectionTime: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "principal_agent_avg_connection_time",
			Help: "The average time all agents are connected for (in minutes)",
		}),

		ApplicationCreated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_applications_created",
			Help: "The total number of applications created on the control plane",
		}),
		ApplicationUpdated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_applications_updated",
			Help: "The total number of applications updated on the control plane",
		}),
		ApplicationDeleted: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_applications_deleted",
			Help: "The total number of applications deleted on the control plane",
		}),

		AppProjectCreated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_app_projects_created",
			Help: "The total number of app project created on the control plane",
		}),
		AppProjectUpdated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_app_projects_updated",
			Help: "The total number of app project updated on the control plane",
		}),
		AppProjectDeleted: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_app_projects_deleted",
			Help: "The total number of app project deleted on the control plane",
		}),

		RepositoryCreated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_repositories_created",
			Help: "The total number of repositories created on the control plane",
		}),
		RepositoryUpdated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_repositories_updated",
			Help: "The total number of repositories updated on the control plane",
		}),
		RepositoryDeleted: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_repositories_deleted",
			Help: "The total number of repositories deleted on the control plane",
		}),

		GPGKeyCreated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_gpg_keys_created",
			Help: "The total number of GPG keys created on the control plane",
		}),
		GPGKeyUpdated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_gpg_keys_updated",
			Help: "The total number of GPG keys updated on the control plane",
		}),
		GPGKeyDeleted: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_gpg_keys_deleted",
			Help: "The total number of GPG keys deleted on the control plane",
		}),

		EventReceived: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_events_received",
			Help: "The total number of events received by principal",
		}),
		EventSent: promauto.NewCounter(prometheus.CounterOpts{
			Name: "principal_events_sent",
			Help: "The total number of events sent by principal",
		}),

		EventProcessingTime: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name: "principal_event_processing_time",
			Help: "Histogram of time taken to process events (in seconds)",
		}, []string{"status", "agent_name", "resource_type"}),

		SendQueueDwell: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "principal_send_queue_dwell_seconds",
			Help:    "Time an outbound event spends in the principal send queue before the event writer picks it up",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"resource_type"}),

		EventWriterDwell: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "principal_event_writer_dwell_seconds",
			Help:    "Time an outbound event spends in the event writer between being queued and actually sent on the wire",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"resource_type"}),

		AckRoundtrip: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "principal_ack_roundtrip_seconds",
			Help:    "Time from when an outbound event is sent on the wire to when the ACK is received back from the agent",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"resource_type"}),

		PrincipalErrors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "principal_errors",
			Help: "The total number of errors occurred in principal",
		}, []string{"resource_type"}),
	}
}

func NewAgentMetrics() *AgentMetrics {
	return &AgentMetrics{
		EventReceived: promauto.NewCounter(prometheus.CounterOpts{
			Name: "agent_events_received",
			Help: "The total number of events received by agent",
		}),
		EventSent: promauto.NewCounter(prometheus.CounterOpts{
			Name: "agent_events_sent",
			Help: "The total number of events sent by agent",
		}),

		EventProcessingTime: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name: "agent_event_processing_time",
			Help: "Histogram of time taken to process events (in seconds)",
		}, []string{"status", "agent_mode", "resource_type"}),

		PropagationLatency: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "agent_event_propagation_latency_seconds",
			Help:    "Histogram of time from principal send to agent processing (in seconds)",
			Buckets: prometheus.DefBuckets,
		}, []string{"resource_type"}),

		AgentErrors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_errors",
			Help: "The total number of errors occurred in agent",
		}, []string{"resource_type"}),
	}
}

// AvgCalculationInterval is time interval for agent connection time calculation
var AvgCalculationInterval = 3 * time.Minute

// AgentConnectionTime is an utility for AvgAgentConnectionTime
type AgentConnectionTime struct {
	Lock sync.RWMutex

	// key: AgentName
	// value: connection time of agent
	// - acquire 'lock' before accessing
	ConnectionTimeMap map[string]time.Time
}

var agentConnectionTime = &AgentConnectionTime{
	ConnectionTimeMap: make(map[string]time.Time),
}

// SetAgentConnectionTime inserts connection time of new agent
func SetAgentConnectionTime(agentName string, start time.Time) {
	agentConnectionTime.Lock.Lock()
	defer agentConnectionTime.Lock.Unlock()

	agentConnectionTime.ConnectionTimeMap[agentName] = start
}

// DeleteAgentConnectionTime removed connection time of existing agent
func DeleteAgentConnectionTime(agentName string) {
	agentConnectionTime.Lock.Lock()
	defer agentConnectionTime.Lock.Unlock()

	delete(agentConnectionTime.ConnectionTimeMap, agentName)
}

// GetAvgAgentConnectionTime calculates average connection time of all connected agents
func GetAvgAgentConnectionTime(metrics *PrincipalMetrics) {
	agentConnectionTime.Lock.RLock()
	defer agentConnectionTime.Lock.RUnlock()

	var totalTime time.Duration

	// get SUM of time differences between current time and agent start time for all connected agents
	for _, t := range agentConnectionTime.ConnectionTimeMap {
		totalTime = totalTime + time.Since(t).Round(time.Minute)
	}

	if totalTime != 0 {
		// calculate average connection time
		avg := int(totalTime.Minutes()) / len(agentConnectionTime.ConnectionTimeMap)
		metrics.AvgAgentConnectionTime.Set(float64(avg))
	} else {
		// Set metrics to zero if there are no agents are connected
		metrics.AvgAgentConnectionTime.Set(float64(0))
	}
}
