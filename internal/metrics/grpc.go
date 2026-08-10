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
	"sync"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	serverGRPCMetrics     *grpcprom.ServerMetrics
	serverGRPCMetricsOnce sync.Once

	clientGRPCMetrics     *grpcprom.ClientMetrics
	clientGRPCMetricsOnce sync.Once
)

// NewServerGRPCMetrics creates and registers gRPC server-side Prometheus
// metrics (counters for started/handled/msg_received/msg_sent RPCs, plus
// a handling-time histogram). The returned ServerMetrics exposes
// UnaryServerInterceptor and StreamServerInterceptor methods to wire
// into the gRPC server interceptor chain.
func NewServerGRPCMetrics() *grpcprom.ServerMetrics {
	serverGRPCMetricsOnce.Do(func() {
		serverGRPCMetrics = grpcprom.NewServerMetrics(
			grpcprom.WithServerHandlingTimeHistogram(
				grpcprom.WithHistogramBuckets(prometheus.DefBuckets),
			),
		)
		prometheus.MustRegister(serverGRPCMetrics)
	})
	return serverGRPCMetrics
}

// NewClientGRPCMetrics creates and registers gRPC client-side Prometheus
// metrics. These are used by the agent when connecting to the principal.
// The returned ClientMetrics exposes UnaryClientInterceptor and
// StreamClientInterceptor methods to wire into the gRPC dial options.
func NewClientGRPCMetrics() *grpcprom.ClientMetrics {
	clientGRPCMetricsOnce.Do(func() {
		clientGRPCMetrics = grpcprom.NewClientMetrics(
			grpcprom.WithClientHandlingTimeHistogram(
				grpcprom.WithHistogramBuckets(prometheus.DefBuckets),
			),
		)
		prometheus.MustRegister(clientGRPCMetrics)
	})
	return clientGRPCMetrics
}
