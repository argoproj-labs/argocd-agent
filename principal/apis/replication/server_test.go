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

package replication

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/replicationapi"
	"github.com/argoproj-labs/argocd-agent/pkg/replication"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mockStateProvider implements AgentStateProvider for testing
type mockStateProvider struct {
	agents map[string]*mockAgentInfo
}

type mockAgentInfo struct {
	mode      string
	connected bool
	resources []ResourceInfo
}

func newMockStateProvider() *mockStateProvider {
	return &mockStateProvider{
		agents: make(map[string]*mockAgentInfo),
	}
}

func (m *mockStateProvider) AddAgent(name, mode string, connected bool, resources []ResourceInfo) {
	m.agents[name] = &mockAgentInfo{
		mode:      mode,
		connected: connected,
		resources: resources,
	}
}

func (m *mockStateProvider) GetAllAgentNames() []string {
	names := make([]string, 0, len(m.agents))
	for name := range m.agents {
		names = append(names, name)
	}
	return names
}

func (m *mockStateProvider) GetAgentMode(agentName string) string {
	if agent, ok := m.agents[agentName]; ok {
		return agent.mode
	}
	return ""
}

func (m *mockStateProvider) IsAgentConnected(agentName string) bool {
	if agent, ok := m.agents[agentName]; ok {
		return agent.connected
	}
	return false
}

func (m *mockStateProvider) GetAgentResources(agentName string) []ResourceInfo {
	if agent, ok := m.agents[agentName]; ok {
		return agent.resources
	}
	return nil
}

// mockReplicaStream implements replicationapi.Replication_SubscribeServer for testing
type mockGrpcStream struct {
	ctx         context.Context
	cancelFn    context.CancelFunc
	sentEvents  []*replicationapi.ReplicatedEvent
	recvAcks    chan *replicationapi.ReplicationAck
	recvErr     error
	sendErr     error
	mu          sync.Mutex
}

func newMockGrpcStream() *mockGrpcStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &mockGrpcStream{
		ctx:        ctx,
		cancelFn:   cancel,
		sentEvents: make([]*replicationapi.ReplicatedEvent, 0),
		recvAcks:   make(chan *replicationapi.ReplicationAck, 10),
	}
}

func (m *mockGrpcStream) Send(ev *replicationapi.ReplicatedEvent) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentEvents = append(m.sentEvents, ev)
	return nil
}

func (m *mockGrpcStream) Recv() (*replicationapi.ReplicationAck, error) {
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	select {
	case <-m.ctx.Done():
		return nil, io.EOF
	case ack := <-m.recvAcks:
		return ack, nil
	}
}

func (m *mockGrpcStream) Context() context.Context {
	return m.ctx
}

// Implement the rest of grpc.ServerStream interface for testing
func (m *mockGrpcStream) SetHeader(_ metadata.MD) error  { return nil }
func (m *mockGrpcStream) SendHeader(_ metadata.MD) error { return nil }
func (m *mockGrpcStream) SetTrailer(_ metadata.MD)       {}
func (m *mockGrpcStream) SendMsg(_ interface{}) error    { return nil }
func (m *mockGrpcStream) RecvMsg(_ interface{}) error    { return nil }

func (m *mockGrpcStream) SentEvents() []*replicationapi.ReplicatedEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*replicationapi.ReplicatedEvent{}, m.sentEvents...)
}

func TestNewServer(t *testing.T) {
	forwarder := replication.NewForwarder()
	stateProvider := newMockStateProvider()

	server := NewServer(forwarder, stateProvider)
	require.NotNil(t, server)
	assert.Equal(t, 10, server.options.MaxReplicaConnections)

	server2 := NewServer(forwarder, stateProvider, WithMaxReplicaConnections(5))
	assert.Equal(t, 5, server2.options.MaxReplicaConnections)
}

func TestServerGetSnapshot(t *testing.T) {
	forwarder := replication.NewForwarder()
	stateProvider := newMockStateProvider()

	// Add some agents
	stateProvider.AddAgent("agent1", "managed", true, []ResourceInfo{
		{Name: "app1", Namespace: "default", Kind: "Application", UID: "uid1"},
	})
	stateProvider.AddAgent("agent2", "autonomous", false, []ResourceInfo{
		{Name: "app2", Namespace: "prod", Kind: "Application", UID: "uid2"},
		{Name: "proj1", Namespace: "prod", Kind: "AppProject", UID: "uid3"},
	})

	server := NewServer(forwarder, stateProvider)

	ctx := context.Background()
	snapshot, err := server.GetSnapshot(ctx, &replicationapi.SnapshotRequest{})
	require.NoError(t, err)
	require.NotNil(t, snapshot)

	assert.Len(t, snapshot.Agents, 2)

	// Find agents by name
	agentMap := make(map[string]*replicationapi.AgentState)
	for _, agent := range snapshot.Agents {
		agentMap[agent.Name] = agent
	}

	agent1 := agentMap["agent1"]
	require.NotNil(t, agent1)
	assert.Equal(t, "managed", agent1.Mode)
	assert.True(t, agent1.Connected)
	assert.Len(t, agent1.Resources, 1)

	agent2 := agentMap["agent2"]
	require.NotNil(t, agent2)
	assert.Equal(t, "autonomous", agent2.Mode)
	assert.False(t, agent2.Connected)
	assert.Len(t, agent2.Resources, 2)
}

func TestServerGetSnapshot_NoStateProvider(t *testing.T) {
	forwarder := replication.NewForwarder()
	server := NewServer(forwarder, nil)

	ctx := context.Background()
	_, err := server.GetSnapshot(ctx, &replicationapi.SnapshotRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state provider not configured")
}

func TestServerStatus(t *testing.T) {
	forwarder := replication.NewForwarder()
	stateProvider := newMockStateProvider()
	server := NewServer(forwarder, stateProvider)

	// Queue some events
	forwarder.Forward(nil, "agent1", replication.DirectionInbound)
	forwarder.Forward(nil, "agent2", replication.DirectionOutbound)

	ctx := context.Background()
	status, err := server.Status(ctx, &replicationapi.StatusRequest{})
	require.NoError(t, err)
	require.NotNil(t, status)

	assert.Equal(t, uint64(2), status.CurrentSequenceNum)
	assert.Equal(t, int32(2), status.PendingEvents)
	assert.Equal(t, int32(0), status.ConnectedReplicas)
}

func TestServerSubscribe(t *testing.T) {
	t.Run("accepts replica connection", func(t *testing.T) {
		forwarder := replication.NewForwarder()
		stateProvider := newMockStateProvider()
		server := NewServer(forwarder, stateProvider)

		// Start forwarder
		ctx, cancel := context.WithCancel(context.Background())
		go forwarder.Run(ctx)
		defer cancel()

		stream := newMockGrpcStream()

		// Run subscribe in background
		done := make(chan error)
		go func() {
			done <- server.Subscribe(stream)
		}()

		// Send initial ACK to unblock the server's flush handshake
		stream.recvAcks <- &replicationapi.ReplicationAck{AckedSequenceNum: 0}

		// Wait for registration + flush
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, 1, server.ConnectedReplicaCount())
		assert.Equal(t, 1, forwarder.ReplicaCount())

		// Disconnect
		stream.cancelFn()

		select {
		case err := <-done:
			assert.Equal(t, context.Canceled, err)
		case <-time.After(time.Second):
			t.Fatal("Subscribe did not return")
		}

		// Should be cleaned up
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, 0, server.ConnectedReplicaCount())
	})

	t.Run("rejects when max connections reached", func(t *testing.T) {
		forwarder := replication.NewForwarder()
		stateProvider := newMockStateProvider()
		server := NewServer(forwarder, stateProvider, WithMaxReplicaConnections(1))

		ctx, cancel := context.WithCancel(context.Background())
		go forwarder.Run(ctx)
		defer cancel()

		// First connection should succeed
		stream1 := newMockGrpcStream()
		go server.Subscribe(stream1)
		stream1.recvAcks <- &replicationapi.ReplicationAck{AckedSequenceNum: 0}
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, 1, server.ConnectedReplicaCount())

		// Second connection should be rejected
		stream2 := newMockGrpcStream()
		err := server.Subscribe(stream2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max replica connections")

		// Cleanup
		stream1.cancelFn()
	})
}

func TestServerForwardEvent(t *testing.T) {
	forwarder := replication.NewForwarder()
	stateProvider := newMockStateProvider()
	server := NewServer(forwarder, stateProvider)

	server.ForwardEvent(nil, "agent1", replication.DirectionInbound)
	server.ForwardEvent(nil, "agent2", replication.DirectionOutbound)

	assert.Equal(t, uint64(2), forwarder.CurrentSequenceNum())
}

func TestServerReplicaManagement(t *testing.T) {
	forwarder := replication.NewForwarder()
	stateProvider := newMockStateProvider()
	server := NewServer(forwarder, stateProvider)

	ctx, cancel := context.WithCancel(context.Background())
	go forwarder.Run(ctx)
	defer cancel()

	// Connect replicas
	stream1 := newMockGrpcStream()
	stream2 := newMockGrpcStream()

	go server.Subscribe(stream1)
	go server.Subscribe(stream2)

	// Send initial ACKs to unblock flush handshake
	stream1.recvAcks <- &replicationapi.ReplicationAck{AckedSequenceNum: 0}
	stream2.recvAcks <- &replicationapi.ReplicationAck{AckedSequenceNum: 0}

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, server.ConnectedReplicaCount())

	ids := server.GetReplicaIDs()
	assert.Len(t, ids, 2)

	// Disconnect one by cancelling its stream context (simulates client disconnect)
	stream1.cancelFn()

	// Wait for cleanup with polling
	assert.Eventually(t, func() bool {
		return server.ConnectedReplicaCount() == 1
	}, time.Second, 10*time.Millisecond, "expected 1 replica after disconnect")

	// Cleanup remaining
	stream2.cancelFn()
}

func TestServerForwardEvent_RoundTrip(t *testing.T) {
	forwarder := replication.NewForwarder()
	stateProvider := newMockStateProvider()
	server := NewServer(forwarder, stateProvider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go forwarder.Run(ctx)

	stream := newMockGrpcStream()
	go server.Subscribe(stream)
	// Send initial ACK to unblock flush handshake
	stream.recvAcks <- &replicationapi.ReplicationAck{AckedSequenceNum: 0}
	time.Sleep(50 * time.Millisecond)

	app := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-app",
			Namespace:       "argocd",
			UID:             "test-uid-123",
			ResourceVersion: "1",
		},
	}
	cloudEvent := event.NewEventSource("test").ApplicationEvent(event.Create, app)
	ev := event.New(cloudEvent, event.TargetApplication)
	server.ForwardEvent(ev, "agent1", replication.DirectionOutbound)

	time.Sleep(100 * time.Millisecond)

	sent := stream.SentEvents()
	require.Len(t, sent, 1)

	sentProto := sent[0]
	assert.Equal(t, "agent1", sentProto.AgentName)
	assert.Equal(t, "outbound", sentProto.Direction)

	decoded, err := format.FromProto(sentProto.Event)
	require.NoError(t, err)
	assert.Equal(t, "application", decoded.DataSchema())
	assert.Equal(t, event.Create.String(), decoded.Type())

	var decodedApp v1alpha1.Application
	require.NoError(t, json.Unmarshal(decoded.Data(), &decodedApp))
	assert.Equal(t, "my-app", decodedApp.Name)
	assert.Equal(t, "argocd", decodedApp.Namespace)

	stream.cancelFn()
}

func TestBufferingAdapter(t *testing.T) {
	t.Run("buffers events then flushes", func(t *testing.T) {
		stream := newMockGrpcStream()
		inner := &streamAdapter{stream: stream, replicaID: "test"}
		adapter := newBufferingAdapter(inner)

		ev1 := &replication.ReplicatedEvent{AgentName: "a1", SequenceNum: 1}
		ev2 := &replication.ReplicatedEvent{AgentName: "a2", SequenceNum: 2}

		require.NoError(t, adapter.Send(ev1))
		require.NoError(t, adapter.Send(ev2))

		// Nothing sent to stream yet
		assert.Empty(t, stream.SentEvents())

		// Flush
		require.NoError(t, adapter.Flush())
		assert.Len(t, stream.SentEvents(), 2)

		// After flush, sends go through directly
		ev3 := &replication.ReplicatedEvent{AgentName: "a3", SequenceNum: 3}
		require.NoError(t, adapter.Send(ev3))
		assert.Len(t, stream.SentEvents(), 3)
	})

	t.Run("events arrive before and after flush in Subscribe", func(t *testing.T) {
		forwarder := replication.NewForwarder()
		stateProvider := newMockStateProvider()
		server := NewServer(forwarder, stateProvider)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go forwarder.Run(ctx)

		stream := newMockGrpcStream()
		done := make(chan error)
		go func() {
			done <- server.Subscribe(stream)
		}()

		// Give Subscribe time to register the buffering adapter
		time.Sleep(50 * time.Millisecond)

		// Forward an event while the replica hasn't ACK'd (should be buffered)
		server.ForwardEvent(nil, "agent-pre", replication.DirectionInbound)
		time.Sleep(50 * time.Millisecond)
		assert.Empty(t, stream.SentEvents())

		// Replica sends initial ACK — triggers flush
		stream.recvAcks <- &replicationapi.ReplicationAck{AckedSequenceNum: 0}
		time.Sleep(50 * time.Millisecond)

		// Buffered event should now be delivered
		assert.Len(t, stream.SentEvents(), 1)

		// Events after flush go through directly
		server.ForwardEvent(nil, "agent-post", replication.DirectionInbound)
		time.Sleep(50 * time.Millisecond)
		assert.Len(t, stream.SentEvents(), 2)

		stream.cancelFn()
		<-done
	})
}

func TestServerShutdown(t *testing.T) {
	forwarder := replication.NewForwarder()
	stateProvider := newMockStateProvider()
	server := NewServer(forwarder, stateProvider)

	ctx, cancel := context.WithCancel(context.Background())
	go forwarder.Run(ctx)
	defer cancel()

	// Connect replicas
	stream1 := newMockGrpcStream()
	stream2 := newMockGrpcStream()

	go server.Subscribe(stream1)
	go server.Subscribe(stream2)

	// Send initial ACKs to unblock flush handshake
	stream1.recvAcks <- &replicationapi.ReplicationAck{AckedSequenceNum: 0}
	stream2.recvAcks <- &replicationapi.ReplicationAck{AckedSequenceNum: 0}

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, server.ConnectedReplicaCount())

	// Cancel stream contexts to simulate client disconnects during shutdown
	// In real gRPC, server.Shutdown would propagate through to close streams
	stream1.cancelFn()
	stream2.cancelFn()

	// Shutdown should complete quickly when streams are already closing
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	err := server.Shutdown(shutdownCtx)
	require.NoError(t, err)

	// Verify cleanup
	assert.Eventually(t, func() bool {
		return server.ConnectedReplicaCount() == 0
	}, time.Second, 10*time.Millisecond, "expected all replicas disconnected after shutdown")
}
