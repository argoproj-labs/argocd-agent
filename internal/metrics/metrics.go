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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	// "github.com/prometheus/client_golang/prometheus/promhttp"
)

// ConnectionTimeMap is an utility for AvgAgentConnectionTime
var ConnectionTimeMap = make(map[string]time.Time)

type ApplicationMetrics struct {
	ApplicationWatcherMetrics
	ApplicationClientMetrics
}

type AppProjectMetrics struct {
	AppProjectClientMetrics
	AppProjectWatcherMetrics
}

type InformerMetrics struct {
	ResourcesListed *prometheus.GaugeVec
	ListDuration    *prometheus.GaugeVec
	AddDuration     *prometheus.GaugeVec
	UpdateDuration  *prometheus.GaugeVec
	DeleteDuration  *prometheus.GaugeVec
}

// ServerMetrics holds metrics about connected Agent at Principal side
type ServerMetrics struct {
	AgentConnected         prometheus.Gauge
	AvgAgentConnectionTime prometheus.Gauge
}

// ApplicationWatcherMetrics holds metrics about Applications watched by the agent
type ApplicationWatcherMetrics struct {
	AppsWatched      prometheus.Gauge
	AppsAdded        prometheus.Counter
	AppsUpdated      prometheus.Counter
	AppsRemoved      prometheus.Counter
	AppWatcherErrors prometheus.Counter
}

type ApplicationClientMetrics struct {
	AppsCreated          *prometheus.CounterVec
	AppsUpdated          *prometheus.CounterVec
	AppsStatusUpdated    *prometheus.CounterVec
	AppsOperationUpdated *prometheus.CounterVec
	AppsDeleted          *prometheus.CounterVec
	AppClientErrors      prometheus.Counter
}

type AppProjectWatcherMetrics struct {
	ProjectsWatched prometheus.Gauge
	ProjectsAdded   prometheus.Counter
	ProjectsUpdated prometheus.Counter
	ProjectsRemoved prometheus.Counter
	ProjectErrors   prometheus.Counter
}

type AppProjectClientMetrics struct {
	AppProjectsCreated          *prometheus.CounterVec
	AppProjectsUpdated          *prometheus.CounterVec
	AppProjectsStatusUpdated    *prometheus.CounterVec
	AppProjectsOperationUpdated *prometheus.CounterVec
	AppProjectsDeleted          *prometheus.CounterVec
	ProjectClientErrors         prometheus.Counter
}

func NewInformerMetrics(label string) *InformerMetrics {
	im := &InformerMetrics{
		ResourcesListed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "argocd_agent_informer_list_num_resources",
			Help: "The number of resources seen by this informer",
		}, []string{label}),
		ListDuration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "argocd_agent_informer_list_duration",
			Help: "The time it took to list resources (in seconds)",
		}, []string{label}),
	}
	return im
}

// NewApplicationWatcherMetrics returns a new instance of ApplicationMetrics
func NewApplicationWatcherMetrics() *ApplicationWatcherMetrics {
	am := &ApplicationWatcherMetrics{
		AppsWatched: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "argocd_agent_watcher_applications_watched",
			Help: "The total number of applications watched by the agent",
		}),
		AppsAdded: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_watcher_applications_added",
			Help: "The number of applications that have been added to the agent",
		}),
		AppsUpdated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_watcher_applications_updated",
			Help: "The number of applications that have been updated",
		}),
		AppsRemoved: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_watcher_applications_removed",
			Help: "The number of applications that have been removed from the agent",
		}),
	}
	return am
}

func NewServerMetricsMetrics() *ServerMetrics {
	return &ServerMetrics{
		AgentConnected: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "argocd_agent_connected_with_principal",
			Help: "The total number of agents connected with principal",
		}),
		AvgAgentConnectionTime: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "argocd_agent_avg_connection_time",
			Help: "The average time all agents are connected for",
		}),
	}
}

func NewAppProjectClientMetrics() *AppProjectClientMetrics {
	return &AppProjectClientMetrics{
		AppProjectsCreated: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_appprojects_created",
			Help: "The total number of appprojects created by the appproject client",
		}, []string{"namespace"}),
		AppProjectsUpdated: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_appprojects_updated",
			Help: "The total number of appprojects updated by the appproject client",
		}, []string{"namespace"}),
		AppProjectsStatusUpdated: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_appprojects_status_updated",
			Help: "The total number of appprojects status updated by the appproject client",
		}, []string{"namespace"}),
		AppProjectsOperationUpdated: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_appprojects_operation_updated",
			Help: "The total number of appprojects operation updated by the appproject client",
		}, []string{"namespace"}),
		AppProjectsDeleted: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_appprojects_deleted",
			Help: "The total number of appprojects deleted by the appproject client",
		}, []string{"namespace"}),
		ProjectClientErrors: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_client_appprojects_errors",
			Help: "The total number of appproject errors reported by the appproject client",
		}),
	}
}

func NewAppProjectWatcherMetrics() *AppProjectWatcherMetrics {
	am := &AppProjectWatcherMetrics{
		ProjectsWatched: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "argocd_agent_watcher_appprojects_watched",
			Help: "The total number of AppProjects watched by the agent",
		}),
		ProjectsAdded: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_watcher_appprojects_added",
			Help: "The number of AppProjects that have been added to the agent",
		}),
		ProjectsUpdated: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_watcher_appprojects_updated",
			Help: "The number of AppProjects that have been updated",
		}),
		ProjectsRemoved: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_watcher_appprojects_removed",
			Help: "The number of AppProjects that have been removed from the agent",
		}),
	}
	return am
}

func NewApplicationClientMetrics() *ApplicationClientMetrics {
	return &ApplicationClientMetrics{
		AppsCreated: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_applications_created",
			Help: "The total number of applications created by the application client",
		}, []string{"namespace"}),
		AppsUpdated: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_applications_updated",
			Help: "The total number of applications updated by the application client",
		}, []string{"namespace"}),
		AppsStatusUpdated: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_applications_status_updated",
			Help: "The total number of applications status updated by the application client",
		}, []string{"namespace"}),
		AppsOperationUpdated: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_applications_operation_updated",
			Help: "The total number of applications operation updated by the application client",
		}, []string{"namespace"}),
		AppsDeleted: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "argocd_agent_client_applications_deleted",
			Help: "The total number of applications deleted by the application client",
		}, []string{"namespace"}),
		AppClientErrors: promauto.NewCounter(prometheus.CounterOpts{
			Name: "argocd_agent_client_applications_errors",
			Help: "The total number of application errors reported by the application client",
		}),
	}
}

// func (am *ApplicationWatcherMetrics) SetWatched(num int64) {
// 	am.AppsWatched.Set(float64(num))
// }

// func (am *ApplicationWatcherMetrics) AppAdded() {
// 	am.AppsWatched.Inc()
// 	am.AppsAdded.Inc()
// }

// func (am *ApplicationWatcherMetrics) AppRemoved() {
// 	am.AppsWatched.Dec()
// 	am.AppsRemoved.Inc()
// }

// func (am *ApplicationWatcherMetrics) AppUpdated() {
// 	am.AppsUpdated.Inc()
// }

// func (am *ApplicationWatcherMetrics) Error() {
// 	am.Errors.Inc()
// }
