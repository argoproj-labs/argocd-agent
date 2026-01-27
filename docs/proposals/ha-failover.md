## ArgoCD Agent HA Failover Design

_ai disclosure: diagrams and tables were formatted/generatd with ai_

## Overview

This document outlines the High Availability (HA) failover strategy for the argocd-agent principal component, enabling cross-region disaster recovery.

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Recovery Target** | 1-5 minutes | Acceptable for DR scenario, enables simpler DNS-based failover |
| **State Replication** | Principal-to-Principal streaming | Uses existing event streaming mechanisms, Replica stays in sync |
| **Agent Connectivity** | Single GSLB endpoint | Transparent to agents, no agent code changes |

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Global DNS / GSLB                                 │
│                      principal.argocd.example.com                           │
│                                                                             │
│   Health Checks: /healthz on each principal                                 │
│   Failover: Route to Region B when Region A unhealthy                       │
└─────────────────────┬───────────────────────────────┬───────────────────────┘
                      │                               │
                      ▼                               ▼
    ┌─────────────────────────────────────┐   ┌─────────────────────────────────┐
    │         REGION A (Primary)          │   │       REGION B (Replica)        │
    │                                     │   │                                 │
    │  ┌───────────────────────────┐      │   │  ┌───────────────────────────┐  │
    │  │     Principal Server      │      │   │  │     Principal Server      │  │
    │  │                           │      │   │  │                           │  │
    │  │  - gRPC :8403             │◄─────┼───┼──│  - Replication Client     │  │
    │  │  - Health :8080/healthz   │      │   │  │  - gRPC :8403             │  │
    │  │  - In-memory state        │──────┼───┼─►│  - Mirrors all state      │  │
    │  │  - Replication Server     │      │   │  │  - Health :8080/healthz   │  │
    │  └─────────────┬─────────────┘      │   │  └─────────────┬─────────────┘  │
    │                │                    │   │                │                │
    │  ┌─────────────▼─────────────┐      │   │  ┌─────────────▼─────────────┐  │
    │  │     ArgoCD Instance       │      │   │  │     ArgoCD Instance       │  │
    │  │  (Source of truth)        │      │   │  │  (Standby - receives      │  │
    │  │                           │      │   │  │   replicated state)       │  │
    │  └───────────────────────────┘      │   │  └───────────────────────────┘  │
    │                                     │   │                                 │
    └─────────────────────────────────────┘   └─────────────────────────────────┘
                      ▲                               ▲
                      │                               │
                      │    Replication Stream:        │
                      │    All events forwarded       │
                      │    Primary → Replica          │
                      │                               │
                      │    On failover:               │
                      │    Agents reconnect to        │
                      │    Replica (via GSLB)         │
                      │                               │
              ┌───────┴───────────────────────────────┴───────┐
              │                                               │
              │              Remote Clusters                  │
              │                                               │
              │    ┌─────────┐  ┌─────────┐  ┌─────────┐      │
              │    │ Agent 1 │  │ Agent 2 │  │ Agent N │      │
              │    └─────────┘  └─────────┘  └─────────┘      │
              │                                               │
              └───────────────────────────────────────────────┘
```

---

## Principal-to-Principal Replication

Minimizing data loss during failover is achieved by leveraging **continuous state replication** from Primary to Replica using the existing event streaming mechanisms.

We could also consider enabling linearizable writes / ensuring a write is propagated to the replica before streaming to agents.

### Replication Model

The Replica principal runs a **Replication Client** that connects to the Primary principal as a special peer. Unlike regular agents (which are namespace-scoped), the replication peer receives **ALL events** across all agents.

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         PRIMARY PRINCIPAL                               │
│                                                                         │
│  ┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐    │
│  │   Agent 1       │     │   Agent 2       │     │   Agent N       │    │
│  │   Queue Pair    │     │   Queue Pair    │     │   Queue Pair    │    │
│  └────────┬────────┘     └────────┬────────┘     └────────┬────────┘    │
│           │                       │                       │             │
│           └───────────────────────┼───────────────────────┘             │
│                                   │                                     │
│                                   ▼                                     │
│                    ┌──────────────────────────┐                         │
│                    │   Replication Forwarder  │                         │
│                    │   (Fans out all events)  │                         │
│                    └────────────┬─────────────┘                         │
│                                 │                                       │
└─────────────────────────────────┼───────────────────────────────────────┘
                                  │
                                  │ EventStream (all events, tagged with agent)
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         REPLICA PRINCIPAL                               │
│                                                                         │
│                    ┌──────────────────────────┐                         │
│                    │   Replication Client     │                         │
│                    │   (Receives all events)  │                         │
│                    └────────────┬─────────────┘                         │
│                                 │                                       │
│           ┌─────────────────────┼─────────────────────┐                 │
│           │                     │                     │                 │
│           ▼                     ▼                     ▼                 │
│  ┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐       │
│  │   Agent 1       │   │   Agent 2       │   │   Agent N       │        │
│  │   Shadow State  │   │   Shadow State  │   │   Shadow State  │        │
│  │   + Queue       │   │   + Queue       │   │   + Queue       │       │
│  └─────────────────┘   └─────────────────┘   └─────────────────┘       │
│                                                                         │
│  (Ready to serve agents immediately on failover - no resync needed)     │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### What Gets Replicated

| Data Type | Source | Replication Method |
|-----------|--------|-------------------|
| **Applications** | ArgoCD informers → Principal | Event stream (Create/Update/Delete) |
| **AppProjects** | ArgoCD informers → Principal | Event stream (Create/Update/Delete) |
| **Repositories** | ArgoCD secrets → Principal | Event stream (Create/Update/Delete) |
| **Agent Connections** | Principal connection state | Metadata in replication stream |
| **Resource Checksums** | `resources.AgentResources` | Periodic snapshot or event-driven |
| **Queue State** | `queue.SendRecvQueues` | Events replicated as they're queued |

### Event Types Replicated

Events flow in **both directions** between Primary and agents. The replica needs visibility into both:

**Outbound Events (Principal → Agent)** - Managed Mode:
| Event Type | Description |
|------------|-------------|
| `Create` | New Application/AppProject created in ArgoCD |
| `Update` | Resource fully updated |
| `Delete` | Resource deleted |
| `SpecUpdate` | Spec-only change pushed to agent |
| `RequestResourceResync` | Principal requests agent to resync |

**Inbound Events (Agent → Principal)** - Both Modes:
| Event Type | Description |
|------------|-------------|
| `StatusUpdate` | Agent reports Application status |
| `OperationUpdate` | Sync operation progress |
| `Create/Update/Delete` | Autonomous mode - agent is source of truth |
| `RequestSyncedResourceList` | Agent initiates resync |
| `ResponseSyncedResource` | Agent sends resource during resync |

### Replication Wire Format

Extend existing CloudEvents with replication metadata:

```go
// Replication wrapper for events
type ReplicatedEvent struct {
    // Original event
    Event *cloudevents.Event `json:"event"`

    // Agent this event is associated with
    AgentName string `json:"agentName"`

    // Direction: "inbound" (agent→principal) or "outbound" (principal→agent)
    Direction string `json:"direction"`

    // Sequence number for ordering
    SequenceNum uint64 `json:"sequenceNum"`

    // Timestamp when event was processed by primary
    ProcessedAt time.Time `json:"processedAt"`
}
```

---

## Implementation Components

### 1. Replication Service (New Proto Definition)

**File**: `principal/apis/replication/replication.proto`

```protobuf
syntax = "proto3";

option go_package = "github.com/argoproj-labs/argocd-agent/pkg/api/grpc/replicationapi";

package replicationapi;

import "github.com/cloudevents/sdk-go/binding/format/protobuf/v2/pb/cloudevent.proto";

// ReplicatedEvent wraps an event with replication metadata
message ReplicatedEvent {
    io.cloudevents.v1.CloudEvent event = 1;
    string agent_name = 2;
    string direction = 3;  // "inbound" or "outbound"
    uint64 sequence_num = 4;
    int64 processed_at_unix = 5;
}

// ReplicationSnapshot for initial sync
message ReplicationSnapshot {
    repeated AgentState agents = 1;
    uint64 last_sequence_num = 2;
}

message AgentState {
    string name = 1;
    string mode = 2;  // "autonomous" or "managed"
    bool connected = 3;
    repeated Resource resources = 4;
}

message Resource {
    string name = 1;
    string namespace = 2;
    string kind = 3;
    string uid = 4;
    bytes spec_checksum = 5;
}

// ACK from replica
message ReplicationAck {
    uint64 acked_sequence_num = 1;
}

// Health/status
message ReplicationStatus {
    uint64 current_sequence_num = 1;
    int32 pending_events = 2;
    int64 last_event_unix = 3;
}

service Replication {
    // Subscribe to replication stream (replica calls this on primary)
    rpc Subscribe(stream ReplicationAck) returns (stream ReplicatedEvent);

    // Get initial snapshot (replica calls on connect)
    rpc GetSnapshot(SnapshotRequest) returns (ReplicationSnapshot);

    // Get replication status
    rpc Status(StatusRequest) returns (ReplicationStatus);
}

message SnapshotRequest {
    // If set, only get events after this sequence number
    uint64 since_sequence_num = 1;
}

message StatusRequest {}
```

---

## HA State Machine

Each principal runs an **HA Controller** that manages state transitions.

### States

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              STATE MACHINE                                  │
│                                                                             │
│   ┌──────────────┐      replication         ┌──────────────┐               │
│   │              │      stream breaks       │              │               │
│   │  REPLICATING ├─────────────────────────►│ DISCONNECTED │               │
│   │              │                          │              │               │
│   └──────┬───────┘                          └──────┬───────┘               │
│          │                                         │                        │
│          │ replication                             │ failover timeout       │
│          │ stream                                  │ (30s) + peer           │
│          │ reconnects                              │ unreachable            │
│          │                                         ▼                        │
│          │                                  ┌──────────────┐               │
│          │                                  │              │               │
│          └──────────────────────────────────┤   STANDBY    │               │
│                                             │              │               │
│                                             └──────┬───────┘               │
│                                                    │                        │
│                                                    │ first agent connects   │
│                                                    │ (GSLB routed here)     │
│                                                    ▼                        │
│                                             ┌──────────────┐               │
│                                             │              │               │
│                                             │    ACTIVE    │               │
│                                             │              │               │
│                                             └──────────────┘               │
│                                                                             │
│  Additional states for recovery:                                            │
│                                                                             │
│   ┌──────────────┐      peer is active      ┌──────────────┐               │
│   │              │      (I was down)        │              │               │
│   │  RECOVERING  ├─────────────────────────►│   SYNCING    │               │
│   │  (startup)   │                          │              │               │
│   └──────────────┘                          └──────┬───────┘               │
│                                                    │                        │
│                                                    │ caught up              │
│                                                    ▼                        │
│                                             ┌──────────────┐               │
│                                             │   STANDBY    │               │
│                                             │ (awaiting    │               │
│                                             │  failback)   │               │
│                                             └──────────────┘               │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Health Check by State

| State | `/healthz` | `/readyz` | Effect |
|-------|------------|-----------|--------|
| REPLICATING | UNHEALTHY | UNHEALTHY | GSLB won't route agents here |
| DISCONNECTED | UNHEALTHY | UNHEALTHY | Waiting for timeout, don't accept traffic |
| STANDBY | **HEALTHY** | **HEALTHY** | Ready! GSLB can route agents here |
| ACTIVE | **HEALTHY** | **HEALTHY** | Serving agents |
| RECOVERING | UNHEALTHY | UNHEALTHY | Just started, determining role |
| SYNCING | UNHEALTHY | UNHEALTHY | Catching up to current primary |

### Agent Connection Handling by State

| State | Agent Connects | Behavior |
|-------|----------------|----------|
| REPLICATING | Reject | Return gRPC error: `UNAVAILABLE: not primary` |
| DISCONNECTED | Reject | Return gRPC error: `UNAVAILABLE: not primary` |
| STANDBY | Accept | Transition to ACTIVE, serve agent |
| ACTIVE | Accept | Normal operation |
| RECOVERING | Reject | Return gRPC error: `UNAVAILABLE: recovering` |
| SYNCING | Reject | Return gRPC error: `UNAVAILABLE: syncing` |

---

## Failover Sequence (Primary Failure)

### Timeline

```
Time    Region A (Primary)          Region B (Replica)           GSLB
────────────────────────────────────────────────────────────────────────────
T+0     💀 Dies                     State: REPLICATING           A: was HEALTHY
                                    Detects stream break          B: UNHEALTHY
                                    State → DISCONNECTED
                                    Starts 30s timer

T+10s   (still down)                State: DISCONNECTED          A: UNHEALTHY
                                    Timer: 20s remaining          B: UNHEALTHY

T+30s   (still down)                Timer expires                 A: UNHEALTHY
                                    Checks peer: unreachable      B: UNHEALTHY
                                    State → STANDBY
                                    Health → HEALTHY              B: HEALTHY ✓

T+45s   (still down)                State: STANDBY               Routes to B!
                                    Ready for agents

T+60s   (still down)                DNS TTL expires              DNS → Region B
                                    First agent connects
                                    State → ACTIVE

T+90s                               Serving all agents           Stable
                                    Accepting replication
                                    (if old primary recovers)
```

### What Happens on Agent Reconnection

Because the Replica has been receiving replicated events:

1. **Agent authenticates** - Same credentials work (shared CA)
2. **Replica checks state** - Already knows this agent's resources from replication
3. **Quick checksum verification** - Confirm replica's view matches agent's
4. **Minimal delta sync** - Only sync events that were in-flight during failover

**Key benefit**: No full resync required. RTO is primarily DNS propagation + failover timeout.

---

## Failback Sequence (Manual)

When Region A recovers, it must sync from Region B before becoming primary again.

### Timeline

```
Time    Region A (Old Primary)      Region B (Current Primary)   Operator
────────────────────────────────────────────────────────────────────────────
T+0     Comes back online           State: ACTIVE                Monitoring
        State: RECOVERING           Serving all agents
        Checks peer status...
        Peer is ACTIVE!
        State → SYNCING
        Connects to B as replica

T+30s   Receiving replication       Forwarding events to A       Gets alert:
        State: SYNCING              Continues serving agents     "Region A online"

T+2m    Caught up!                  (still serving)              Sees A synced
        State: STANDBY
        Health: UNHEALTHY
        (waiting for operator)

T+5m    (waiting)                   (still serving)              Decides to
                                                                 failback

T+5m    Receives: "become primary"  Receives: "become replica"   Runs:
+1s     State → ACTIVE              State → REPLICATING          $ argocd-agent
        Health → HEALTHY            Health → UNHEALTHY             ha failback
        Starts replication server   Connects to A as replica

T+6m    GSLB routes to A            Agents reconnect to A        Done ✓
        Serving all agents          Receiving replication
```

### Failback Command

Failback could be assisted by argocd-agent cli

```bash
# Check current HA status
$ argocd-agent ha status
Region A (principal.region-a.internal):
  State: STANDBY
  Role: preferred-primary
  Replication: synced (lag: 0s)
  Last event: 2024-01-15T10:30:00Z

Region B (principal.region-b.internal):
  State: ACTIVE
  Role: preferred-replica
  Connected agents: 47
  Uptime: 2h 15m

# Trigger failback (requires confirmation)
$ argocd-agent ha failback --to region-a

WARNING: This will:
  1. Promote Region A to primary
  2. Demote Region B to replica
  3. Cause all 47 agents to reconnect

Region A replication status: SYNCED (lag: 0s)

Proceed? [y/N]: y

[1/3] Signaling Region B to become replica... done
[2/3] Signaling Region A to become primary... done
[3/3] Waiting for agents to reconnect... 47/47 connected

Failback complete. Region A is now primary.

NOTE: Update GSLB health check priorities if using manual DNS failover.
```

---

## HA Controller Implementation

### Configuration

```yaml
# Region A - Preferred Primary
principal:
  ha:
    enabled: true
    preferredRole: primary
    peerAddress: principal.region-b.internal:8404
    failoverTimeout: 30s      # Time to wait before promoting

# Region B - Preferred Replica
principal:
  ha:
    enabled: true
    preferredRole: replica
    peerAddress: principal.region-a.internal:8404
    failoverTimeout: 30s
```
---

## Configuration

### Principal Configuration

The HA configuration is vendor-agnostic. The same principal binary can run as primary or replica based on configuration.

```yaml
# values-region-a.yaml (Preferred Primary)
principal:
  ha:
    enabled: true
    preferredRole: primary              # "primary" or "replica"
    peerAddress: principal.region-b.internal:8404  # Direct peer address (not GSLB)
    failoverTimeout: 30s                # Time to wait before auto-promoting

  replication:
    port: 8404                          # Separate port for replication traffic
    tls:
      certFile: /etc/argocd-agent/replication/tls.crt
      keyFile: /etc/argocd-agent/replication/tls.key
      caFile: /etc/argocd-agent/replication/ca.crt

  # Health endpoint for GSLB (any vendor)
  health:
    port: 8080
    livenessPath: /healthz
    readinessPath: /readyz
```

```yaml
# values-region-b.yaml (Preferred Replica)
principal:
  ha:
    enabled: true
    preferredRole: replica
    peerAddress: principal.region-a.internal:8404
    failoverTimeout: 30s

  replication:
    port: 8404
    tls:
      certFile: /etc/argocd-agent/replication/tls.crt
      keyFile: /etc/argocd-agent/replication/tls.key
      caFile: /etc/argocd-agent/replication/ca.crt

  health:
    port: 8080
    livenessPath: /healthz
    readinessPath: /readyz
```

### Agent Configuration (Unchanged)

Agents connect to a single DNS endpoint. The GSLB/DNS layer handles routing.

```yaml
# agent-config.yaml - NO CHANGES NEEDED
agent:
  remote:
    # Single endpoint - GSLB routes to healthy principal
    address: principal.argocd.example.com:8403
```

---

## GSLB / Global Load Balancer Setup

The HA design requires a Global Server Load Balancer (GSLB) or DNS-based failover to route agents to the healthy principal.

### Requirements

| Requirement | Description |
|-------------|-------------|
| Health checks | Must poll `/healthz` endpoint on each principal |
| Failover routing | Route traffic to healthy endpoint when primary fails |
| DNS TTL | Configurable, recommend 60s for reasonable RTO |
| Single endpoint | Agents connect to one DNS name |

### Example: AWS Route53 (Reference Implementation)

```yaml
# AWS Route53 Health Checks + Failover Routing
# This is one possible implementation - adapt for your GSLB provider

HostedZone: argocd.example.com

HealthChecks:
  - Id: region-a-health
    Type: HTTPS
    FullyQualifiedDomainName: principal.region-a.internal
    Port: 8080
    ResourcePath: /healthz
    RequestInterval: 10        # Check every 10s
    FailureThreshold: 3        # 3 failures = unhealthy

  - Id: region-b-health
    Type: HTTPS
    FullyQualifiedDomainName: principal.region-b.internal
    Port: 8080
    ResourcePath: /healthz
    RequestInterval: 10
    FailureThreshold: 3

Records:
  # Primary record (Region A)
  - Name: principal.argocd.example.com
    Type: A
    SetIdentifier: region-a-primary
    Failover: PRIMARY
    HealthCheckId: region-a-health
    TTL: 60
    ResourceRecords:
      - <region-a-lb-ip>

  # Failover record (Region B)
  - Name: principal.argocd.example.com
    Type: A
    SetIdentifier: region-b-secondary
    Failover: SECONDARY
    HealthCheckId: region-b-health
    TTL: 60
    ResourceRecords:
      - <region-b-lb-ip>
```

### Example: Generic DNS Provider

For providers without native GSLB, use external health checkers:

```yaml
# Using external-dns with health check annotations
# Or any DNS provider with API access

# Health check script (runs on monitoring system)
healthcheck:
  targets:
    - name: region-a
      endpoint: https://principal.region-a.internal:8080/healthz
      interval: 10s
      threshold: 3
    - name: region-b
      endpoint: https://principal.region-b.internal:8080/healthz
      interval: 10s
      threshold: 3

  dns:
    record: principal.argocd.example.com
    ttl: 60
    # Update DNS to point to healthy region
    onFailure:
      action: update-dns
      target: healthy-region
```

### Example: Kubernetes + External DNS

```yaml
# If using Kubernetes in both regions with external-dns
apiVersion: v1
kind: Service
metadata:
  name: principal
  annotations:
    external-dns.alpha.kubernetes.io/hostname: principal.argocd.example.com
    external-dns.alpha.kubernetes.io/ttl: "60"
spec:
  type: LoadBalancer
  ports:
    - port: 8403
      name: grpc
    - port: 8080
      name: health
  selector:
    app: argocd-agent-principal
```

---

## Operational Considerations

### Replication Lag Monitoring

### Split-Brain Prevention

The replication model inherently prevents split-brain:

1. **Replica knows it's a replica** - Won't accept agents while replication stream is healthy
2. **Failover requires replication stream break** - Only then does replica become active
3. **GSLB is authoritative** - Agents only connect to whoever GSLB points to

---


## Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Replication backpressure | Metrics only, no action | Keep simple for v1; monitor and tune buffer sizes |
| Secrets handling | Operator configures manually | Avoid replicating sensitive data; use external secret management |
| Failback mode | Manual only | Prevents flapping, gives operator control |
| Split-brain prevention | Health check + state machine | Replica only healthy in STANDBY/ACTIVE; rejects agents otherwise |
| Failover timeout | 30 seconds (configurable) | Balance between avoiding flapping and minimizing RTO |

---

## Open Questions (Future Enhancements)

1. **Multi-Replica**: Should we support multiple replicas for read scaling or additional redundancy? #186 should be the main design for horizontal scaling

2. **Automatic Failback**: Add option to automatically failback after N minutes if preferred primary is synced and healthy? Personally think manual failback is easiest for now

3. **Partial Failures**: What if primary is reachable for replication but not for agents (network partition)?

---

## References

- [argocd-agent Architecture](../README.md)
- [Event System](../internal/event/event.go)
- [Queue Implementation](../internal/queue/queue.go)
- [Resync Protocol](../internal/resync/resync.go)


