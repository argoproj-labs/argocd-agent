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
	"context"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

type metricsTestStream struct{}

func (metricsTestStream) Send(*eventstreamapi.Event) error { return nil }

func (metricsTestStream) Context() context.Context { return context.Background() }

type stubLookup struct {
	ew *event.EventWriter
}

func (s *stubLookup) CurrentEventWriter() *event.EventWriter {
	return s.ew
}

func gaugeValueForAgent(t *testing.T, mfs []*dto.MetricFamily, metricName, agent string) float64 {
	t.Helper()
	for _, mf := range mfs {
		if mf.GetName() != metricName {
			continue
		}
		for _, m := range mf.GetMetric() {
			var lbl string
			for _, lp := range m.GetLabel() {
				if lp.GetName() == eventWriterLabelAgent {
					lbl = lp.GetValue()
					break
				}
			}
			if lbl == agent {
				return m.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("metric %s for agent %q not found", metricName, agent)
	return 0
}

func TestPrincipalEventWriterGaugeCollector(t *testing.T) {
	m := event.NewEventWritersMap()
	reg := prometheus.NewPedanticRegistry()
	c := newPrincipalEventWriterGaugeCollector(m)
	require.NoError(t, reg.Register(c))

	mfs, err := reg.Gather()
	require.NoError(t, err)
	require.Empty(t, mfs)

	ew := event.NewEventWriter("agent-x", metricsTestStream{})
	m.Add("agent-x", ew)

	mfs, err = reg.Gather()
	require.NoError(t, err)
	require.NotEmpty(t, mfs)

	require.Equal(t, 0.0, gaugeValueForAgent(t, mfs, "principal_event_writer_sent_pending", "agent-x"))
	require.Equal(t, 0.0, gaugeValueForAgent(t, mfs, "principal_event_writer_resend_due", "agent-x"))
	require.Equal(t, 0.0, gaugeValueForAgent(t, mfs, "principal_event_writer_resend_backoff_wait", "agent-x"))
}

func TestAgentEventWriterGaugeCollector_nilWriter(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	c := newAgentEventWriterGaugeCollector(&stubLookup{ew: nil})
	require.NoError(t, reg.Register(c))
	mfs, err := reg.Gather()
	require.NoError(t, err)
	require.Empty(t, mfs)
}

func TestAgentEventWriterGaugeCollector_withWriter(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	ew := event.NewEventWriter("", metricsTestStream{})
	c := newAgentEventWriterGaugeCollector(&stubLookup{ew: ew})
	require.NoError(t, reg.Register(c))
	mfs, err := reg.Gather()
	require.NoError(t, err)
	require.NotEmpty(t, mfs)
	require.Equal(t, 0.0, gaugeValueForAgent(t, mfs, "agent_event_writer_sent_pending", ""))
}
