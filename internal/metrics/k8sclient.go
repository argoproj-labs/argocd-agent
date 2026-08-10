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
	"context"
	neturl "net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	k8smetrics "k8s.io/client-go/tools/metrics"
)

// RegisterK8sClientMetrics wires the standard client-go REST client metrics
// (request latency, rate-limiter latency, request totals, retries) into the
// default Prometheus registry using the conventional rest_client_* names.
// It is safe to call multiple times; client-go only registers once.
func RegisterK8sClientMetrics() {
	requestLatency := promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "rest_client_request_duration_seconds",
		Help:    "Request latency in seconds. Broken down by verb and host.",
		Buckets: prometheus.DefBuckets,
	}, []string{"verb", "host"})

	rateLimiterLatency := promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "rest_client_rate_limiter_duration_seconds",
		Help:    "Client-side rate limiter latency in seconds. Broken down by verb and host.",
		Buckets: prometheus.DefBuckets,
	}, []string{"verb", "host"})

	requestResult := promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rest_client_requests_total",
		Help: "Number of HTTP requests, partitioned by status code, method, and host.",
	}, []string{"code", "method", "host"})

	requestRetries := promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rest_client_request_retries_total",
		Help: "Number of request retries, partitioned by status code, method, and host.",
	}, []string{"code", "method", "host"})

	k8smetrics.Register(k8smetrics.RegisterOpts{
		RequestLatency:     &latencyAdapter{requestLatency},
		RateLimiterLatency: &latencyAdapter{rateLimiterLatency},
		RequestResult:      &resultAdapter{requestResult},
		RequestRetry:       &retryAdapter{requestRetries},
	})
}

type latencyAdapter struct {
	m *prometheus.HistogramVec
}

func (a *latencyAdapter) Observe(_ context.Context, verb string, u neturl.URL, latency time.Duration) {
	a.m.WithLabelValues(verb, u.Host).Observe(latency.Seconds())
}

type resultAdapter struct {
	m *prometheus.CounterVec
}

func (a *resultAdapter) Increment(_ context.Context, code, method, host string) {
	a.m.WithLabelValues(code, method, host).Inc()
}

type retryAdapter struct {
	m *prometheus.CounterVec
}

func (a *retryAdapter) IncrementRetry(_ context.Context, code, method, host string) {
	a.m.WithLabelValues(code, method, host).Inc()
}
