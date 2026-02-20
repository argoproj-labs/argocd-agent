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

package eventstream

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/principal/apis/eventstream/mock"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Subscribe(t *testing.T) {
	metric := metrics.NewPrincipalMetrics()
	cluster := &cluster.Manager{}

	t.Run("Test send to subcription stream", func(t *testing.T) {
		qs := queue.NewSendRecvQueues()
		qs.Create("default")
		s := NewServer(qs, event.NewEventWritersMap(), metric, cluster)
		st := &mock.MockEventServer{
			AgentName: "default",
			AgentMode: string(types.AgentModeManaged),
		}

		logCtx := s.log()
		st.AddRecvHook(func(s *mock.MockEventServer) error {
			logCtx.WithField("component", "RecvHook").Tracef("Entry")
			ticker := time.NewTicker(500 * time.Millisecond)
			<-ticker.C
			ticker.Stop()
			logCtx.WithField("component", "RecvHook").Tracef("Exit")
			return io.EOF
		})
		emitter := event.NewEventSource("test")
		// qs.SendQ("default").Add(event.LegacyEvent{Type: event.EventAppAdded, Application: &v1alpha1.Application{
		// 	ObjectMeta: v1.ObjectMeta{Name: "foo", Namespace: "test"}},
		// })
		qs.SendQ("default").Add(emitter.ApplicationEvent(
			event.Create,
			&v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "foo", Namespace: "test"}},
		))
		qs.SendQ("default").Add(emitter.ApplicationEvent(
			event.Create,
			&v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "bar", Namespace: "test"}},
		))
		err := s.Subscribe(st)
		assert.Nil(t, err)
		assert.Equal(t, 0, int(st.NumRecv.Load()))
		assert.Equal(t, 2, int(st.NumSent.Load()))
	})
	t.Run("Test recv from subscription stream", func(t *testing.T) {
		qs := queue.NewSendRecvQueues()
		qs.Create("default")
		s := NewServer(qs, event.NewEventWritersMap(), metric, cluster)
		st := &mock.MockEventServer{AgentName: "default", Application: v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foo",
				Namespace: "default",
			},
		}}
		numReceived := 0
		st.AddRecvHook(func(s *mock.MockEventServer) error {
			if numReceived >= 2 {
				return io.EOF
			}
			numReceived += 1
			return nil
		})
		err := s.Subscribe(st)
		require.NoError(t, err)
		require.True(t, qs.HasQueuePair("default"))
		assert.Equal(t, 2, int(st.NumRecv.Load()))
		assert.Equal(t, 0, int(st.NumSent.Load()))
	})

	t.Run("Test connection closed by peer", func(t *testing.T) {
		qs := queue.NewSendRecvQueues()
		qs.Create("default")
		s := NewServer(qs, event.NewEventWritersMap(), metric, cluster)
		st := &mock.MockEventServer{AgentName: "default"}
		st.AddRecvHook(func(s *mock.MockEventServer) error {
			return fmt.Errorf("some error")
		})
		err := s.Subscribe(st)
		assert.Nil(t, err)
		assert.Equal(t, 0, int(st.NumRecv.Load()))
		assert.Equal(t, 0, int(st.NumSent.Load()))
	})

	t.Run("Test no agent information in context", func(t *testing.T) {
		qs := queue.NewSendRecvQueues()
		qs.Create("default")
		s := NewServer(qs, event.NewEventWritersMap(), metric, cluster)
		st := &mock.MockEventServer{AgentName: ""}
		err := s.Subscribe(st)
		assert.Error(t, err)
	})

	t.Run("Test events being discarded for unmanaged agent", func(t *testing.T) {
		qs := queue.NewSendRecvQueues()
		qs.Create("default")
		s := NewServer(qs, event.NewEventWritersMap(), metric, cluster)
		st := &mock.MockEventServer{
			AgentName: "default",
			AgentMode: string(types.AgentModeAutonomous),
		}

		logCtx := s.log()
		st.AddRecvHook(func(s *mock.MockEventServer) error {
			logCtx.WithField("component", "RecvHook").Tracef("Entry")
			ticker := time.NewTicker(500 * time.Millisecond)
			<-ticker.C
			ticker.Stop()
			logCtx.WithField("component", "RecvHook").Tracef("Exit")
			return io.EOF
		})
		emitter := event.NewEventSource("test")
		qs.SendQ("default").Add(emitter.ApplicationEvent(
			event.Create,
			&v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "foo", Namespace: "test"}},
		))
		qs.SendQ("default").Add(emitter.ApplicationEvent(
			event.Update,
			&v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "foo", Namespace: "test"}},
		))
		qs.SendQ("default").Add(emitter.ApplicationEvent(
			event.Delete,
			&v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "foo", Namespace: "test"}},
		))
		err := s.Subscribe(st)
		assert.Nil(t, err)
		assert.Equal(t, 0, int(st.NumRecv.Load()))
		assert.Equal(t, 1, int(st.NumSent.Load()))
	})
}

func TestDisconnectAll(t *testing.T) {
	clusterMgr := &cluster.Manager{}

	t.Run("disconnects active agents", func(t *testing.T) {
		qs := queue.NewSendRecvQueues()
		qs.Create("agent-a")
		qs.Create("agent-b")
		s := NewServer(qs, event.NewEventWritersMap(), nil, clusterMgr)

		done := make(chan string, 2)
		// gate blocks recv hooks until released — simulates long-lived streams
		gate := make(chan struct{})

		for _, name := range []string{"agent-a", "agent-b"} {
			name := name
			st := &mock.MockEventServer{AgentName: name}
			st.AddRecvHook(func(_ *mock.MockEventServer) error {
				<-gate
				return io.EOF
			})
			go func() {
				_ = s.Subscribe(st)
				done <- name
			}()
		}

		require.Eventually(t, func() bool {
			return s.ConnectedAgentCount() == 2
		}, time.Second, 10*time.Millisecond)

		s.DisconnectAll()
		// Unblock recv hooks so goroutines can exit
		close(gate)

		require.Eventually(t, func() bool {
			return s.ConnectedAgentCount() == 0
		}, time.Second, 10*time.Millisecond)

		<-done
		<-done
	})
}

func TestAcceptCheck(t *testing.T) {
	clusterMgr := &cluster.Manager{}

	t.Run("rejects when check returns error", func(t *testing.T) {
		qs := queue.NewSendRecvQueues()
		qs.Create("default")
		s := NewServer(qs, event.NewEventWritersMap(), nil, clusterMgr,
			WithAcceptCheck(func(_ string) error {
				return fmt.Errorf("not accepting connections")
			}),
		)
		st := &mock.MockEventServer{AgentName: "default"}
		err := s.Subscribe(st)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not accepting connections")
		assert.Equal(t, 0, s.ConnectedAgentCount())
	})

	t.Run("accepts when check returns nil", func(t *testing.T) {
		qs := queue.NewSendRecvQueues()
		qs.Create("default")
		s := NewServer(qs, event.NewEventWritersMap(), nil, clusterMgr,
			WithAcceptCheck(func(_ string) error {
				return nil
			}),
		)
		st := &mock.MockEventServer{AgentName: "default"}
		st.AddRecvHook(func(_ *mock.MockEventServer) error {
			return io.EOF
		})
		err := s.Subscribe(st)
		require.NoError(t, err)
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
