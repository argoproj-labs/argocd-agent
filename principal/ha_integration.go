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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/haadminapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/replicationapi"
	"github.com/argoproj-labs/argocd-agent/pkg/ha"
	"github.com/argoproj-labs/argocd-agent/pkg/replication"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/principal/apis/haadmin"
	replicationserver "github.com/argoproj-labs/argocd-agent/principal/apis/replication"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"google.golang.org/grpc"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HAComponents holds all HA-related components for the principal server
type HAComponents struct {
	// Controller manages HA state machine
	Controller *ha.Controller

	// ReplicationForwarder forwards events to replica principals
	ReplicationForwarder *replication.Forwarder

	// ReplicationServer is the gRPC server for replication
	ReplicationServer *replicationserver.Server

	// HAAdminServer handles operator promote/demote/status RPCs
	HAAdminServer *haadmin.Server

	// adminGRPCServer is the localhost-only gRPC server for HAAdmin
	adminGRPCServer *grpc.Server

	// ReplicationClient connects to the primary when in replica mode
	ReplicationClient *replication.Client

	// stateProvider provides agent state to the replication server
	stateProvider *serverStateProvider

	// clientMu protects replication client lifecycle
	clientMu sync.Mutex
	// clientCtx is the context for the running replication client
	clientCtx context.Context
	// clientCancelFn cancels the replication client context
	clientCancelFn context.CancelFunc

	// forwarderMu protects forwarder lifecycle
	forwarderMu sync.Mutex
	// forwarderCtx is the context for the running forwarder
	forwarderCtx context.Context
	// forwarderCancelFn cancels the forwarder context
	forwarderCancelFn context.CancelFunc
}

// serverStateProvider implements replicationserver.AgentStateProvider
// using the principal server's internal state
type serverStateProvider struct {
	server *Server
}

// GetAllAgentNames returns the names of all known agents.
// Merges agents from namespaceMap (connected agents) and resources
// (agents with tracked resources from informer callbacks on startup).
func (p *serverStateProvider) GetAllAgentNames() []string {
	seen := make(map[string]bool)

	p.server.clientLock.RLock()
	for name := range p.server.namespaceMap {
		seen[name] = true
	}
	p.server.clientLock.RUnlock()

	for _, name := range p.server.resources.Names() {
		seen[name] = true
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// GetAgentMode returns the mode of an agent ("autonomous" or "managed")
func (p *serverStateProvider) GetAgentMode(agentName string) string {
	mode := p.server.agentMode(agentName)
	return mode.String()
}

// IsAgentConnected returns whether an agent is currently connected
func (p *serverStateProvider) IsAgentConnected(agentName string) bool {
	// An agent is considered connected if it has a send queue
	return p.server.queues.SendQ(agentName) != nil
}

// GetAgentResources returns the resources managed by an agent
func (p *serverStateProvider) GetAgentResources(agentName string) []replicationserver.ResourceInfo {
	res := p.server.resources.Get(agentName)
	if res == nil {
		return nil
	}

	resourceKeys := res.GetAll()
	resources := make([]replicationserver.ResourceInfo, 0, len(resourceKeys))
	for _, key := range resourceKeys {
		info := replicationserver.ResourceInfo{
			Name:      key.Name,
			Namespace: key.Namespace,
			Kind:      key.Kind,
			UID:       key.UID,
		}
		data, err := p.serializeResource(key)
		if err != nil {
			log().WithField("resource", key.String()).WithError(err).Warn("HA: skipping resource serialization")
		} else {
			info.Data = data
		}
		resources = append(resources, info)
	}

	return resources
}

func (p *serverStateProvider) serializeResource(key resources.ResourceKey) ([]byte, error) {
	ctx := p.server.ctx
	switch key.Kind {
	case "Application":
		if p.server.appManager == nil {
			return nil, nil
		}
		app, err := p.server.appManager.Get(ctx, key.Name, key.Namespace)
		if err != nil {
			return nil, err
		}
		return json.Marshal(app)
	case "AppProject":
		if p.server.projectManager == nil {
			return nil, nil
		}
		proj, err := p.server.projectManager.Get(ctx, key.Name, key.Namespace)
		if err != nil {
			return nil, err
		}
		return json.Marshal(proj)
	default:
		return nil, nil
	}
}

// NewHAComponents creates and initializes HA components for the server
func NewHAComponents(ctx context.Context, server *Server, haOpts ...ha.Option) (*HAComponents, error) {
	components := &HAComponents{
		stateProvider: &serverStateProvider{server: server},
	}

	// Create the replication forwarder
	components.ReplicationForwarder = replication.NewForwarder()

	// Create the HA controller first so we can read its options for mTLS
	controller, err := ha.NewController(ctx, haOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create HA controller: %w", err)
	}
	components.Controller = controller

	haOptions := controller.Options()

	// Create the replication server with mTLS options from HA config
	var replServerOpts []replicationserver.ServerOption
	if haOptions.TLSConfig != nil {
		tlsConfig := haOptions.TLSConfig.Clone()
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		replServerOpts = append(replServerOpts, replicationserver.WithTLSConfig(tlsConfig))
	}
	if haOptions.AuthMethod != nil {
		replServerOpts = append(replServerOpts, replicationserver.WithAuthMethod(haOptions.AuthMethod))
	}
	if len(haOptions.AllowedReplicationClients) > 0 {
		replServerOpts = append(replServerOpts, replicationserver.WithAllowedClients(haOptions.AllowedReplicationClients))
	}
	components.ReplicationServer = replicationserver.NewServer(
		components.ReplicationForwarder,
		components.stateProvider,
		replServerOpts...,
	)

	// Create the HAAdmin server
	components.HAAdminServer = haadmin.NewServer(controller, &haStatusProvider{components: components, server: server})

	// Create the localhost-only admin gRPC server (no TLS — access via kubectl port-forward)
	components.adminGRPCServer = grpc.NewServer()
	haadminapi.RegisterHAAdminServer(components.adminGRPCServer, components.HAAdminServer)

	// Create the replication client if HA is enabled
	if haOptions.Enabled && haOptions.PeerAddress != "" {
		// Build the replication address from peer address (health port) and replication port
		// PeerAddress is in the format "host:healthPort", we need to extract just the host
		peerHost, _, err := net.SplitHostPort(haOptions.PeerAddress)
		if err != nil {
			// If there's no port in the address, use it as-is
			peerHost = haOptions.PeerAddress
		}
		replicationAddr := fmt.Sprintf("%s:%d", peerHost, haOptions.ReplicationPort)

		clientOpts := []replication.ClientOption{
			replication.WithPrimaryAddress(replicationAddr),
			replication.WithReconnectBackoff(
				haOptions.ReconnectBackoffInitial,
				haOptions.ReconnectBackoffMax,
				haOptions.ReconnectBackoffFactor,
			),
			// Wire up callbacks to HA controller
			replication.WithOnConnected(func() {
				log().Info("HA: Replication stream connected to primary")
				controller.OnReplicationConnected()
			}),
			replication.WithOnDisconnected(func() {
				log().Warn("HA: Replication stream disconnected from primary")
				controller.OnReplicationDisconnected()
			}),
			replication.WithOnSyncComplete(func() {
				log().Info("HA: Initial sync from primary complete")
				controller.OnSyncComplete()
			}),
			// Snapshot handler to apply initial state from primary
			replication.WithSnapshotHandler(components.handleSnapshot),
			// Event handler to apply incremental events
			replication.WithEventHandler(components.handleReplicatedEvent),
		}

		// Configure TLS
		if haOptions.TLSConfig != nil {
			clientOpts = append(clientOpts, replication.WithClientTLS(haOptions.TLSConfig))
		} else {
			clientOpts = append(clientOpts, replication.WithInsecure())
		}

		components.ReplicationClient = replication.NewClient(ctx, clientOpts...)
	}

	// Set up callbacks for state changes
	controller.SetOnStateChange(func(from, to ha.State) {
		log().WithField("from", from).WithField("to", to).Info("HA state changed")
	})

	controller.SetOnBecomeActive(func(ctx context.Context) {
		log().Info("HA: This principal has become ACTIVE")
		components.stopReplicationClient()
		if err := components.ReplicationServer.Start(haOptions.ReplicationPort); err != nil {
			log().WithError(err).Error("HA: Failed to start replication server on become active")
		}
		components.startForwarder(ctx)
		if err := server.populateSourceCache(ctx); err != nil {
			log().WithError(err).Error("HA: Failed to populate source cache on promotion")
		}
	})

	controller.SetOnBecomeReplica(func(ctx context.Context) {
		log().Info("HA: This principal has become REPLICA")
		if server.eventStreamSrv != nil {
			server.eventStreamSrv.DisconnectAll()
		}
		components.stopForwarder()
		components.startReplicationClient(ctx)
	})

	return components, nil
}

// StartHA starts the HA components
func (h *HAComponents) StartHA(ctx context.Context) error {
	if h == nil {
		return nil
	}

	// Start the HA controller - this determines initial state
	if err := h.Controller.Start(); err != nil {
		return fmt.Errorf("failed to start HA controller: %w", err)
	}

	// Start components based on initial state
	initialState := h.Controller.State()
	log().WithField("initial_state", initialState).Info("HA: Starting components based on initial state")

	// Get replication port for the server
	haOptions := h.Controller.Options()

	// Start the localhost-only admin gRPC server for HAAdmin (status/promote/demote).
	// Binding to 127.0.0.1 means access requires kubectl port-forward — no mTLS needed.
	adminAddr := fmt.Sprintf("127.0.0.1:%d", haOptions.AdminPort)
	adminListener, err := net.Listen("tcp", adminAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on admin port %s: %w", adminAddr, err)
	}
	log().WithField("addr", adminAddr).Info("Starting HA admin gRPC server")
	go func() {
		if err := h.adminGRPCServer.Serve(adminListener); err != nil {
			log().WithError(err).Error("HA admin gRPC server error")
		}
	}()

	// Start the replication server (mTLS, principal-to-principal only)
	if err := h.ReplicationServer.Start(haOptions.ReplicationPort); err != nil {
		return fmt.Errorf("failed to start replication server: %w", err)
	}

	switch initialState {
	case ha.StateActive:
		h.startForwarder(ctx)
	case ha.StateReplicating, ha.StateSyncing:
		h.startReplicationClient(ctx)
	case ha.StateDisconnected:
		h.startReplicationClient(ctx)
	case ha.StateRecovering:
		log().Info("HA: Still recovering, components will start on state transition")
	}

	log().Info("HA components started")
	return nil
}

// ShutdownHA gracefully shuts down HA components
func (h *HAComponents) ShutdownHA(ctx context.Context) error {
	if h == nil {
		return nil
	}

	var errs []error

	// Stop the HA controller
	if err := h.Controller.Shutdown(); err != nil {
		errs = append(errs, fmt.Errorf("HA controller shutdown error: %w", err))
	}

	// Shutdown the replication server
	if err := h.ReplicationServer.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("replication server shutdown error: %w", err))
	}

	// Shutdown the admin gRPC server
	if h.adminGRPCServer != nil {
		h.adminGRPCServer.GracefulStop()
	}

	// Stop the forwarder if running
	h.stopForwarder()

	// Stop the replication client if running
	h.stopReplicationClient()
	if h.ReplicationClient != nil {
		if err := h.ReplicationClient.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("replication client stop error: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("HA shutdown errors: %v", errs)
	}

	log().Info("HA components shut down")
	return nil
}

// startReplicationClient starts the replication client to receive events from the primary
func (h *HAComponents) startReplicationClient(ctx context.Context) {
	if h == nil || h.ReplicationClient == nil {
		log().Warn("HA: Replication client not configured, cannot start")
		return
	}

	h.clientMu.Lock()
	defer h.clientMu.Unlock()

	// If already running, don't start again
	if h.clientCtx != nil {
		log().Debug("HA: Replication client already running")
		return
	}

	// Create a cancellable context for this client session
	h.clientCtx, h.clientCancelFn = context.WithCancel(ctx)

	// Capture context for goroutine to avoid race condition
	clientCtx := h.clientCtx

	go func() {
		log().Info("HA: Starting replication client")
		if err := h.ReplicationClient.Run(clientCtx); err != nil {
			if clientCtx.Err() == nil {
				// Only log if not cancelled
				log().WithError(err).Error("HA: Replication client stopped with error")
			}
		}
		log().Info("HA: Replication client stopped")
	}()
}

// stopReplicationClient stops the replication client
func (h *HAComponents) stopReplicationClient() {
	if h == nil {
		return
	}

	h.clientMu.Lock()
	defer h.clientMu.Unlock()

	if h.clientCancelFn != nil {
		log().Info("HA: Stopping replication client")
		h.clientCancelFn()
		h.clientCancelFn = nil
		h.clientCtx = nil
	}

	// Also disconnect the underlying connection
	if h.ReplicationClient != nil {
		if err := h.ReplicationClient.Disconnect(); err != nil {
			log().WithError(err).Warn("HA: Error disconnecting replication client")
		}
	}
}

// startForwarder starts the replication forwarder to send events to replicas
func (h *HAComponents) startForwarder(ctx context.Context) {
	if h == nil || h.ReplicationForwarder == nil {
		return
	}

	h.forwarderMu.Lock()
	defer h.forwarderMu.Unlock()

	// If already running, don't start again
	if h.forwarderCtx != nil {
		log().Debug("HA: Replication forwarder already running")
		return
	}

	// Create a cancellable context for the forwarder
	h.forwarderCtx, h.forwarderCancelFn = context.WithCancel(ctx)

	// Capture context for goroutine to avoid race condition
	forwarderCtx := h.forwarderCtx

	go func() {
		log().Info("HA: Starting replication forwarder")
		h.ReplicationForwarder.Run(forwarderCtx)
		log().Info("HA: Replication forwarder stopped")
	}()
}

// stopForwarder stops the replication forwarder
func (h *HAComponents) stopForwarder() {
	if h == nil {
		return
	}

	h.forwarderMu.Lock()
	defer h.forwarderMu.Unlock()

	if h.forwarderCancelFn != nil {
		log().Info("HA: Stopping replication forwarder")
		h.forwarderCancelFn()
		h.forwarderCancelFn = nil
		h.forwarderCtx = nil
	}
}

// handleReplicatedEvent processes events received from the primary.
// Updates local resource tracking so the replica is ready upon promotion.
func (h *HAComponents) handleReplicatedEvent(ev *replication.ReplicatedEvent) error {
	if h == nil || ev == nil || ev.Event == nil {
		return nil
	}

	ce := ev.Event.CloudEvent()
	if ce == nil {
		return nil
	}

	target := event.Target(ce)
	evType := event.EventType(ce.Type())
	server := h.stateProvider.server

	log().WithFields(map[string]any{
		"agent":    ev.AgentName,
		"target":   target,
		"type":     evType,
		"sequence": ev.SequenceNum,
	}).Debug("HA: Replaying event")

	ctx := server.ctx

	switch target {
	case event.TargetApplication:
		app, err := ev.Event.Application()
		if err != nil {
			return fmt.Errorf("failed to decode application from replicated event: %w", err)
		}
		key := resources.NewResourceKeyFromApp(app)
		switch evType {
		case event.Create, event.SpecUpdate, event.StatusUpdate, event.TerminateOperation:
			if _, err := server.appManager.Upsert(ctx, app); err != nil {
				log().WithField("app", app.QualifiedName()).WithError(err).Warn("HA: failed to upsert application from replicated event")
			}
			server.resources.Add(ev.AgentName, key)
		case event.Delete:
			if err := server.appManager.Delete(ctx, app.Namespace, app, nil); err != nil && !k8serrors.IsNotFound(err) {
				log().WithField("app", app.QualifiedName()).WithError(err).Warn("HA: failed to delete application from replicated event")
			}
			server.resources.Remove(ev.AgentName, key)
		}

	case event.TargetAppProject:
		proj, err := ev.Event.AppProject()
		if err != nil {
			return fmt.Errorf("failed to decode appproject from replicated event: %w", err)
		}
		key := resources.NewResourceKeyFromAppProject(proj)
		switch evType {
		case event.Create, event.SpecUpdate, event.StatusUpdate:
			if _, err := server.projectManager.Upsert(ctx, proj); err != nil {
				log().WithField("project", proj.Name).WithError(err).Warn("HA: failed to upsert appproject from replicated event")
			}
			server.resources.Add(ev.AgentName, key)
		case event.Delete:
			if err := server.projectManager.Delete(ctx, proj, nil); err != nil && !k8serrors.IsNotFound(err) {
				log().WithField("project", proj.Name).WithError(err).Warn("HA: failed to delete appproject from replicated event")
			}
			server.resources.Remove(ev.AgentName, key)
		}
	}

	return nil
}

// handleSnapshot applies a snapshot received from the primary.
// This is called during initial sync to establish baseline state.
func (h *HAComponents) handleSnapshot(snapshot *replicationapi.ReplicationSnapshot) error {
	if h == nil || snapshot == nil {
		return nil
	}

	server := h.stateProvider.server

	log().WithFields(map[string]any{
		"agents":        len(snapshot.Agents),
		"last_sequence": snapshot.LastSequenceNum,
	}).Info("HA: Applying snapshot from primary")

	for _, agentState := range snapshot.Agents {
		log().WithFields(map[string]any{
			"agent":     agentState.Name,
			"mode":      agentState.Mode,
			"resources": len(agentState.Resources),
		}).Debug("HA: Syncing agent state from snapshot")

		// Set agent mode in namespace map
		mode := types.AgentModeFromString(agentState.Mode)
		server.setAgentMode(agentState.Name, mode)

		// Build set of snapshot resource keys for this agent
		snapshotKeys := make(map[resources.ResourceKey]struct{}, len(agentState.Resources))

		// Sync resources: write full objects to cluster and track keys
		for _, res := range agentState.Resources {
			key := resources.ResourceKey{
				Name:      res.Name,
				Namespace: res.Namespace,
				Kind:      res.Kind,
				UID:       res.Uid,
			}
			snapshotKeys[key] = struct{}{}
			if len(res.Data) > 0 {
				if err := h.upsertResourceFromSnapshot(server.ctx, server, res); err != nil {
					log().WithField("resource", key.String()).WithError(err).Warn("HA: failed to upsert resource from snapshot")
				} else {
					log().WithField("resource", key.String()).Debug("HA: upserted resource from snapshot")
				}
			} else {
				log().WithField("resource", key.String()).Warn("HA: snapshot resource has no data")
			}
			server.resources.Add(agentState.Name, key)
		}

		// Remove resources that exist locally but are absent from the snapshot
		for _, existing := range server.resources.GetAllResources(agentState.Name) {
			if _, ok := snapshotKeys[existing]; ok {
				continue
			}
			h.deleteStaleResource(server.ctx, server, existing)
			server.resources.Remove(agentState.Name, existing)
		}

		// Ensure queue pair exists so the replica is ready to serve
		// this agent if promoted
		if !server.queues.HasQueuePair(agentState.Name) {
			if err := server.queues.Create(agentState.Name); err != nil {
				log().WithField("agent", agentState.Name).WithError(err).Warn("HA: Failed to create queue pair from snapshot")
			}
		}
	}

	log().WithField("sequence", snapshot.LastSequenceNum).Info("HA: Snapshot applied successfully")
	return nil
}

func (h *HAComponents) upsertResourceFromSnapshot(ctx context.Context, server *Server, res *replicationapi.Resource) error {
	switch res.Kind {
	case "Application":
		var app v1alpha1.Application
		if err := json.Unmarshal(res.Data, &app); err != nil {
			return fmt.Errorf("unmarshal application: %w", err)
		}
		if _, err := server.appManager.Upsert(ctx, &app); err != nil {
			return fmt.Errorf("upsert application: %w", err)
		}
	case "AppProject":
		var proj v1alpha1.AppProject
		if err := json.Unmarshal(res.Data, &proj); err != nil {
			return fmt.Errorf("unmarshal appproject: %w", err)
		}
		if _, err := server.projectManager.Upsert(ctx, &proj); err != nil {
			return fmt.Errorf("upsert appproject: %w", err)
		}
	}
	return nil
}

func (h *HAComponents) deleteStaleResource(ctx context.Context, server *Server, key resources.ResourceKey) {
	switch key.Kind {
	case "Application":
		if server.appManager != nil {
			if err := server.appManager.Delete(ctx, key.Namespace, &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			}, nil); err != nil && !k8serrors.IsNotFound(err) {
				log().WithField("resource", key.String()).WithError(err).Warn("HA: failed to delete stale application")
			}
		}
	case "AppProject":
		if server.projectManager != nil {
			if err := server.projectManager.Delete(ctx, &v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			}, nil); err != nil && !k8serrors.IsNotFound(err) {
				log().WithField("resource", key.String()).WithError(err).Warn("HA: failed to delete stale appproject")
			}
		}
	}
}

// ForwardEventForReplication forwards an event to replicas
// This should be called after processing events from agents
func (h *HAComponents) ForwardEventForReplication(ev *event.Event, agentName string, direction replication.Direction) {
	if h == nil || h.ReplicationForwarder == nil {
		return
	}

	// Only forward events when we're in active state
	if h.Controller != nil && h.Controller.State() != ha.StateActive {
		return
	}

	log().WithFields(map[string]any{
		"agent":     agentName,
		"direction": direction,
		"target":    event.Target(ev.CloudEvent()),
		"type":      ev.CloudEvent().Type(),
	}).Debug("HA: Forwarding event for replication")

	h.ReplicationServer.ForwardEvent(ev, agentName, direction)
}

// OnAgentConnect should be called when an agent connects to the principal
func (h *HAComponents) OnAgentConnect(agent types.Agent) error {
	if h == nil || h.Controller == nil {
		return nil
	}

	return h.Controller.OnAgentConnect(agent.Name())
}

// OnAgentDisconnect should be called when an agent disconnects from the principal
// Note: The controller doesn't have OnAgentDisconnect - agents disconnecting
// doesn't affect HA state directly
func (h *HAComponents) OnAgentDisconnect(agentName string) {
	if h == nil || h.Controller == nil {
		return
	}
	// No action needed - agent disconnect doesn't trigger HA state changes
	log().WithField("agent", agentName).Debug("Agent disconnected (HA notified)")
}

// HAHealthzHandler returns an HTTP handler that reports HA-aware health status
// The health check returns:
// - 200 OK if this principal should receive traffic (active, standby, or standalone)
// - 503 Service Unavailable if this principal is a replica and should not receive traffic
func (h *HAComponents) HAHealthzHandler(baseHandler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h == nil || h.Controller == nil {
			// No HA configured, use base handler
			baseHandler(w, r)
			return
		}

		state := h.Controller.State()

		// Add HA status headers for debugging
		w.Header().Set("X-HA-State", string(state))

		// Only healthy states should receive agent traffic
		if state.IsHealthy() {
			baseHandler(w, r)
			return
		}

		// Replicating/disconnected/syncing principals should return unhealthy
		// so GSLB routes traffic to the active principal
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "HA state: %s - not accepting traffic\n", state)
	}
}

// GetHAStatus returns the current HA status for metrics/debugging
func (h *HAComponents) GetHAStatus() *HAStatus {
	if h == nil || h.Controller == nil {
		return &HAStatus{
			Enabled: false,
		}
	}

	controllerStatus := h.Controller.GetStatus()

	status := &HAStatus{
		Enabled:           true,
		State:             controllerStatus.State,
		PreferredRole:     controllerStatus.PreferredRole,
		PeerAddress:       controllerStatus.PeerAddress,
		PeerReachable:     controllerStatus.PeerReachable,
		PeerState:         controllerStatus.PeerState,
		ConnectedReplicas: h.ReplicationServer.ConnectedReplicaCount(),
	}

	// Get replication status if available
	if h.ReplicationClient != nil {
		clientStatus := h.ReplicationClient.GetStatus()
		status.LastEventTimestamp = clientStatus.LastEventTimestamp
		status.LastSequenceNum = clientStatus.LastSequenceNum
	}

	return status
}

// HAStatus represents the current HA status
type HAStatus struct {
	Enabled           bool
	State             ha.State
	PreferredRole     ha.Role
	PeerAddress       string
	PeerReachable     bool
	PeerState         ha.State
	ConnectedReplicas int
	LastEventTimestamp int64
	LastSequenceNum   uint64
}

// haStatusProvider implements haadmin.StatusProvider using HAComponents
type haStatusProvider struct {
	components *HAComponents
	server     *Server
}

func (p *haStatusProvider) ConnectedReplicaCount() int {
	if p.components.ReplicationServer == nil {
		return 0
	}
	return p.components.ReplicationServer.ConnectedReplicaCount()
}

func (p *haStatusProvider) LastEventTimestamp() int64 {
	if p.components.ReplicationClient == nil {
		return 0
	}
	return p.components.ReplicationClient.GetStatus().LastEventTimestamp
}

func (p *haStatusProvider) LastSequenceNum() uint64 {
	if p.components.ReplicationClient == nil {
		return 0
	}
	return p.components.ReplicationClient.GetStatus().LastSequenceNum
}

func (p *haStatusProvider) ConnectedAgentCount() int {
	if p.server == nil || p.server.eventStreamSrv == nil {
		return 0
	}
	return p.server.eventStreamSrv.ConnectedAgentCount()
}

// IsActive returns true if this principal is currently active
func (h *HAComponents) IsActive() bool {
	if h == nil || h.Controller == nil {
		return true // No HA = standalone = active
	}
	return h.Controller.State() == ha.StateActive
}

// ShouldAcceptAgents returns true if this principal should accept agent connections
func (h *HAComponents) ShouldAcceptAgents() bool {
	if h == nil || h.Controller == nil {
		return true // No HA = standalone = accept all
	}
	return h.Controller.ShouldAcceptAgents()
}
