// Copyright 2026 The argocd-agent Authors
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
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/util/workqueue"
)

var _ workqueue.MetricsProvider = &QueueMetrics{}

var dqMetricsProvider *DedupeQueueMetrics

// QueueMetrics implements the workqueue.MetricsProvider interface,
// providing metrics for our send/recv queues.
type QueueMetrics struct {
	depth                   *prometheus.GaugeVec
	adds                    *prometheus.CounterVec
	latency                 *prometheus.HistogramVec
	workDuration            *prometheus.HistogramVec
	unfinishedWorkSeconds   *prometheus.GaugeVec
	longestRunningProcessor *prometheus.GaugeVec
	retries                 *prometheus.CounterVec
}

type gauge struct{ prometheus.Gauge }
type counter struct{ prometheus.Counter }
type histogram struct{ prometheus.Observer }

func (g gauge) Inc()          { g.Gauge.Inc() }
func (g gauge) Dec()          { g.Gauge.Dec() }
func (g gauge) Set(v float64) { g.Gauge.Set(v) }

func (c counter) Inc()          { c.Counter.Inc() }
func (c counter) Add(v float64) { c.Counter.Add(v) }

func (h histogram) Observe(v float64) { h.Observer.Observe(v) }

func (p *QueueMetrics) NewDepthMetric(name string) workqueue.GaugeMetric {
	return gauge{p.depth.WithLabelValues(name)}
}

func (p *QueueMetrics) NewAddsMetric(name string) workqueue.CounterMetric {
	return counter{p.adds.WithLabelValues(name)}
}

func (p *QueueMetrics) NewLatencyMetric(name string) workqueue.HistogramMetric {
	return histogram{p.latency.WithLabelValues(name)}
}

func (p *QueueMetrics) NewWorkDurationMetric(name string) workqueue.HistogramMetric {
	return histogram{p.workDuration.WithLabelValues(name)}
}

func (p *QueueMetrics) NewUnfinishedWorkSecondsMetric(name string) workqueue.SettableGaugeMetric {
	return gauge{p.unfinishedWorkSeconds.WithLabelValues(name)}
}

func (p *QueueMetrics) NewLongestRunningProcessorSecondsMetric(name string) workqueue.SettableGaugeMetric {
	return gauge{p.longestRunningProcessor.WithLabelValues(name)}
}

func (p *QueueMetrics) NewRetriesMetric(name string) workqueue.CounterMetric {
	return counter{p.retries.WithLabelValues(name)}
}

// RegisterQueueMetrics registers the queue metrics with the given prefix.
// The prefix should be the component's name.
// This function must only be called once per process, otherwise it will panic.
func RegisterQueueMetrics(prefix string) {
	provider := &QueueMetrics{
		depth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: prefix + "_workqueue_depth",
				Help: "Current depth of workqueue",
			},
			[]string{"queue"},
		),
		adds: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: prefix + "_workqueue_adds_total",
				Help: "Total number of adds to the workqueue",
			},
			[]string{"queue"},
		),
		latency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    prefix + "_workqueue_queue_duration_seconds",
				Help:    "How long items stay in queue before being processed",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"queue"},
		),
		workDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    prefix + "_workqueue_work_duration_seconds",
				Help:    "How long processing an item takes",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"queue"},
		),
		unfinishedWorkSeconds: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: prefix + "_workqueue_unfinished_work_seconds",
				Help: "Total time of unfinished work",
			},
			[]string{"queue"},
		),
		longestRunningProcessor: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: prefix + "_workqueue_longest_running_processor_seconds",
				Help: "Longest running processor duration",
			},
			[]string{"queue"},
		),
		retries: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: prefix + "_workqueue_retries_total",
				Help: "Total number of retries",
			},
			[]string{"queue"},
		),
	}
	prometheus.DefaultRegisterer.MustRegister(
		provider.depth,
		provider.adds,
		provider.latency,
		provider.workDuration,
		provider.unfinishedWorkSeconds,
		provider.longestRunningProcessor,
		provider.retries,
	)
	workqueue.SetProvider(provider)
}

// DedupeQueueMetrics holds Prometheus metrics for dedupeQueue instances.
type DedupeQueueMetrics struct {
	Depth         *prometheus.GaugeVec
	Adds          *prometheus.CounterVec
	EventsDeduped *prometheus.CounterVec
	Duration      *prometheus.HistogramVec
}

func NewDedupeQueueMetrics(prefix string) *DedupeQueueMetrics {
	return &DedupeQueueMetrics{
		Depth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: prefix + "_queue_depth",
				Help: "Current depth of queue",
			},
			[]string{"queue"},
		),
		Adds: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: prefix + "_queue_adds_total",
				Help: "Total number of adds to the deduped queue",
			},
			[]string{"queue"},
		),
		EventsDeduped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: prefix + "_queue_events_deduped_total",
				Help: "Total number of events deduped from the queue",
			},
			[]string{"queue"},
		),
		Duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    prefix + "_queue_duration_seconds",
				Help:    "Time an event spent waiting in the queue before being dequeued",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"queue"},
		),
	}
}

func RegisterDedupeQueueMetrics(prefix string) {
	dqMetricsProvider = NewDedupeQueueMetrics(prefix)
	prometheus.DefaultRegisterer.MustRegister(
		dqMetricsProvider.Depth,
		dqMetricsProvider.Adds,
		dqMetricsProvider.EventsDeduped,
		dqMetricsProvider.Duration,
	)
}

func GetDedupeQueueMetrics() *DedupeQueueMetrics {
	return dqMetricsProvider
}
