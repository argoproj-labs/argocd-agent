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

	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	queueDepthLabelQueue     = "queue"
	queueDepthLabelDirection = "direction"
)

type eventQueueDepthCollector struct {
	queues *queue.SendRecvQueues
	desc   *prometheus.Desc
}

func newEventQueueDepthCollector(metricName, help string, q *queue.SendRecvQueues) prometheus.Collector {
	return &eventQueueDepthCollector{
		queues: q,
		desc: prometheus.NewDesc(
			metricName,
			help,
			[]string{queueDepthLabelQueue, queueDepthLabelDirection},
			nil,
		),
	}
}

func (c *eventQueueDepthCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *eventQueueDepthCollector) Collect(ch chan<- prometheus.Metric) {
	c.queues.ObserveDepths(func(name string, sendLen, recvLen int) {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(sendLen), name, "send")
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(recvLen), name, "recv")
	})
}

func registerQueueDepthCollector(c prometheus.Collector) {
	if err := prometheus.DefaultRegisterer.Register(c); err != nil {
		if _, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return
		}
		panic(err)
	}
}

var (
	principalQueueDepthRegisterOnce sync.Once
	agentQueueDepthRegisterOnce    sync.Once
)

// RegisterPrincipalEventQueueDepthCollector registers a collector for
// principal_event_queue_depth on the default Prometheus registry. It runs at
// most once per process; later calls are ignored (see sync.Once).
func RegisterPrincipalEventQueueDepthCollector(q *queue.SendRecvQueues) {
	principalQueueDepthRegisterOnce.Do(func() {
		c := newEventQueueDepthCollector(
			"principal_event_queue_depth",
			"Number of CloudEvents waiting in the principal send or receive queue for a queue pair",
			q,
		)
		registerQueueDepthCollector(c)
	})
}

// RegisterAgentEventQueueDepthCollector registers a collector for
// agent_event_queue_depth on the default Prometheus registry. It runs at most
// once per process; later calls are ignored (see sync.Once).
func RegisterAgentEventQueueDepthCollector(q *queue.SendRecvQueues) {
	agentQueueDepthRegisterOnce.Do(func() {
		c := newEventQueueDepthCollector(
			"agent_event_queue_depth",
			"Number of CloudEvents waiting in the agent send or receive queue for a queue pair",
			q,
		)
		registerQueueDepthCollector(c)
	})
}
