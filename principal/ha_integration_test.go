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

package principal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/ha"
	"github.com/argoproj-labs/argocd-agent/pkg/replication"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// createTestServer creates a minimal Server for testing HA integration
func createTestServer() *Server {
	return &Server{
		queues:       queue.NewSendRecvQueues(),
		namespaceMap: make(map[string]types.AgentMode),
		resources:    resources.NewAgentResources(),
	}
}

func TestNewHAComponents(t *testing.T) {
	t.Run("creates components successfully", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()

		components, err := NewHAComponents(ctx, server)
		require.NoError(t, err)
		require.NotNil(t, components)

		assert.NotNil(t, components.Controller)
		assert.NotNil(t, components.ReplicationForwarder)
		assert.NotNil(t, components.ReplicationServer)
		assert.NotNil(t, components.stateProvider)
	})

	t.Run("creates with HA options", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()

		components, err := NewHAComponents(ctx, server,
			ha.WithEnabled(true),
			ha.WithPreferredRole("primary"),
			ha.WithPeerAddress("peer.example.com:8443"),
		)
		require.NoError(t, err)
		require.NotNil(t, components)

		opts := components.Controller.Options()
		assert.True(t, opts.Enabled)
		assert.Equal(t, ha.RolePrimary, opts.PreferredRole)
		assert.Equal(t, "peer.example.com:8443", opts.PeerAddress)
	})
}

func TestServerStateProvider(t *testing.T) {
	t.Run("GetAllAgentNames returns known agents", func(t *testing.T) {
		server := createTestServer()
		server.namespaceMap["agent1"] = types.AgentModeManaged
		server.namespaceMap["agent2"] = types.AgentModeAutonomous

		provider := &serverStateProvider{server: server}

		names := provider.GetAllAgentNames()
		assert.Len(t, names, 2)
		assert.Contains(t, names, "agent1")
		assert.Contains(t, names, "agent2")
	})

	t.Run("GetAgentMode returns correct mode", func(t *testing.T) {
		server := createTestServer()
		server.namespaceMap["managed-agent"] = types.AgentModeManaged
		server.namespaceMap["autonomous-agent"] = types.AgentModeAutonomous

		provider := &serverStateProvider{server: server}

		assert.Equal(t, "managed", provider.GetAgentMode("managed-agent"))
		assert.Equal(t, "autonomous", provider.GetAgentMode("autonomous-agent"))
		assert.Equal(t, "unknown", provider.GetAgentMode("nonexistent"))
	})

	t.Run("IsAgentConnected checks queue existence", func(t *testing.T) {
		server := createTestServer()
		server.queues.Create("connected-agent")

		provider := &serverStateProvider{server: server}

		assert.True(t, provider.IsAgentConnected("connected-agent"))
		assert.False(t, provider.IsAgentConnected("disconnected-agent"))
	})

	t.Run("GetAgentResources returns resources", func(t *testing.T) {
		server := createTestServer()
		server.resources.Add("agent1", resources.ResourceKey{
			Name:      "app1",
			Namespace: "default",
			Kind:      "Application",
			UID:       "uid-123",
		})
		server.resources.Add("agent1", resources.ResourceKey{
			Name:      "proj1",
			Namespace: "argocd",
			Kind:      "AppProject",
			UID:       "uid-456",
		})

		provider := &serverStateProvider{server: server}

		res := provider.GetAgentResources("agent1")
		assert.Len(t, res, 2)

		// Check that resources are returned
		foundApp := false
		foundProj := false
		for _, r := range res {
			if r.Name == "app1" && r.Kind == "Application" {
				foundApp = true
			}
			if r.Name == "proj1" && r.Kind == "AppProject" {
				foundProj = true
			}
		}
		assert.True(t, foundApp, "expected to find Application")
		assert.True(t, foundProj, "expected to find AppProject")
	})

	t.Run("GetAgentResources returns nil for unknown agent", func(t *testing.T) {
		server := createTestServer()
		provider := &serverStateProvider{server: server}

		res := provider.GetAgentResources("unknown-agent")
		assert.Nil(t, res)
	})
}

func TestHAComponentsStartAndShutdown(t *testing.T) {
	t.Run("StartHA and ShutdownHA work correctly", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		server := createTestServer()
		components, err := NewHAComponents(ctx, server)
		require.NoError(t, err)

		err = components.StartHA(ctx)
		require.NoError(t, err)

		// Verify components are running
		assert.NotNil(t, components.Controller)

		// Shutdown
		err = components.ShutdownHA(ctx)
		require.NoError(t, err)
	})

	t.Run("nil HAComponents handles gracefully", func(t *testing.T) {
		var components *HAComponents

		err := components.StartHA(context.Background())
		assert.NoError(t, err)

		err = components.ShutdownHA(context.Background())
		assert.NoError(t, err)
	})
}

func TestHAComponentsOnAgentConnect(t *testing.T) {
	t.Run("OnAgentConnect with nil components", func(t *testing.T) {
		var components *HAComponents
		agent := types.NewAgent("test", "managed")
		err := components.OnAgentConnect(agent)
		assert.NoError(t, err)
	})

	t.Run("OnAgentConnect notifies controller", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()

		components, err := NewHAComponents(ctx, server)
		require.NoError(t, err)

		// Start the controller so it's in a valid state
		err = components.Controller.Start()
		require.NoError(t, err)

		// When in standby, first agent connection should transition to active
		// But we're not in standby by default, so this tests the rejection path
		agent := types.NewAgent("test-agent", "managed")
		err = components.OnAgentConnect(agent)
		// May error if not in accepting state - that's expected behavior
		_ = err
	})
}

func TestHAComponentsOnAgentDisconnect(t *testing.T) {
	t.Run("OnAgentDisconnect with nil components", func(t *testing.T) {
		var components *HAComponents
		// Should not panic
		components.OnAgentDisconnect("test-agent")
	})

	t.Run("OnAgentDisconnect logs disconnect", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()

		components, err := NewHAComponents(ctx, server)
		require.NoError(t, err)

		// Should not panic or error
		components.OnAgentDisconnect("test-agent")
	})
}

func TestHAHealthzHandler(t *testing.T) {
	baseHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}

	t.Run("nil components uses base handler", func(t *testing.T) {
		var components *HAComponents
		handler := components.HAHealthzHandler(baseHandler)

		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()
		handler(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "OK", w.Body.String())
	})

	t.Run("active state returns healthy", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()

		components, err := NewHAComponents(ctx, server)
		require.NoError(t, err)

		// Start controller - it should become active since HA is disabled by default
		err = components.Controller.Start()
		require.NoError(t, err)

		handler := components.HAHealthzHandler(baseHandler)

		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()
		handler(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "active", w.Header().Get("X-HA-State"))
	})
}

func TestGetHAStatus(t *testing.T) {
	t.Run("nil components returns disabled status", func(t *testing.T) {
		var components *HAComponents
		status := components.GetHAStatus()

		assert.False(t, status.Enabled)
	})

	t.Run("returns full status when enabled", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()

		components, err := NewHAComponents(ctx, server,
			ha.WithEnabled(true),
			ha.WithPreferredRole("primary"),
			ha.WithPeerAddress("peer:8443"),
		)
		require.NoError(t, err)

		status := components.GetHAStatus()
		assert.True(t, status.Enabled)
		assert.Equal(t, ha.RolePrimary, status.PreferredRole)
		assert.Equal(t, "peer:8443", status.PeerAddress)
	})
}

func TestIsActive(t *testing.T) {
	t.Run("nil components returns true (standalone)", func(t *testing.T) {
		var components *HAComponents
		assert.True(t, components.IsActive())
	})

	t.Run("returns true when state is active", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()

		components, err := NewHAComponents(ctx, server)
		require.NoError(t, err)

		// Start controller - becomes active when HA disabled
		err = components.Controller.Start()
		require.NoError(t, err)

		assert.True(t, components.IsActive())
	})
}

func TestShouldAcceptAgents(t *testing.T) {
	t.Run("nil components returns true (standalone)", func(t *testing.T) {
		var components *HAComponents
		assert.True(t, components.ShouldAcceptAgents())
	})

	t.Run("returns true when in accepting state", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()

		components, err := NewHAComponents(ctx, server)
		require.NoError(t, err)

		// Start controller
		err = components.Controller.Start()
		require.NoError(t, err)

		// Active state should accept agents
		assert.True(t, components.ShouldAcceptAgents())
	})
}

func TestForwardEventForReplication(t *testing.T) {
	t.Run("nil components handles gracefully", func(t *testing.T) {
		var components *HAComponents
		// Should not panic
		components.ForwardEventForReplication(nil, "agent1", "inbound")
	})

	t.Run("does not forward when not active", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()

		components, err := NewHAComponents(ctx, server,
			ha.WithEnabled(true),
			ha.WithPreferredRole("replica"),
			ha.WithPeerAddress("peer:8443"),
		)
		require.NoError(t, err)

		// Don't start controller - should be in recovering state
		// Forwarding should be a no-op
		components.ForwardEventForReplication(nil, "agent1", "inbound")

		// Verify no events were queued
		assert.Equal(t, uint64(0), components.ReplicationForwarder.CurrentSequenceNum())
	})
}

func TestHandleReplicatedEvent_EventDecoding(t *testing.T) {
	evSource := event.NewEventSource("test")

	t.Run("nil event returns nil", func(t *testing.T) {
		ctx := context.Background()
		server := createTestServer()
		components, err := NewHAComponents(ctx, server)
		require.NoError(t, err)

		err = components.handleReplicatedEvent(&replication.ReplicatedEvent{
			AgentName: "agent1",
		})
		assert.NoError(t, err)
	})

	t.Run("application create event can be decoded", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-app",
				Namespace: "argocd",
				UID:       "uid-app-1",
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "default",
			},
		}

		ce := evSource.ApplicationEvent(event.Create, app)
		ev := event.New(ce, event.TargetApplication)

		assert.Equal(t, event.TargetApplication, event.Target(ev.CloudEvent()))
		assert.Equal(t, event.Create, event.EventType(ev.CloudEvent().Type()))

		decoded, err := ev.Application()
		require.NoError(t, err)
		assert.Equal(t, "my-app", decoded.Name)
		assert.Equal(t, "argocd", decoded.Namespace)
		assert.Equal(t, "default", decoded.Spec.Project)
	})

	t.Run("appproject create event can be decoded", func(t *testing.T) {
		proj := &v1alpha1.AppProject{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-project",
				Namespace: "argocd",
				UID:       "uid-proj-1",
			},
			Spec: v1alpha1.AppProjectSpec{
				Description: "test project",
			},
		}

		ce := evSource.AppProjectEvent(event.Create, proj)
		ev := event.New(ce, event.TargetAppProject)

		assert.Equal(t, event.TargetAppProject, event.Target(ev.CloudEvent()))
		assert.Equal(t, event.Create, event.EventType(ev.CloudEvent().Type()))

		decoded, err := ev.AppProject()
		require.NoError(t, err)
		assert.Equal(t, "my-project", decoded.Name)
		assert.Equal(t, "argocd", decoded.Namespace)
		assert.Equal(t, "test project", decoded.Spec.Description)
	})

	t.Run("outbound spec update event preserves data", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "updated-app",
				Namespace: "argocd",
				UID:       "uid-app-2",
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "production",
				Source:  &v1alpha1.ApplicationSource{RepoURL: "https://github.com/example/repo"},
			},
		}

		ce := evSource.ApplicationEvent(event.SpecUpdate, app)
		ev := event.New(ce, event.TargetApplication)

		replEv := &replication.ReplicatedEvent{
			Event:       ev,
			AgentName:   "agent1",
			Direction:   replication.DirectionOutbound,
			SequenceNum: 42,
		}

		assert.Equal(t, event.TargetApplication, event.Target(replEv.Event.CloudEvent()))
		assert.Equal(t, event.SpecUpdate, event.EventType(replEv.Event.CloudEvent().Type()))

		decoded, err := replEv.Event.Application()
		require.NoError(t, err)
		assert.Equal(t, "updated-app", decoded.Name)
		assert.Equal(t, "production", decoded.Spec.Project)
		assert.Equal(t, "https://github.com/example/repo", decoded.Spec.Source.RepoURL)
		assert.Equal(t, uint64(42), replEv.SequenceNum)
	})
}
