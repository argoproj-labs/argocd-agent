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
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/replicationapi"
	"github.com/argoproj-labs/argocd-agent/pkg/replication"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ModuleLogger("replication_server")
}

// AgentStateProvider provides agent state for snapshots
type AgentStateProvider interface {
	GetAllAgentNames() []string
	GetAgentMode(agentName string) string
	IsAgentConnected(agentName string) bool
	GetAgentResources(agentName string) []ResourceInfo
	GetPrincipalResources() []ResourceInfo
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

// Server implements the gRPC replication service.
// Auth is handled by the main server's interceptors; this struct only
// contains service logic and replica tracking.
type Server struct {
	replicationapi.UnimplementedReplicationServer

	mu sync.RWMutex

	forwarder     *replication.Forwarder
	stateProvider AgentStateProvider
	options       *ServerOptions
	replicas      map[string]*replicaClient
}

// ServerOptions configures the replication server
type ServerOptions struct {
	MaxReplicaConnections int
	InitialAckTimeout     time.Duration
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
		MaxReplicaConnections: 10,
		InitialAckTimeout:     30 * time.Second,
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

// Subscribe handles the bidirectional replication stream.
func (s *Server) Subscribe(stream replicationapi.Replication_SubscribeServer) error {
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

	s.mu.Lock()
	if len(s.replicas) >= s.options.MaxReplicaConnections {
		s.mu.Unlock()
		client.logCtx.Warn("Rejecting replica connection: max connections reached")
		return status.Errorf(codes.ResourceExhausted, "max replica connections reached")
	}
	s.replicas[replicaID] = client
	s.mu.Unlock()

	client.logCtx.Info("Replica connected for replication")

	inner := &streamAdapter{stream: stream, replicaID: replicaID}
	adapter := newBufferingAdapter(inner)
	s.forwarder.RegisterReplica(replicaID, adapter)

	defer func() {
		s.forwarder.UnregisterReplica(replicaID)
		s.mu.Lock()
		delete(s.replicas, replicaID)
		s.mu.Unlock()
		cancel()
		client.logCtx.Info("Replica disconnected from replication")
	}()

	type ackResult struct {
		ack *replicationapi.ReplicationAck
		err error
	}
	ackCh := make(chan ackResult, 1)
	ackCtx, ackCancel := context.WithTimeout(ctx, s.options.InitialAckTimeout)
	defer ackCancel()
	go func() {
		ack, err := client.stream.Recv()
		ackCh <- ackResult{ack, err}
	}()
	var firstAck *replicationapi.ReplicationAck
	select {
	case res := <-ackCh:
		if res.err != nil {
			return fmt.Errorf("waiting for initial ACK: %w", res.err)
		}
		firstAck = res.ack
	case <-ackCtx.Done():
		return fmt.Errorf("waiting for initial ACK: timed out after %s", s.options.InitialAckTimeout)
	}
	s.forwarder.UpdateReplicaAck(replicaID, firstAck.AckedSequenceNum)
	client.logCtx.WithField("acked_seq", firstAck.AckedSequenceNum).Info("Replica snapshot applied, flushing buffered events")

	if err := adapter.Flush(); err != nil {
		return fmt.Errorf("flushing buffered events: %w", err)
	}

	client.wg.Add(1)
	go func() {
		defer client.wg.Done()
		s.receiveAcks(client)
	}()

	<-ctx.Done()

	client.wg.Wait()
	return ctx.Err()
}

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

		s.forwarder.UpdateReplicaAck(client.id, ack.AckedSequenceNum)
		client.logCtx.WithField("acked_seq", ack.AckedSequenceNum).Trace("Received ACK from replica")
	}
}

// GetSnapshot returns the current state snapshot for initial sync.
func (s *Server) GetSnapshot(ctx context.Context, req *replicationapi.SnapshotRequest) (*replicationapi.ReplicationSnapshot, error) {
	if s.stateProvider == nil {
		return nil, status.Errorf(codes.Internal, "state provider not configured")
	}

	snapshot := &replicationapi.ReplicationSnapshot{
		Agents:          make([]*replicationapi.AgentState, 0),
		LastSequenceNum: s.forwarder.CurrentSequenceNum(),
	}

	principalResources := s.stateProvider.GetPrincipalResources()
	for _, res := range principalResources {
		snapshot.PrincipalResources = append(snapshot.PrincipalResources, &replicationapi.Resource{
			Name:         res.Name,
			Namespace:    res.Namespace,
			Kind:         res.Kind,
			Uid:          res.UID,
			SpecChecksum: res.SpecChecksum,
			Data:         res.Data,
		})
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

// GetForwarder returns the underlying forwarder
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

// DisconnectAllReplicas cancels all connected replica streams
func (s *Server) DisconnectAllReplicas() {
	s.mu.Lock()
	for _, client := range s.replicas {
		client.cancelFn()
	}
	s.mu.Unlock()
}
