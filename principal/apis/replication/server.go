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
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/replicationapi"
	"github.com/argoproj-labs/argocd-agent/pkg/replication"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ModuleLogger("replication_server")
}

// AgentStateProvider provides agent state for snapshots
type AgentStateProvider interface {
	// GetAllAgentNames returns the names of all known agents
	GetAllAgentNames() []string
	// GetAgentMode returns the mode of an agent ("autonomous" or "managed")
	GetAgentMode(agentName string) string
	// IsAgentConnected returns whether an agent is currently connected
	IsAgentConnected(agentName string) bool
	// GetAgentResources returns the resources managed by an agent
	GetAgentResources(agentName string) []ResourceInfo
}

// ResourceInfo contains information about a resource
type ResourceInfo struct {
	Name         string
	Namespace    string
	Kind         string
	UID          string
	SpecChecksum []byte
	Data         []byte
}

// Server implements the gRPC replication service
type Server struct {
	replicationapi.UnimplementedReplicationServer

	mu sync.RWMutex

	// Forwarder handles event distribution to replicas
	forwarder *replication.Forwarder

	// State provider for snapshots
	stateProvider AgentStateProvider

	// Options
	options *ServerOptions

	// Connected replicas (for management)
	replicas map[string]*replicaClient

	// gRPC server for replication
	grpcServer *grpc.Server

	// Listener for the gRPC server
	listener net.Listener

	// Additional gRPC services to register alongside replication
	additionalRegistrations []func(grpc.ServiceRegistrar)
}

// ServerOptions configures the replication server
type ServerOptions struct {
	// MaxReplicaConnections limits the number of concurrent replica connections
	MaxReplicaConnections int
}

// ServerOption configures the server
type ServerOption func(*ServerOptions)

// WithMaxReplicaConnections sets the maximum number of replica connections
func WithMaxReplicaConnections(max int) ServerOption {
	return func(o *ServerOptions) {
		o.MaxReplicaConnections = max
	}
}

// replicaClient tracks a connected replica
type replicaClient struct {
	id       string
	ctx      context.Context
	cancelFn context.CancelFunc
	logCtx   *logrus.Entry
	stream   replicationapi.Replication_SubscribeServer
	wg       sync.WaitGroup
}

// NewServer creates a new replication server
func NewServer(forwarder *replication.Forwarder, stateProvider AgentStateProvider, opts ...ServerOption) *Server {
	options := &ServerOptions{
		MaxReplicaConnections: 10, // Default max replicas
	}
	for _, opt := range opts {
		opt(options)
	}

	return &Server{
		forwarder:     forwarder,
		stateProvider: stateProvider,
		options:       options,
		replicas:      make(map[string]*replicaClient),
	}
}

// RegisterAdditionalService registers an additional gRPC service on the
// replication server. Must be called before Start.
func (s *Server) RegisterAdditionalService(register func(grpc.ServiceRegistrar)) {
	s.additionalRegistrations = append(s.additionalRegistrations, register)
}

// Start starts the gRPC server on the specified port
// This method is idempotent - calling it when already started will return nil
func (s *Server) Start(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Already running
	if s.grpcServer != nil {
		log().Debug("Replication server already running")
		return nil
	}

	addr := fmt.Sprintf("0.0.0.0:%d", port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = listener

	// Create gRPC server
	s.grpcServer = grpc.NewServer()
	replicationapi.RegisterReplicationServer(s.grpcServer, s)

	// Register additional services (e.g., HAAdmin)
	for _, register := range s.additionalRegistrations {
		register(s.grpcServer)
	}

	log().WithField("port", port).Info("Starting replication gRPC server")

	// Start serving in a goroutine
	go func() {
		if err := s.grpcServer.Serve(listener); err != nil {
			// Only log if not a clean shutdown
			s.mu.RLock()
			isRunning := s.grpcServer != nil
			s.mu.RUnlock()
			if isRunning {
				log().WithError(err).Error("Replication gRPC server error")
			}
		}
	}()

	return nil
}

// Subscribe handles the bidirectional replication stream.
// Replicas call this to receive events and send acknowledgments.
// Implements replicationapi.ReplicationServer interface.
func (s *Server) Subscribe(stream replicationapi.Replication_SubscribeServer) error {
	// Generate a unique ID for this replica connection
	replicaID := uuid.New().String()[:8]

	ctx, cancel := context.WithCancel(stream.Context())
	client := &replicaClient{
		id:       replicaID,
		ctx:      ctx,
		cancelFn: cancel,
		stream:   stream,
		logCtx: log().WithFields(logrus.Fields{
			"replica_id": replicaID,
		}),
	}

	// Check if we're at max capacity
	s.mu.Lock()
	if len(s.replicas) >= s.options.MaxReplicaConnections {
		s.mu.Unlock()
		client.logCtx.Warn("Rejecting replica connection: max connections reached")
		return status.Errorf(codes.ResourceExhausted, "max replica connections reached")
	}
	s.replicas[replicaID] = client
	s.mu.Unlock()

	client.logCtx.Info("Replica connected for replication")

	// Register a buffering adapter so events are queued (not lost) while
	// the replica fetches and applies its snapshot.
	inner := &streamAdapter{stream: stream, replicaID: replicaID}
	adapter := newBufferingAdapter(inner)
	s.forwarder.RegisterReplica(replicaID, adapter)

	// Ensure cleanup on exit
	defer func() {
		s.forwarder.UnregisterReplica(replicaID)
		s.mu.Lock()
		delete(s.replicas, replicaID)
		s.mu.Unlock()
		cancel()
		client.logCtx.Info("Replica disconnected from replication")
	}()

	// Wait for the replica's first ACK which signals that it has applied its
	// snapshot. Then flush buffered events and continue with normal streaming.
	firstAck, err := client.stream.Recv()
	if err != nil {
		return fmt.Errorf("waiting for initial ACK: %w", err)
	}
	s.forwarder.UpdateReplicaAck(replicaID, firstAck.AckedSequenceNum)
	client.logCtx.WithField("acked_seq", firstAck.AckedSequenceNum).Info("Replica snapshot applied, flushing buffered events")

	if err := adapter.Flush(); err != nil {
		return fmt.Errorf("flushing buffered events: %w", err)
	}

	// Start ACK receiver goroutine for subsequent ACKs
	client.wg.Add(1)
	go func() {
		defer client.wg.Done()
		s.receiveAcks(client)
	}()

	// Wait for context cancellation or error
	<-ctx.Done()

	client.wg.Wait()
	return ctx.Err()
}

// streamAdapter adapts Replication_SubscribeServer to replication.ReplicaStream
type streamAdapter struct {
	stream    replicationapi.Replication_SubscribeServer
	replicaID string
}

func (a *streamAdapter) Send(ev *replication.ReplicatedEvent) error {
	protoEv, err := convertToProto(ev)
	if err != nil {
		return fmt.Errorf("failed to convert event: %w", err)
	}
	return a.stream.Send(protoEv)
}

func (a *streamAdapter) Context() context.Context {
	return a.stream.Context()
}

// bufferingAdapter wraps streamAdapter to buffer events until the replica
// signals it has applied its snapshot. This eliminates the gap between
// snapshot generation and stream subscription where events could be lost.
type bufferingAdapter struct {
	inner     *streamAdapter
	mu        sync.Mutex
	buffering bool
	buffer    []*replication.ReplicatedEvent
}

func newBufferingAdapter(inner *streamAdapter) *bufferingAdapter {
	return &bufferingAdapter{
		inner:     inner,
		buffering: true,
	}
}

func (b *bufferingAdapter) Send(ev *replication.ReplicatedEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buffering {
		b.buffer = append(b.buffer, ev)
		return nil
	}
	return b.inner.Send(ev)
}

func (b *bufferingAdapter) Context() context.Context {
	return b.inner.Context()
}

// Flush sends all buffered events and switches to pass-through mode.
func (b *bufferingAdapter) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ev := range b.buffer {
		if err := b.inner.Send(ev); err != nil {
			return err
		}
	}
	b.buffer = nil
	b.buffering = false
	return nil
}

// convertToProto converts an internal ReplicatedEvent to the proto message
func convertToProto(ev *replication.ReplicatedEvent) (*replicationapi.ReplicatedEvent, error) {
	var protoEvent *replicationapi.ReplicatedEvent

	if ev.Event != nil && ev.Event.CloudEvent() != nil {
		pbEvent, err := format.ToProto(ev.Event.CloudEvent())
		if err != nil {
			return nil, err
		}
		protoEvent = &replicationapi.ReplicatedEvent{
			Event:           pbEvent,
			AgentName:       ev.AgentName,
			Direction:       string(ev.Direction),
			SequenceNum:     ev.SequenceNum,
			ProcessedAtUnix: ev.ProcessedAtUnix,
		}
	} else {
		protoEvent = &replicationapi.ReplicatedEvent{
			AgentName:       ev.AgentName,
			Direction:       string(ev.Direction),
			SequenceNum:     ev.SequenceNum,
			ProcessedAtUnix: ev.ProcessedAtUnix,
		}
	}

	return protoEvent, nil
}

// receiveAcks handles incoming acknowledgments from a replica
func (s *Server) receiveAcks(client *replicaClient) {
	for {
		select {
		case <-client.ctx.Done():
			return
		default:
		}

		ack, err := client.stream.Recv()
		if err != nil {
			if err == io.EOF {
				client.logCtx.Debug("Replica closed ACK stream")
			} else {
				st, ok := status.FromError(err)
				if ok && (st.Code() == codes.Canceled || st.Code() == codes.DeadlineExceeded) {
					client.logCtx.Debug("ACK stream canceled")
				} else {
					client.logCtx.WithError(err).Warn("Error receiving ACK from replica")
				}
			}
			client.cancelFn()
			return
		}

		// Update the forwarder with the ACK
		s.forwarder.UpdateReplicaAck(client.id, ack.AckedSequenceNum)
		client.logCtx.WithField("acked_seq", ack.AckedSequenceNum).Trace("Received ACK from replica")
	}
}

// GetSnapshot returns the current state snapshot for initial sync.
// TODO: use req.SinceSequenceNum to return an incremental snapshot
// instead of the full state on every reconnect.
// Implements replicationapi.ReplicationServer interface.
func (s *Server) GetSnapshot(ctx context.Context, req *replicationapi.SnapshotRequest) (*replicationapi.ReplicationSnapshot, error) {
	if s.stateProvider == nil {
		return nil, status.Errorf(codes.Internal, "state provider not configured")
	}

	snapshot := &replicationapi.ReplicationSnapshot{
		Agents:          make([]*replicationapi.AgentState, 0),
		LastSequenceNum: s.forwarder.CurrentSequenceNum(),
	}

	agentNames := s.stateProvider.GetAllAgentNames()
	for _, name := range agentNames {
		agentState := &replicationapi.AgentState{
			Name:      name,
			Mode:      s.stateProvider.GetAgentMode(name),
			Connected: s.stateProvider.IsAgentConnected(name),
			Resources: make([]*replicationapi.Resource, 0),
		}

		resources := s.stateProvider.GetAgentResources(name)
		for _, res := range resources {
			agentState.Resources = append(agentState.Resources, &replicationapi.Resource{
				Name:         res.Name,
				Namespace:    res.Namespace,
				Kind:         res.Kind,
				Uid:          res.UID,
				SpecChecksum: res.SpecChecksum,
				Data:         res.Data,
			})
		}

		snapshot.Agents = append(snapshot.Agents, agentState)
	}

	log().WithFields(logrus.Fields{
		"agent_count": len(snapshot.Agents),
		"sequence":    snapshot.LastSequenceNum,
	}).Info("Generated snapshot for replica")

	return snapshot, nil
}

// Status returns the current replication status.
// Implements replicationapi.ReplicationServer interface.
func (s *Server) Status(ctx context.Context, req *replicationapi.StatusRequest) (*replicationapi.ReplicationStatus, error) {
	forwarderStatus := s.forwarder.GetStatus()

	return &replicationapi.ReplicationStatus{
		CurrentSequenceNum: forwarderStatus.CurrentSequenceNum,
		PendingEvents:      int32(forwarderStatus.PendingEvents),
		ConnectedReplicas:  int32(forwarderStatus.ConnectedReplicas),
	}, nil
}

// ForwardEvent is called by the principal to forward an event for replication
func (s *Server) ForwardEvent(ev *event.Event, agentName string, direction replication.Direction) {
	s.forwarder.Forward(ev, agentName, direction)
}

// GetForwarder returns the underlying forwarder (for integration)
func (s *Server) GetForwarder() *replication.Forwarder {
	return s.forwarder
}

// ConnectedReplicaCount returns the number of connected replicas
func (s *Server) ConnectedReplicaCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.replicas)
}

// GetReplicaIDs returns the IDs of all connected replicas
func (s *Server) GetReplicaIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.replicas))
	for id := range s.replicas {
		ids = append(ids, id)
	}
	return ids
}

// DisconnectReplica forcefully disconnects a replica
func (s *Server) DisconnectReplica(replicaID string) {
	s.mu.RLock()
	client, ok := s.replicas[replicaID]
	s.mu.RUnlock()

	if ok {
		client.cancelFn()
	}
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	log().Info("Shutting down replication server")

	// Stop the gRPC server first
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
		s.grpcServer = nil
	}

	// Close the listener
	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
	}

	// Cancel all replica connections
	s.mu.Lock()
	for _, client := range s.replicas {
		client.cancelFn()
	}
	s.mu.Unlock()

	// Wait for all replicas to disconnect (with timeout)
	done := make(chan struct{})
	go func() {
		for {
			s.mu.RLock()
			count := len(s.replicas)
			s.mu.RUnlock()
			if count == 0 {
				close(done)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	select {
	case <-done:
		log().Info("All replicas disconnected")
	case <-ctx.Done():
		log().Warn("Shutdown timeout, some replicas may not have disconnected cleanly")
	}

	return nil
}
