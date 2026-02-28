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
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
)

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ModuleLogger("replication")
}

// Direction indicates the direction of an event
type Direction string

const (
	// DirectionInbound indicates an event from agent to principal
	DirectionInbound Direction = "inbound"
	// DirectionOutbound indicates an event from principal to agent
	DirectionOutbound Direction = "outbound"
)

// ReplicatedEvent wraps an event with replication metadata
type ReplicatedEvent struct {
	Event           *event.Event
	AgentName       string
	Direction       Direction
	SequenceNum     uint64
	ProcessedAtUnix int64
	// protoEvent holds the original proto event when received from the replication stream.
	// This is used to access the CloudEvent data without reconversion.
	protoEvent any
}

// ReplicaStream represents a connected replica's event stream
type ReplicaStream interface {
	Send(ev *ReplicatedEvent) error
	Context() context.Context
}

// Forwarder handles forwarding events to replica principals
type Forwarder struct {
	mu sync.RWMutex

	// Sequence number for ordering
	sequenceNum atomic.Uint64

	// Queue of events to replicate
	pendingEvents chan *ReplicatedEvent

	// Connected replicas
	replicas map[string]*replicaConnection

	// Metrics
	metrics *ForwarderMetrics

	// Buffer size for pending events
	bufferSize int
}

type replicaConnection struct {
	id     string
	stream ReplicaStream
	acked  atomic.Uint64
}

// ForwarderMetrics contains metrics for the forwarder
type ForwarderMetrics struct {
	EventsQueued       prometheus.Counter
	EventsDropped      prometheus.Counter
	EventsForwarded    prometheus.Counter
	ReplicasConnected  prometheus.Gauge
	QueueSize          prometheus.Gauge
	ForwardingErrors   prometheus.Counter
	LastSequenceNumber prometheus.Gauge
}

var (
	forwarderMetricsOnce     sync.Once
	forwarderMetricsInstance *ForwarderMetrics
)

// NewForwarderMetrics creates metrics for the forwarder
func NewForwarderMetrics() *ForwarderMetrics {
	forwarderMetricsOnce.Do(func() {
		forwarderMetricsInstance = &ForwarderMetrics{
			EventsQueued: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_events_queued_total",
				Help: "Total number of events queued for replication",
			}),
			EventsDropped: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_events_dropped_total",
				Help: "Total number of events dropped due to full queue",
			}),
			EventsForwarded: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_events_forwarded_total",
				Help: "Total number of events successfully forwarded to replicas",
			}),
			ReplicasConnected: promauto.NewGauge(prometheus.GaugeOpts{
				Name: "argocd_agent_replication_replicas_connected",
				Help: "Number of replica principals currently connected",
			}),
			QueueSize: promauto.NewGauge(prometheus.GaugeOpts{
				Name: "argocd_agent_replication_queue_size",
				Help: "Current number of events in the replication queue",
			}),
			ForwardingErrors: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_forwarding_errors_total",
				Help: "Total number of errors while forwarding events",
			}),
			LastSequenceNumber: promauto.NewGauge(prometheus.GaugeOpts{
				Name: "argocd_agent_replication_last_sequence_number",
				Help: "The last sequence number assigned to an event",
			}),
		}
	})
	return forwarderMetricsInstance
}

// ForwarderOption configures a Forwarder
type ForwarderOption func(*Forwarder)

// WithBufferSize sets the buffer size for the pending events channel
func WithBufferSize(size int) ForwarderOption {
	return func(f *Forwarder) {
		f.bufferSize = size
	}
}

// NewForwarder creates a new Forwarder
func NewForwarder(opts ...ForwarderOption) *Forwarder {
	f := &Forwarder{
		replicas:   make(map[string]*replicaConnection),
		metrics:    NewForwarderMetrics(),
		bufferSize: 1000, // Default buffer size
	}

	for _, opt := range opts {
		opt(f)
	}

	f.pendingEvents = make(chan *ReplicatedEvent, f.bufferSize)

	return f
}

// Forward queues an event for replication to all connected replicas
func (f *Forwarder) Forward(ev *event.Event, agentName string, direction Direction) {
	seq := f.sequenceNum.Add(1)

	replEv := &ReplicatedEvent{
		Event:           ev,
		AgentName:       agentName,
		Direction:       direction,
		SequenceNum:     seq,
		ProcessedAtUnix: time.Now().Unix(),
	}

	select {
	case f.pendingEvents <- replEv:
		f.metrics.EventsQueued.Inc()
		f.metrics.QueueSize.Inc()
		f.metrics.LastSequenceNumber.Set(float64(seq))
	default:
		// Queue full - log warning, event will be dropped
		log().WithField("sequence_num", seq).Warn("Replication queue full, dropping event")
		f.metrics.EventsDropped.Inc()
	}
}

// RegisterReplica adds a replica connection
func (f *Forwarder) RegisterReplica(id string, stream ReplicaStream) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.replicas[id]; !exists {
		f.metrics.ReplicasConnected.Inc()
		log().WithField("replica_id", id).Info("Replica connected for replication")
	}
	f.replicas[id] = &replicaConnection{
		id:     id,
		stream: stream,
	}
}

// UnregisterReplica removes a replica connection
func (f *Forwarder) UnregisterReplica(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.replicas[id]; ok {
		delete(f.replicas, id)
		f.metrics.ReplicasConnected.Dec()
		log().WithField("replica_id", id).Info("Replica disconnected from replication")
	}
}

// ReplicaCount returns the number of connected replicas
func (f *Forwarder) ReplicaCount() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.replicas)
}

// CurrentSequenceNum returns the current sequence number
func (f *Forwarder) CurrentSequenceNum() uint64 {
	return f.sequenceNum.Load()
}

// PendingEventCount returns the number of events waiting to be forwarded
func (f *Forwarder) PendingEventCount() int {
	return len(f.pendingEvents)
}

// Run starts the forwarding loop. It should be called in a goroutine.
func (f *Forwarder) Run(ctx context.Context) {
	log().Info("Starting replication forwarder")

	for {
		select {
		case <-ctx.Done():
			log().Info("Replication forwarder stopped")
			return
		case ev := <-f.pendingEvents:
			f.metrics.QueueSize.Dec()
			f.broadcastToReplicas(ev)
		}
	}
}

// broadcastToReplicas sends an event to all connected replicas
func (f *Forwarder) broadcastToReplicas(ev *ReplicatedEvent) {
	var dead []string

	f.mu.RLock()
	if len(f.replicas) == 0 {
		f.mu.RUnlock()
		return
	}

	for id, replica := range f.replicas {
		select {
		case <-replica.stream.Context().Done():
			dead = append(dead, id)
			continue
		default:
		}

		if err := replica.stream.Send(ev); err != nil {
			log().WithField("replica_id", id).WithError(err).Warn("Failed to send event to replica")
			f.metrics.ForwardingErrors.Inc()
		} else {
			f.metrics.EventsForwarded.Inc()
		}
	}
	f.mu.RUnlock()

	if len(dead) > 0 {
		f.mu.Lock()
		for _, id := range dead {
			if _, ok := f.replicas[id]; ok {
				delete(f.replicas, id)
				f.metrics.ReplicasConnected.Dec()
				log().WithField("replica_id", id).Info("Removed dead replica during broadcast")
			}
		}
		f.mu.Unlock()
	}
}

// UpdateReplicaAck updates the acknowledged sequence number for a replica
func (f *Forwarder) UpdateReplicaAck(id string, ackedSeq uint64) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if replica, ok := f.replicas[id]; ok {
		replica.acked.Store(ackedSeq)
	}
}

// GetReplicaLag returns the replication lag (in events) for a specific replica
func (f *Forwarder) GetReplicaLag(id string) uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if replica, ok := f.replicas[id]; ok {
		currentSeq := f.sequenceNum.Load()
		ackedSeq := replica.acked.Load()
		if currentSeq > ackedSeq {
			return currentSeq - ackedSeq
		}
	}
	return 0
}

// Status returns the current status of the forwarder
type ForwarderStatus struct {
	CurrentSequenceNum uint64
	PendingEvents      int
	ConnectedReplicas  int
	ReplicaLags        map[string]uint64
}

// GetStatus returns the current forwarder status
func (f *Forwarder) GetStatus() *ForwarderStatus {
	f.mu.RLock()
	defer f.mu.RUnlock()

	currentSeq := f.sequenceNum.Load()
	replicaLags := make(map[string]uint64)

	for id, replica := range f.replicas {
		ackedSeq := replica.acked.Load()
		if currentSeq > ackedSeq {
			replicaLags[id] = currentSeq - ackedSeq
		} else {
			replicaLags[id] = 0
		}
	}

	return &ForwarderStatus{
		CurrentSequenceNum: currentSeq,
		PendingEvents:      len(f.pendingEvents),
		ConnectedReplicas:  len(f.replicas),
		ReplicaLags:        replicaLags,
	}
}

// ToProto converts a ReplicatedEvent to its protobuf representation
func (ev *ReplicatedEvent) ToProto() (*ReplicatedEventProto, error) {
	if ev.Event == nil || ev.Event.CloudEvent() == nil {
		return nil, nil
	}

	protoEvent, err := format.ToProto(ev.Event.CloudEvent())
	if err != nil {
		return nil, err
	}

	return &ReplicatedEventProto{
		Event:           protoEvent,
		AgentName:       ev.AgentName,
		Direction:       string(ev.Direction),
		SequenceNum:     ev.SequenceNum,
		ProcessedAtUnix: ev.ProcessedAtUnix,
	}, nil
}

// ReplicatedEventProto is a temporary struct until proto generation is set up
// This mirrors the proto message structure
type ReplicatedEventProto struct {
	Event           any // *pb.CloudEvent
	AgentName       string
	Direction       string
	SequenceNum     uint64
	ProcessedAtUnix int64
}
