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

	"github.com/argoproj-labs/argocd-agent/internal/version"
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

	AppSetCreated prometheus.Counter
	AppSetUpdated prometheus.Counter
	AppSetDeleted prometheus.Counter

	GPGKeyCount prometheus.Gauge

	EventReceived prometheus.Counter
	EventSent     prometheus.Counter

	EventProcessingTime        *prometheus.HistogramVec
	EventWriterSendErrors      *prometheus.CounterVec
	EventWriterEventsDiscarded *prometheus.CounterVec

	PrincipalErrors *prometheus.CounterVec

	AgentConnectionCount *prometheus.CounterVec

	ResourceProxyRequests *prometheus.CounterVec
	ResourceProxyErrors   *prometheus.CounterVec

	RedisProxyRequests *prometheus.CounterVec
	RedisProxyErrors   *prometheus.CounterVec
}

// AgentMetrics holds metrics of agent
type AgentMetrics struct {
	EventReceived              prometheus.Counter
	EventSent                  prometheus.Counter
	EventProcessingTime        *prometheus.HistogramVec
	PropagationLatency         *prometheus.HistogramVec
	EventWriterEventsDiscarded *prometheus.CounterVec
	AgentErrors                *prometheus.CounterVec
	ConnectionStatus           prometheus.Gauge
	ConnectionStartTimestamp   prometheus.Gauge
	ConnectionCount            prometheus.Counter
	AuthFailures               prometheus.Counter

	ResourceProxyRequests prometheus.Counter
	ResourceProxyErrors   prometheus.Counter
	RedisProxyRequests    *prometheus.CounterVec
	RedisProxyErrors      *prometheus.CounterVec
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
			Name: "argocd_principal_connected_agents",
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

		AppSetCreated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_principal_appsets_created",
			Help: "The total number of ApplicationSets created on the control plane",
		}),
		AppSetUpdated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_principal_appsets_updated",
			Help: "The total number of ApplicationSets updated on the control plane",
		}),
		AppSetDeleted: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_principal_appsets_deleted",
			Help: "The total number of ApplicationSets deleted on the control plane",
		}),

		GPGKeyCount: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "argocd_principal_gpg_keys_count",
			Help: "The current number of GPG keys on the control plane",
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

		EventWriterSendErrors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "principal_event_writer_send_errors_total",
			Help: "The total number of EventWriter send errors observed by principal",
		}, []string{"agent_name", "reason"}),

		EventWriterEventsDiscarded: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_principal_event_writer_events_discarded_total",
			Help: "The total number of events discarded by the EventWriter after exhausting retries",
		}, []string{"agent_name", "event_type", "resource_type"}),

		PrincipalErrors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "principal_errors",
			Help: "The total number of errors occurred in principal",
		}, []string{"resource_type"}),

		AgentConnectionCount: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_principal_agent_connections_total",
			Help: "The total number of successful connections from each agent to the principal",
		}, []string{"agent_name"}),

		ResourceProxyRequests: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_principal_resource_proxy_requests_total",
			Help: "The total number of resource proxy requests received",
		}, []string{"agent_name"}),
		ResourceProxyErrors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_principal_resource_proxy_errors_total",
			Help: "The total number of resource proxy request failures",
		}, []string{"agent_name", "reason"}),

		RedisProxyRequests: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_principal_redis_proxy_requests_total",
			Help: "The total number of Redis proxy requests forwarded to agents",
		}, []string{"agent_name", "command"}),
		RedisProxyErrors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_principal_redis_proxy_errors_total",
			Help: "The total number of Redis proxy request failures",
		}, []string{"agent_name", "command"}),
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

		EventWriterEventsDiscarded: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_event_writer_events_discarded_total",
			Help: "The total number of events discarded by the EventWriter after exhausting retries",
		}, []string{"event_type", "resource_type"}),

		AgentErrors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_errors",
			Help: "The total number of errors occurred in agent",
		}, []string{"resource_type"}),

		ConnectionStatus: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "argocd_agent_connection_status",
			Help: "Whether the agent is currently connected to the principal (1 = connected, 0 = disconnected)",
		}),
		ConnectionStartTimestamp: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "argocd_agent_connection_start_timestamp_seconds",
			Help: "Unix timestamp of when the current connection to the principal was established",
		}),
		ConnectionCount: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_connections_total",
			Help: "The total number of successful connections from the agent to the principal",
		}),

		AuthFailures: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_auth_failures_total",
			Help: "The total number of authentication failures when connecting to the principal",
		}),
		ResourceProxyRequests: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_resource_proxy_requests_total",
			Help: "The total number of resource proxy requests processed by the agent",
		}),
		ResourceProxyErrors: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_resource_proxy_errors_total",
			Help: "The total number of resource proxy request failures on the agent",
		}),
		RedisProxyRequests: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_redis_proxy_requests_total",
			Help: "The total number of Redis proxy requests processed by the agent",
		}, []string{"command"}),
		RedisProxyErrors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_redis_proxy_errors_total",
			Help: "The total number of Redis proxy request failures on the agent",
		}, []string{"command"}),
	}
}

// RegisterBuildInfo registers a gauge that exports build metadata as labels.
func RegisterBuildInfo(v *version.Version) {
	promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "argocd_agent_build_info",
		Help: "Build metadata for the running argocd-agent binary",
	}, []string{"version", "git_revision"}).
		WithLabelValues(v.Version(), v.GitRevision()).
		Set(1)
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
