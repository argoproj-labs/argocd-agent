// Copyright 2025 The argocd-agent Authors
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

package agent

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func TestResyncOnStart(t *testing.T) {
	a, _ := newAgent(t)
	a.emitter = event.NewEventSource("test")
	a.kubeClient.RestConfig = &rest.Config{}
	logCtx := log()

	t.Run("should return if the agent has already been synced", func(t *testing.T) {
		a.resyncedOnStart = true
		err := a.resyncOnStart(logCtx)
		assert.Nil(t, err)

		sendQ := a.queues.SendQ(defaultQueueName)
		assert.Zero(t, sendQ.Len())
	})

	t.Run("send resource resync request in autonomous mode", func(t *testing.T) {
		a.resyncedOnStart = false
		a.mode = types.AgentModeAutonomous
		err := a.resyncOnStart(logCtx)
		assert.Nil(t, err)

		sendQ := a.queues.SendQ(defaultQueueName)
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)

		assert.Equal(t, event.EventRequestResourceResync.String(), ev.Type())
		assert.True(t, a.resyncedOnStart)
	})

	t.Run("send synced resource list request in managed mode", func(t *testing.T) {
		a.resyncedOnStart = false
		a.mode = types.AgentModeManaged
		err := a.resyncOnStart(logCtx)
		assert.Nil(t, err)

		sendQ := a.queues.SendQ(defaultQueueName)
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)

		assert.Equal(t, event.SyncedResourceList.String(), ev.Type())
		assert.True(t, a.resyncedOnStart)
	})
}

func TestAgentHopMetricsObservations(t *testing.T) {
	agentMetrics := metrics.NewAgentMetrics()

	t.Run("sender observes send queue dwell", func(t *testing.T) {
		a, _ := newAgent(t)
		a.metrics = agentMetrics
		a.emitter = event.NewEventSource("test")
		a.eventWriter = event.NewEventWriter(&fakeSubscribeClient{ctx: context.Background()})

		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "app",
				Namespace:       "argocd",
				ResourceVersion: "1",
				UID:             "uid-1",
			},
		}

		before := histogramSampleCount(t, a.metrics.SendQueueDwell, event.TargetApplication.String())

		ev := a.emitter.ApplicationEvent(event.Create, app)
		sendQ := a.queues.SendQ(defaultQueueName)
		sendQ.Add(ev)

		time.Sleep(5 * time.Millisecond)

		err := a.sender(&fakeSubscribeClient{ctx: context.Background()})
		require.NoError(t, err)

		after := histogramSampleCount(t, a.metrics.SendQueueDwell, event.TargetApplication.String())
		assert.Equal(t, before+1, after)
	})

	t.Run("receiver observes ack roundtrip by original resource type", func(t *testing.T) {
		a, _ := newAgent(t)
		a.metrics = agentMetrics
		a.emitter = event.NewEventSource("test")

		stream := &fakeSubscribeClient{ctx: context.Background()}
		a.eventWriter = event.NewEventWriter(stream)
		a.eventWriter.SetMetrics(a.metrics)

		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "app",
				Namespace:       "argocd",
				ResourceVersion: "1",
				UID:             "uid-1",
			},
		}

		beforeApp := histogramSampleCount(t, a.metrics.AckRoundtrip, event.TargetApplication.String())
		beforeAck := histogramSampleCount(t, a.metrics.AckRoundtrip, event.TargetEventAck.String())

		original := a.emitter.ApplicationEvent(event.Create, app)
		a.eventWriter.Add(original)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go a.eventWriter.SendWaitingEvents(ctx)
		require.Eventually(t, func() bool {
			return a.eventWriter.SentResourceType(event.ResourceID(original)) == event.TargetApplication.String()
		}, time.Second, 10*time.Millisecond)
		cancel()

		ack := a.emitter.ProcessedEvent(event.EventProcessed, event.New(original, event.TargetApplication))
		wireAck, err := format.ToProto(ack)
		require.NoError(t, err)

		err = a.receiver(&fakeSubscribeClient{
			ctx:       context.Background(),
			recvEvent: &eventstreamapi.Event{Event: wireAck},
		})
		require.NoError(t, err)

		afterApp := histogramSampleCount(t, a.metrics.AckRoundtrip, event.TargetApplication.String())
		afterAck := histogramSampleCount(t, a.metrics.AckRoundtrip, event.TargetEventAck.String())
		assert.Equal(t, beforeApp+1, afterApp)
		assert.Equal(t, beforeAck, afterAck)
	})
}

func histogramSampleCount(t *testing.T, vec *prometheus.HistogramVec, label string) uint64 {
	t.Helper()

	observer, err := vec.GetMetricWithLabelValues(label)
	require.NoError(t, err)

	histogram, ok := observer.(prometheus.Histogram)
	require.True(t, ok)

	metric := &dto.Metric{}
	require.NoError(t, histogram.Write(metric))

	return metric.GetHistogram().GetSampleCount()
}

type fakeSubscribeClient struct {
	ctx       context.Context
	recvEvent *eventstreamapi.Event
	recvErr   error
	recvUsed  bool
}

func (f *fakeSubscribeClient) Send(*eventstreamapi.Event) error {
	return nil
}

func (f *fakeSubscribeClient) Recv() (*eventstreamapi.Event, error) {
	if f.recvUsed {
		if f.recvErr != nil {
			return nil, f.recvErr
		}
		return nil, io.EOF
	}
	f.recvUsed = true
	if f.recvEvent != nil {
		return f.recvEvent, nil
	}
	if f.recvErr != nil {
		return nil, f.recvErr
	}
	return nil, io.EOF
}

func (f *fakeSubscribeClient) Header() (metadata.MD, error) {
	return metadata.MD{}, nil
}

func (f *fakeSubscribeClient) Trailer() metadata.MD {
	return metadata.MD{}
}

func (f *fakeSubscribeClient) CloseSend() error {
	return nil
}

func (f *fakeSubscribeClient) Context() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}

func (f *fakeSubscribeClient) SendMsg(any) error {
	return nil
}

func (f *fakeSubscribeClient) RecvMsg(any) error {
	return nil
}
