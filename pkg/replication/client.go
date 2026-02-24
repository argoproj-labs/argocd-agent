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
	"crypto/tls"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/replicationapi"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// ClientState represents the state of the replication client
type ClientState string

const (
	ClientStateDisconnected ClientState = "disconnected"
	ClientStateConnecting   ClientState = "connecting"
	ClientStateConnected    ClientState = "connected"
	ClientStateSyncing      ClientState = "syncing"
)

// EventHandler is called for each replicated event received
type EventHandler func(ev *ReplicatedEvent) error

// SnapshotHandler is called when a snapshot is received from the primary
type SnapshotHandler func(snapshot *replicationapi.ReplicationSnapshot) error

// Client connects to a primary principal and receives replicated events
type Client struct {
	mu sync.RWMutex

	// Connection settings
	primaryAddr string
	tlsConfig   *tls.Config
	insecure    bool

	// gRPC connection
	conn *grpc.ClientConn

	// State tracking
	state           ClientState
	lastSequenceNum atomic.Uint64
	lastEventTime   atomic.Int64

	// Callbacks
	eventHandler    EventHandler
	snapshotHandler SnapshotHandler
	onConnected     func()
	onDisconnected  func()
	onSyncComplete  func()

	// Reconnection settings
	reconnectBackoff        time.Duration
	reconnectBackoffMax     time.Duration
	reconnectBackoffFactor  float64
	currentReconnectBackoff time.Duration

	// ACK settings
	ackInterval time.Duration

	// Reconciliation settings
	reconcileInterval time.Duration
	needsReconcile    atomic.Bool

	// Metrics
	metrics *ClientMetrics

	// Lifecycle
	ctx      context.Context
	cancelFn context.CancelFunc
}

// ClientMetrics contains metrics for the replication client
type ClientMetrics struct {
	ConnectionState     prometheus.Gauge
	EventsReceived      prometheus.Counter
	EventsProcessed     prometheus.Counter
	EventsErrors        prometheus.Counter
	EventsDropped       prometheus.Counter
	ReplicationLag      prometheus.Gauge
	ReconnectAttempts   prometheus.Counter
	LastSequenceNumber  prometheus.Gauge
	SyncSnapshotSize    prometheus.Gauge
	ReconciliationCount prometheus.Counter
	SequenceGaps        prometheus.Counter
}

var (
	clientMetricsOnce     sync.Once
	clientMetricsInstance *ClientMetrics
)

// NewClientMetrics creates metrics for the client
func NewClientMetrics() *ClientMetrics {
	clientMetricsOnce.Do(func() {
		clientMetricsInstance = &ClientMetrics{
			ConnectionState: promauto.NewGauge(prometheus.GaugeOpts{
				Name: "argocd_agent_replication_client_connected",
				Help: "Whether the replication client is connected (1) or not (0)",
			}),
			EventsReceived: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_client_events_received_total",
				Help: "Total number of replication events received",
			}),
			EventsProcessed: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_client_events_processed_total",
				Help: "Total number of replication events successfully processed",
			}),
			EventsErrors: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_client_events_errors_total",
				Help: "Total number of errors processing replication events",
			}),
			EventsDropped: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_client_events_dropped_total",
				Help: "Total number of replicated events dropped due to processing failures",
			}),
			ReplicationLag: promauto.NewGauge(prometheus.GaugeOpts{
				Name: "argocd_agent_replication_client_lag_seconds",
				Help: "Replication lag in seconds (time since last event was processed on primary)",
			}),
			ReconnectAttempts: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_client_reconnect_attempts_total",
				Help: "Total number of reconnection attempts",
			}),
			LastSequenceNumber: promauto.NewGauge(prometheus.GaugeOpts{
				Name: "argocd_agent_replication_client_last_sequence_number",
				Help: "The last sequence number received from primary",
			}),
			SyncSnapshotSize: promauto.NewGauge(prometheus.GaugeOpts{
				Name: "argocd_agent_replication_client_snapshot_agents",
				Help: "Number of agents in the last received snapshot",
			}),
			ReconciliationCount: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_client_reconciliations_total",
				Help: "Total number of reconciliation snapshot fetches",
			}),
			SequenceGaps: promauto.NewCounter(prometheus.CounterOpts{
				Name: "argocd_agent_replication_client_sequence_gaps_total",
				Help: "Total number of sequence gaps detected in event stream",
			}),
		}
	})
	return clientMetricsInstance
}

// ClientOption configures a Client
type ClientOption func(*Client)

// WithPrimaryAddress sets the address of the primary principal
func WithPrimaryAddress(addr string) ClientOption {
	return func(c *Client) {
		c.primaryAddr = addr
	}
}

// WithClientTLS sets the TLS configuration for the client
func WithClientTLS(config *tls.Config) ClientOption {
	return func(c *Client) {
		c.tlsConfig = config
		c.insecure = false
	}
}

// WithInsecure disables TLS for the client connection
func WithInsecure() ClientOption {
	return func(c *Client) {
		c.insecure = true
	}
}

// WithEventHandler sets the callback for processing replicated events
func WithEventHandler(handler EventHandler) ClientOption {
	return func(c *Client) {
		c.eventHandler = handler
	}
}

// WithSnapshotHandler sets the callback for processing snapshots from the primary
func WithSnapshotHandler(handler SnapshotHandler) ClientOption {
	return func(c *Client) {
		c.snapshotHandler = handler
	}
}

// WithReconnectBackoff sets the reconnection backoff parameters
func WithReconnectBackoff(initial, max time.Duration, factor float64) ClientOption {
	return func(c *Client) {
		c.reconnectBackoff = initial
		c.reconnectBackoffMax = max
		c.reconnectBackoffFactor = factor
	}
}

// WithAckInterval sets how often to send acknowledgments
func WithAckInterval(interval time.Duration) ClientOption {
	return func(c *Client) {
		c.ackInterval = interval
	}
}

// WithReconcileInterval sets how often to check for sequence gaps and reconcile
func WithReconcileInterval(interval time.Duration) ClientOption {
	return func(c *Client) {
		c.reconcileInterval = interval
	}
}

// WithOnConnected sets the callback for when connection is established
func WithOnConnected(fn func()) ClientOption {
	return func(c *Client) {
		c.onConnected = fn
	}
}

// WithOnDisconnected sets the callback for when connection is lost
func WithOnDisconnected(fn func()) ClientOption {
	return func(c *Client) {
		c.onDisconnected = fn
	}
}

// WithOnSyncComplete sets the callback for when initial sync is complete
func WithOnSyncComplete(fn func()) ClientOption {
	return func(c *Client) {
		c.onSyncComplete = fn
	}
}

// NewClient creates a new replication client
func NewClient(ctx context.Context, opts ...ClientOption) *Client {
	c := &Client{
		state:                   ClientStateDisconnected,
		reconnectBackoff:        1 * time.Second,
		reconnectBackoffMax:     30 * time.Second,
		reconnectBackoffFactor:  1.5,
		currentReconnectBackoff: 1 * time.Second,
		ackInterval:             5 * time.Second,
		reconcileInterval:       1 * time.Minute,
		metrics:                 NewClientMetrics(),
	}

	c.ctx, c.cancelFn = context.WithCancel(ctx)

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Connect establishes the gRPC connection to the primary
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.primaryAddr == "" {
		return fmt.Errorf("primary address not configured")
	}

	c.setState(ClientStateConnecting)

	dialOpts := []grpc.DialOption{}

	if c.insecure {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else if c.tlsConfig != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(c.tlsConfig)))
	} else {
		return fmt.Errorf("TLS configuration required (or use WithInsecure)")
	}

	conn, err := grpc.NewClient(c.primaryAddr, dialOpts...)
	if err != nil {
		c.setState(ClientStateDisconnected)
		return fmt.Errorf("failed to connect to primary: %w", err)
	}

	c.conn = conn
	c.setState(ClientStateConnected)
	c.metrics.ConnectionState.Set(1)
	c.resetReconnectBackoff()

	log().WithField("primary", c.primaryAddr).Info("Connected to primary for replication")

	// Note: onConnected is called after sync completes, not on TCP connect
	// This ensures the HA controller doesn't prematurely cancel failover timer

	return nil
}

// Disconnect closes the connection to the primary
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			log().WithError(err).Warn("Error closing connection to primary")
		}
		c.conn = nil
	}

	c.setState(ClientStateDisconnected)
	c.metrics.ConnectionState.Set(0)

	if c.onDisconnected != nil {
		go c.onDisconnected()
	}

	return nil
}

// Stop stops the client and closes the connection
func (c *Client) Stop() error {
	c.cancelFn()
	return c.Disconnect()
}

// State returns the current client state
func (c *Client) State() ClientState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// IsConnected returns true if the client is connected
func (c *Client) IsConnected() bool {
	return c.State() == ClientStateConnected
}

// LastSequenceNum returns the last received sequence number
func (c *Client) LastSequenceNum() uint64 {
	return c.lastSequenceNum.Load()
}

// ProcessEvent processes a received replicated event
func (c *Client) ProcessEvent(ev *ReplicatedEvent) error {
	c.metrics.EventsReceived.Inc()

	// Update lag metric
	if ev.ProcessedAtUnix > 0 {
		lag := time.Now().Unix() - ev.ProcessedAtUnix
		c.metrics.ReplicationLag.Set(float64(lag))
	}

	// Call the event handler if configured
	if c.eventHandler != nil {
		if err := c.eventHandler(ev); err != nil {
			c.metrics.EventsErrors.Inc()
			return fmt.Errorf("event handler error: %w", err)
		}
	}

	// Update tracking
	c.lastSequenceNum.Store(ev.SequenceNum)
	c.lastEventTime.Store(time.Now().Unix())
	c.metrics.EventsProcessed.Inc()
	c.metrics.LastSequenceNumber.Set(float64(ev.SequenceNum))

	return nil
}

// GetStatus returns the current client status
func (c *Client) GetStatus() *ClientStatus {
	c.mu.RLock()
	state := c.state
	primaryAddr := c.primaryAddr
	c.mu.RUnlock()

	lastSeq := c.lastSequenceNum.Load()
	lastEventTime := c.lastEventTime.Load()

	return &ClientStatus{
		State:              state,
		PrimaryAddress:     primaryAddr,
		LastSequenceNum:    lastSeq,
		LastEventTimestamp: lastEventTime,
	}
}

// ClientStatus represents the current status of the replication client
type ClientStatus struct {
	State              ClientState
	PrimaryAddress     string
	LastSequenceNum    uint64
	LastEventTimestamp int64
}

// setState updates the client state (caller must hold lock)
func (c *Client) setState(state ClientState) {
	c.state = state
}

// resetReconnectBackoff resets the backoff to initial value
func (c *Client) resetReconnectBackoff() {
	c.currentReconnectBackoff = c.reconnectBackoff
}

// nextReconnectBackoff returns the next backoff duration and updates the internal state
func (c *Client) nextReconnectBackoff() time.Duration {
	current := c.currentReconnectBackoff
	next := time.Duration(float64(current) * c.reconnectBackoffFactor)
	c.currentReconnectBackoff = min(next, c.reconnectBackoffMax)
	return current
}

// RunWithReconnect runs the replication client with automatic reconnection
func (c *Client) RunWithReconnect(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try to connect
		connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := c.Connect(connectCtx)
		cancel()

		if err != nil {
			c.metrics.ReconnectAttempts.Inc()
			backoff := c.nextReconnectBackoff()
			log().WithError(err).WithField("backoff", backoff).Warn("Failed to connect to primary, will retry")

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		// Connected - the caller should handle the stream
		// This method just handles reconnection logic
		return nil
	}
}

// Conn returns the underlying gRPC connection (for creating service clients)
func (c *Client) Conn() *grpc.ClientConn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

// Run starts the replication client with automatic reconnection and event streaming.
// It subscribes first, then fetches the snapshot while events buffer server-side,
// then signals the server to flush buffered events. This eliminates the gap where
// events generated between snapshot and subscribe could be lost.
func (c *Client) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Connect to the primary
		if err := c.connectWithBackoff(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}

		// Run the event stream (subscribes, snapshots, then processes events)
		err := c.runEventStream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log().WithError(err).Warn("Event stream disconnected, will reconnect")
		}

		// Disconnect and prepare for reconnection
		c.Disconnect()
	}
}

// syncSnapshot fetches and applies the initial snapshot from the primary
func (c *Client) syncSnapshot(ctx context.Context) error {
	c.mu.Lock()
	c.setState(ClientStateSyncing)
	c.mu.Unlock()

	snapshot, err := c.GetSnapshot(ctx)
	if err != nil {
		return err
	}

	// Apply snapshot via handler if configured
	if c.snapshotHandler != nil {
		if err := c.snapshotHandler(snapshot); err != nil {
			return fmt.Errorf("failed to apply snapshot: %w", err)
		}
	}

	// Notify connected (after sync succeeds, not on TCP connect)
	// This ensures HA controller doesn't cancel failover timer prematurely
	if c.onConnected != nil {
		go c.onConnected()
	}

	// Notify sync complete
	if c.onSyncComplete != nil {
		c.onSyncComplete()
	}

	c.mu.Lock()
	c.setState(ClientStateConnected)
	c.mu.Unlock()

	return nil
}

// connectWithBackoff attempts to connect with exponential backoff
func (c *Client) connectWithBackoff(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := c.Connect(connectCtx)
		cancel()

		if err == nil {
			return nil
		}

		c.metrics.ReconnectAttempts.Inc()
		backoff := c.nextReconnectBackoff()
		log().WithError(err).WithField("backoff", backoff).Warn("Failed to connect to primary, will retry")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			continue
		}
	}
}

// runEventStream subscribes to the event stream, fetches and applies the
// snapshot while events buffer server-side, then signals the server to flush.
func (c *Client) runEventStream(ctx context.Context) error {
	conn := c.Conn()
	if conn == nil {
		return fmt.Errorf("not connected")
	}

	client := replicationapi.NewReplicationClient(conn)

	// Reset sequence tracking for this connection. The primary's forwarder
	// sequence is ephemeral and resets on restart, so we must not carry
	// stale values across reconnects.
	c.lastSequenceNum.Store(0)

	// 1. Subscribe first — server starts buffering events for us
	stream, err := client.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}
	log().Info("Subscribed to primary replication stream")

	// 2. Fetch and apply snapshot over the same connection
	if err := c.syncSnapshot(ctx); err != nil {
		return fmt.Errorf("snapshot sync failed: %w", err)
	}

	// 3. Send initial ACK with the snapshot sequence number to tell the
	//    server we're ready — it will flush buffered events.
	snapshotSeq := c.lastSequenceNum.Load()
	if err := stream.Send(&replicationapi.ReplicationAck{AckedSequenceNum: snapshotSeq}); err != nil {
		return fmt.Errorf("failed to send initial ACK: %w", err)
	}
	log().WithField("acked_seq", snapshotSeq).Info("Sent initial ACK after snapshot")

	// 4. Normal streaming
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	ackCh := make(chan uint64, 100)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.sendAcks(streamCtx, stream, ackCh)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.runReconcileLoop(streamCtx)
	}()

	err = c.receiveEvents(streamCtx, stream, ackCh)

	cancelStream()
	wg.Wait()

	return err
}

// receiveEvents processes incoming events from the stream
func (c *Client) receiveEvents(ctx context.Context, stream replicationapi.Replication_SubscribeClient, ackCh chan<- uint64) error {
	expectedSeq := c.lastSequenceNum.Load() + 1

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		protoEvent, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				log().Info("Replication stream closed by primary")
				return nil
			}
			st, ok := status.FromError(err)
			if ok && (st.Code() == codes.Canceled || st.Code() == codes.Unavailable) {
				log().Debug("Replication stream canceled or unavailable")
				return err
			}
			return fmt.Errorf("error receiving event: %w", err)
		}

		ev := c.protoToReplicatedEvent(protoEvent)

		if expectedSeq > 0 && ev.SequenceNum > expectedSeq {
			log().WithField("expected", expectedSeq).WithField("got", ev.SequenceNum).
				Warn("Sequence gap detected, marking for reconciliation")
			c.needsReconcile.Store(true)
			c.metrics.SequenceGaps.Inc()
		}
		expectedSeq = ev.SequenceNum + 1

		if err := c.ProcessEvent(ev); err != nil {
			log().WithError(err).WithField("sequence", ev.SequenceNum).Warn("Failed to process replicated event")
			c.metrics.EventsDropped.Inc()
			continue
		}

		select {
		case ackCh <- ev.SequenceNum:
		default:
		}
	}
}

// sendAcks periodically sends acknowledgments to the primary
func (c *Client) sendAcks(ctx context.Context, stream replicationapi.Replication_SubscribeClient, ackCh <-chan uint64) {
	ticker := time.NewTicker(c.ackInterval)
	defer ticker.Stop()

	var lastAcked uint64

	for {
		select {
		case <-ctx.Done():
			// Send final ACK before exiting
			if lastSeq := c.lastSequenceNum.Load(); lastSeq > lastAcked {
				_ = stream.Send(&replicationapi.ReplicationAck{AckedSequenceNum: lastSeq})
			}
			return

		case seq := <-ackCh:
			// Track the highest sequence number received
			if seq > lastAcked {
				lastAcked = seq
			}

		case <-ticker.C:
			// Send periodic ACK with highest processed sequence
			currentSeq := c.lastSequenceNum.Load()
			if currentSeq > 0 && currentSeq != lastAcked {
				if err := stream.Send(&replicationapi.ReplicationAck{AckedSequenceNum: currentSeq}); err != nil {
					log().WithError(err).Warn("Failed to send ACK")
					return
				}
				lastAcked = currentSeq
				log().WithField("acked_seq", currentSeq).Trace("Sent ACK to primary")
			}
		}
	}
}

// protoToReplicatedEvent converts a proto ReplicatedEvent to the internal type
func (c *Client) protoToReplicatedEvent(proto *replicationapi.ReplicatedEvent) *ReplicatedEvent {
	var ev *event.Event
	if proto.Event != nil {
		var err error
		ev, err = event.FromWire(proto.Event)
		if err != nil {
			log().WithError(err).Warn("Failed to convert proto CloudEvent in replicated event")
		}
	}
	return &ReplicatedEvent{
		Event:           ev,
		AgentName:       proto.AgentName,
		Direction:       Direction(proto.Direction),
		SequenceNum:     proto.SequenceNum,
		ProcessedAtUnix: proto.ProcessedAtUnix,
		protoEvent:      proto,
	}
}

func (c *Client) runReconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(c.reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.reconcile(ctx); err != nil {
				log().WithError(err).Warn("Reconciliation failed")
			}
		}
	}
}

func (c *Client) reconcile(ctx context.Context) error {
	conn := c.Conn()
	if conn == nil {
		return nil
	}

	client := replicationapi.NewReplicationClient(conn)

	status, err := client.Status(ctx, &replicationapi.StatusRequest{})
	if err != nil {
		return fmt.Errorf("status check failed: %w", err)
	}

	localSeq := c.lastSequenceNum.Load()
	remoteSeq := status.CurrentSequenceNum

	if localSeq >= remoteSeq && !c.needsReconcile.Load() {
		return nil
	}

	log().WithField("local_seq", localSeq).WithField("remote_seq", remoteSeq).
		Info("Reconciling: re-fetching snapshot")

	snapshot, err := c.GetSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("snapshot fetch failed: %w", err)
	}

	if c.snapshotHandler != nil {
		if err := c.snapshotHandler(snapshot); err != nil {
			return fmt.Errorf("snapshot apply failed: %w", err)
		}
	}

	c.needsReconcile.Store(false)
	c.metrics.ReconciliationCount.Inc()
	return nil
}

// GetSnapshot retrieves the current state snapshot from the primary.
// This should be called before subscribing to get the initial state.
func (c *Client) GetSnapshot(ctx context.Context) (*replicationapi.ReplicationSnapshot, error) {
	conn := c.Conn()
	if conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	client := replicationapi.NewReplicationClient(conn)

	req := &replicationapi.SnapshotRequest{
		SinceSequenceNum: c.lastSequenceNum.Load(),
	}

	snapshot, err := client.GetSnapshot(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot: %w", err)
	}

	// Update metrics. Use CAS to avoid overwriting a higher sequence number
	// that receiveEvents may have stored concurrently.
	if snapshot != nil {
		c.metrics.SyncSnapshotSize.Set(float64(len(snapshot.Agents)))
		for {
			current := c.lastSequenceNum.Load()
			if snapshot.LastSequenceNum <= current {
				break
			}
			if c.lastSequenceNum.CompareAndSwap(current, snapshot.LastSequenceNum) {
				break
			}
		}
		c.metrics.LastSequenceNumber.Set(float64(c.lastSequenceNum.Load()))
	}

	log().WithField("agents", len(snapshot.Agents)).WithField("sequence", snapshot.LastSequenceNum).Info("Received snapshot from primary")

	return snapshot, nil
}
