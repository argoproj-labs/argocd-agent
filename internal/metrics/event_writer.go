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

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/prometheus/client_golang/prometheus"
)

const eventWriterLabelAgent = "agent"

// EventWriterLookup is implemented by *agent.Agent for scrape-time EventWriter metrics.
type EventWriterLookup interface {
	CurrentEventWriter() *event.EventWriter
}

type principalEventWriterGaugeCollector struct {
	writers *event.EventWritersMap
	descPending, descDue, descBackoff *prometheus.Desc
}

func newPrincipalEventWriterGaugeCollector(writers *event.EventWritersMap) *principalEventWriterGaugeCollector {
	return &principalEventWriterGaugeCollector{
		writers: writers,
		descPending: prometheus.NewDesc(
			"principal_event_writer_sent_pending",
			"Number of resources with a CloudEvent sent to the agent and awaiting ACK or resend",
			[]string{eventWriterLabelAgent},
			nil,
		),
		descDue: prometheus.NewDesc(
			"principal_event_writer_resend_due",
			"Subset of sent_pending where the next resend attempt is allowed (retry timer elapsed)",
			[]string{eventWriterLabelAgent},
			nil,
		),
		descBackoff: prometheus.NewDesc(
			"principal_event_writer_resend_backoff_wait",
			"Subset of sent_pending waiting on retry backoff before the next resend attempt",
			[]string{eventWriterLabelAgent},
			nil,
		),
	}
}

func (c *principalEventWriterGaugeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.descPending
	ch <- c.descDue
	ch <- c.descBackoff
}

func (c *principalEventWriterGaugeCollector) Collect(ch chan<- prometheus.Metric) {
	c.writers.ObserveWriters(func(agent string, ew *event.EventWriter) {
		ew.ObserveSentResendState(func(pending, due, backoff int) {
			ch <- prometheus.MustNewConstMetric(c.descPending, prometheus.GaugeValue, float64(pending), agent)
			ch <- prometheus.MustNewConstMetric(c.descDue, prometheus.GaugeValue, float64(due), agent)
			ch <- prometheus.MustNewConstMetric(c.descBackoff, prometheus.GaugeValue, float64(backoff), agent)
		})
	})
}

type agentEventWriterGaugeCollector struct {
	lookup EventWriterLookup
	descPending, descDue, descBackoff *prometheus.Desc
}

func newAgentEventWriterGaugeCollector(lookup EventWriterLookup) *agentEventWriterGaugeCollector {
	return &agentEventWriterGaugeCollector{
		lookup: lookup,
		descPending: prometheus.NewDesc(
			"agent_event_writer_sent_pending",
			"Number of resources with a CloudEvent sent to the principal and awaiting ACK or resend",
			[]string{eventWriterLabelAgent},
			nil,
		),
		descDue: prometheus.NewDesc(
			"agent_event_writer_resend_due",
			"Subset of sent_pending where the next resend attempt is allowed (retry timer elapsed)",
			[]string{eventWriterLabelAgent},
			nil,
		),
		descBackoff: prometheus.NewDesc(
			"agent_event_writer_resend_backoff_wait",
			"Subset of sent_pending waiting on retry backoff before the next resend attempt",
			[]string{eventWriterLabelAgent},
			nil,
		),
	}
}

func (c *agentEventWriterGaugeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.descPending
	ch <- c.descDue
	ch <- c.descBackoff
}

func (c *agentEventWriterGaugeCollector) Collect(ch chan<- prometheus.Metric) {
	ew := c.lookup.CurrentEventWriter()
	if ew == nil {
		return
	}
	const agentLabel = ""
	ew.ObserveSentResendState(func(pending, due, backoff int) {
		ch <- prometheus.MustNewConstMetric(c.descPending, prometheus.GaugeValue, float64(pending), agentLabel)
		ch <- prometheus.MustNewConstMetric(c.descDue, prometheus.GaugeValue, float64(due), agentLabel)
		ch <- prometheus.MustNewConstMetric(c.descBackoff, prometheus.GaugeValue, float64(backoff), agentLabel)
	})
}

func registerEventWriterCollector(c prometheus.Collector) {
	if err := prometheus.DefaultRegisterer.Register(c); err != nil {
		if _, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return
		}
		panic(err)
	}
}

var (
	principalEventWriterOnce sync.Once
	agentEventWriterOnce     sync.Once

	principalRetriesExhaustedDrop *prometheus.CounterVec
	agentRetriesExhaustedDrop     *prometheus.CounterVec
	dropCountersMu                sync.RWMutex
)

// RegisterPrincipalEventWriterMetrics registers EventWriter backlog gauges and the
// retry-exhausted drop counter on the default Prometheus registry (once per process).
func RegisterPrincipalEventWriterMetrics(writers *event.EventWritersMap) {
	principalEventWriterOnce.Do(func() {
		c := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "principal_event_writer_retries_exhausted_drop_total",
			Help: "CloudEvents dropped after exhausting send retries (principal to agent stream)",
		}, []string{eventWriterLabelAgent})
		if err := prometheus.DefaultRegisterer.Register(c); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if cv, ok := are.ExistingCollector.(*prometheus.CounterVec); ok {
					c = cv
				}
			} else {
				panic(err)
			}
		}
		dropCountersMu.Lock()
		principalRetriesExhaustedDrop = c
		dropCountersMu.Unlock()
		registerEventWriterCollector(newPrincipalEventWriterGaugeCollector(writers))
	})
}

// RegisterAgentEventWriterMetrics registers EventWriter backlog gauges and the
// retry-exhausted drop counter on the default Prometheus registry (once per process).
func RegisterAgentEventWriterMetrics(lookup EventWriterLookup) {
	agentEventWriterOnce.Do(func() {
		c := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_event_writer_retries_exhausted_drop_total",
			Help: "CloudEvents dropped after exhausting send retries (agent to principal stream)",
		}, []string{eventWriterLabelAgent})
		if err := prometheus.DefaultRegisterer.Register(c); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if cv, ok := are.ExistingCollector.(*prometheus.CounterVec); ok {
					c = cv
				}
			} else {
				panic(err)
			}
		}
		dropCountersMu.Lock()
		agentRetriesExhaustedDrop = c
		dropCountersMu.Unlock()
		registerEventWriterCollector(newAgentEventWriterGaugeCollector(lookup))
	})
}

// IncPrincipalEventWriterRetriesExhaustedDrop increments the drop counter for an agent stream.
func IncPrincipalEventWriterRetriesExhaustedDrop(agent string) {
	dropCountersMu.RLock()
	c := principalRetriesExhaustedDrop
	dropCountersMu.RUnlock()
	if c == nil {
		return
	}
	c.WithLabelValues(agent).Inc()
}

// IncAgentEventWriterRetriesExhaustedDrop increments the agent-side drop counter.
func IncAgentEventWriterRetriesExhaustedDrop() {
	dropCountersMu.RLock()
	c := agentRetriesExhaustedDrop
	dropCountersMu.RUnlock()
	if c == nil {
		return
	}
	c.WithLabelValues("").Inc()
}
