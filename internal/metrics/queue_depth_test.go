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
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/cloudevents/sdk-go/v2/event"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/client_golang/prometheus"
)

func labelValue(labels []*dto.LabelPair, name string) string {
	for _, lp := range labels {
		if lp.GetName() == name {
			return lp.GetValue()
		}
	}
	return ""
}

func TestEventQueueDepthCollector(t *testing.T) {
	q := queue.NewSendRecvQueues()
	require.NoError(t, q.Create("agent-a"))
	ev := event.New()
	ev.SetID("1")
	q.SendQ("agent-a").Add(&ev)
	ev2 := event.New()
	ev2.SetID("2")
	q.RecvQ("agent-a").Add(&ev2)
	q.RecvQ("agent-a").Add(&ev)

	reg := prometheus.NewPedanticRegistry()
	c := newEventQueueDepthCollector(
		"principal_event_queue_depth",
		"Number of CloudEvents waiting in the principal send or receive queue for a queue pair",
		q,
	)
	require.NoError(t, reg.Register(c))

	mfs, err := reg.Gather()
	require.NoError(t, err)
	require.Len(t, mfs, 1)
	mf := mfs[0]
	require.Equal(t, "principal_event_queue_depth", mf.GetName())
	require.Equal(t, dto.MetricType_GAUGE, mf.GetType())

	var sendVal, recvVal float64
	var seenSend, seenRecv bool
	for _, m := range mf.GetMetric() {
		dir := labelValue(m.GetLabel(), queueDepthLabelDirection)
		switch dir {
		case "send":
			seenSend = true
			require.Equal(t, "agent-a", labelValue(m.GetLabel(), queueDepthLabelQueue))
			sendVal = m.GetGauge().GetValue()
		case "recv":
			seenRecv = true
			require.Equal(t, "agent-a", labelValue(m.GetLabel(), queueDepthLabelQueue))
			recvVal = m.GetGauge().GetValue()
		}
	}
	require.True(t, seenSend)
	require.True(t, seenRecv)
	require.Equal(t, 1.0, sendVal)
	require.Equal(t, 2.0, recvVal)
}

func TestEventQueueDepthCollector_EmptyRegistry(t *testing.T) {
	q := queue.NewSendRecvQueues()
	reg := prometheus.NewRegistry()
	c := newEventQueueDepthCollector("agent_event_queue_depth", "help", q)
	require.NoError(t, reg.Register(c))

	mfs, err := reg.Gather()
	require.NoError(t, err)
	require.Empty(t, mfs, "no queue pairs means no metric series")
}

func TestEventQueueDepthCollector_Describe(t *testing.T) {
	q := queue.NewSendRecvQueues()
	c := newEventQueueDepthCollector("principal_event_queue_depth", "help text", q)
	ch := make(chan *prometheus.Desc, 1)
	c.Describe(ch)
	close(ch)
	desc := <-ch
	require.NotNil(t, desc)
	s := desc.String()
	require.Contains(t, s, "principal_event_queue_depth")
	require.Contains(t, s, "help text")
}
