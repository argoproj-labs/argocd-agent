## ArgoCD Agent HA Failover Design

_ai disclosure: diagrams and tables were formatted/generated with ai_

## Overview

This document outlines the High Availability (HA) failover strategy for the argocd-agent principal component, enabling cross-region disaster recovery.

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Recovery Target** | 1-5 minutes | Acceptable for DR scenario, enables simpler DNS-based failover |
| **State Replication** | Principal-to-Principal streaming | Uses existing event streaming mechanisms, Replica stays in sync |
| **Agent Connectivity** | Single GSLB endpoint | Transparent to agents, minimal agent code changes |
| **Split-Brain Prevention** | Self-fencing on lost peer ACK | Primary must prove replica is listening; no external quorum needed |
| **Consistency vs Availability** | Safety over availability | Accept brief outage during some partitions to prevent split-brain |

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
    │  │  - Term: N                │◄─ACK─┼───┼──│  - Term: N                │  │
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
                      │    - Events + Heartbeats      │
                      │    - Bidirectional ACKs       │
                      │    - Term synchronization     │
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

## Split-Brain Prevention

### The Problem

With only two principals and no external witness, network partitions can cause both principals to believe they should be active (split-brain). DNS/GSLB alone cannot prevent this—it routes traffic but cannot fence an already-active principal.

### Solution: Self-Fencing on Lost Peer ACK

**Core rule:** The primary must continuously prove the replica is listening. If the primary stops receiving ACKs, it must fence itself.

```
PR1 (primary):
  - Sends heartbeats every 5s on replication stream
  - Expects ACKs from PR2
  - No ACK for ackTimeout (15s) → check peer health
  - If peer unreachable → peer likely dead, stay ACTIVE
  - If peer reachable but not ACKing → partition, fence self

PR2 (replica):
  - No heartbeat/events for failoverTimeout (30s) → promote to STANDBY
  - Trusts that PR1 will self-fence if partitioned
```

This guarantees that in asymmetric partition scenarios:
- PR2 may promote
- PR1 will fence itself
- Only one principal can serve at any time

**Trade-off:** In some partitions the system may become temporarily unavailable, but it remains correct.

### Term/Epoch for Stale Rejection

To prevent stale leaders from corrupting state, we use a monotonically increasing term:

- Each ACTIVE promotion increments the term
- Agent connections and inbound events carry the term
- A principal only accepts inbound updates for its current term

```go
type HAState struct {
    Role        Role   // ACTIVE, STANDBY, FENCED, etc.
    Term        uint64 // Incremented on each promotion
    LastPeerAck time.Time
}

func (s *HAState) Promote() {
    s.Term++
    s.Role = RoleActive
}

func (s *HAState) ValidateInbound(term uint64) error {
    if term != s.Term {
        return status.Error(codes.FailedPrecondition, "stale term")
    }
    return nil
}
```

### Role of DNS/GSLB

DNS failover (Route53, etc.) is **necessary for traffic steering** but **not sufficient for split-brain prevention**:

| What DNS Does | What DNS Doesn't Do |
|---------------|---------------------|
| Health check principals | Fence active principals |
| Route new connections | Terminate existing connections |
| Failover traffic | Prevent dual-active |

In this design:
- **Self-fencing = correctness** (prevents split-brain)
- **DNS/GSLB = traffic steering** (routes agents to healthy principal)

---

## Principal-to-Principal Replication

Minimizing data loss during failover is achieved by leveraging **continuous state replication** from Primary to Replica using the existing event streaming mechanisms.

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
                                  │ EventStream (events + heartbeats)
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         REPLICA PRINCIPAL                               │
│                                                                         │
│                    ┌──────────────────────────┐                         │
│                    │   Replication Client     │                         │
│                    │   (Receives all events,  │                         │
│                    │    sends ACKs)           │                         │
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
| **Term** | HA Controller | Included in heartbeats |

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
type ReplicatedEvent struct {
    Event       *cloudevents.Event `json:"event"`
    AgentName   string             `json:"agentName"`
    Direction   string             `json:"direction"` // "inbound" or "outbound"
    SequenceNum uint64             `json:"sequenceNum"`
    ProcessedAt time.Time          `json:"processedAt"`
    Term        uint64             `json:"term"`
}
```

---

## Implementation Components

### 1. Replication Service (Proto Definition)

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
    uint64 term = 6;
}

// Heartbeat sent by primary to replica
message ReplicationHeartbeat {
    uint64 sequence_num = 1;
    int64 timestamp_unix = 2;
    uint64 term = 3;
}

// ACK from replica to primary (critical for split-brain prevention)
message ReplicationAck {
    uint64 acked_sequence_num = 1;
    uint64 term = 2;
}

// ReplicationSnapshot for initial sync
message ReplicationSnapshot {
    repeated AgentState agents = 1;
    uint64 last_sequence_num = 2;
    uint64 term = 3;
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

// Health/status
message ReplicationStatus {
    uint64 current_sequence_num = 1;
    int32 pending_events = 2;
    int64 last_event_unix = 3;
    uint64 term = 4;
    string state = 5;  // HA state: ACTIVE, REPLICATING, FENCED, etc.
}

// Stream message can be either event or heartbeat
message ReplicationStreamMessage {
    oneof payload {
        ReplicatedEvent event = 1;
        ReplicationHeartbeat heartbeat = 2;
    }
}

service Replication {
    // Subscribe to replication stream (replica calls this on primary)
    // Bidirectional: primary sends events/heartbeats, replica sends ACKs
    rpc Subscribe(stream ReplicationAck) returns (stream ReplicationStreamMessage);

    // Get initial snapshot (replica calls on connect)
    rpc GetSnapshot(SnapshotRequest) returns (ReplicationSnapshot);

    // Get replication status
    rpc Status(StatusRequest) returns (ReplicationStatus);
}

message SnapshotRequest {
    uint64 since_sequence_num = 1;
}

message StatusRequest {}
```

### 2. HA Controller

```go
type HAController struct {
    mu sync.RWMutex

    state           HAState
    term            uint64
    lastPeerAck     time.Time
    peerAddress     string
    preferredRole   string
    gslbHostname    string

    // Timeouts
    ackTimeout       time.Duration // Time without ACKs before considering fence (15s)
    heartbeatInterval time.Duration // Heartbeat frequency (5s)
    failoverTimeout  time.Duration // Time before replica promotes (30s)
    recoveryTimeout  time.Duration // Time before unfencing after peer confirmed dead (60s)

    // Optional external witness
    trafficAuthority TrafficAuthority
}

type HAState string

const (
    StateRecovering  HAState = "RECOVERING"   // Just started, determining role
    StateActive      HAState = "ACTIVE"       // Serving agents, sending replication
    StateReplicating HAState = "REPLICATING"  // Receiving replication, not serving
    StateDisconnected HAState = "DISCONNECTED" // Lost replication, waiting
    StateStandby     HAState = "STANDBY"      // Ready to serve, waiting for agents
    StateSyncing     HAState = "SYNCING"      // Catching up to current primary
    StateFenced      HAState = "FENCED"       // Self-fenced, not serving
)
```

---

## HA State Machine

Each principal runs an **HA Controller** that manages state transitions.

### States

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                              STATE MACHINE                                        │
│                                                                                   │
│  ┌──────────────┐                                                                 │
│  │  RECOVERING  │ (startup)                                                       │
│  └──────┬───────┘                                                                 │
│         │                                                                         │
│         ├─── peer ACTIVE ──────────────────────► SYNCING ───► STANDBY            │
│         │                                                                         │
│         └─── peer unreachable/down ────────────► ACTIVE (if preferred-primary)   │
│                                                  STANDBY (if preferred-replica)  │
│                                                                                   │
│                                                                                   │
│  PRIMARY PATH:                                                                    │
│  ─────────────                                                                    │
│  ┌──────────────┐     no ACKs for        ┌──────────────┐                        │
│  │              │     ackTimeout         │              │                        │
│  │    ACTIVE    ├───────────────────────►│    FENCED    │                        │
│  │              │     + peer reachable   │              │                        │
│  └──────────────┘                        └──────┬───────┘                        │
│         ▲                                       │                                 │
│         │                                       │ peer reconnects                 │
│         │ unfence (peer confirmed dead         │ as new primary                  │
│         │ + GSLB routes to me)                 ▼                                 │
│         │                                ┌──────────────┐                        │
│         └────────────────────────────────┤   SYNCING    │                        │
│                                          └──────────────┘                        │
│                                                                                   │
│                                                                                   │
│  REPLICA PATH:                                                                    │
│  ────────────                                                                     │
│  ┌──────────────┐    replication         ┌──────────────┐                        │
│  │              │    stream breaks       │              │                        │
│  │  REPLICATING ├───────────────────────►│ DISCONNECTED │                        │
│  │              │                        │              │                        │
│  └──────┬───────┘                        └──────┬───────┘                        │
│         │                                       │                                 │
│         │ replication                           │ failoverTimeout                 │
│         │ reconnects                            │ + peer unreachable              │
│         │                                       ▼                                 │
│         │                                ┌──────────────┐                        │
│         │                                │              │                        │
│         └────────────────────────────────┤   STANDBY    │                        │
│                                          │              │                        │
│                                          └──────┬───────┘                        │
│                                                 │                                 │
│                                                 │ first agent connects            │
│                                                 │ (term++)                        │
│                                                 ▼                                 │
│                                          ┌──────────────┐                        │
│                                          │              │                        │
│                                          │    ACTIVE    │                        │
│                                          │              │                        │
│                                          └──────────────┘                        │
│                                                                                   │
└──────────────────────────────────────────────────────────────────────────────────┘
```

### Health Check by State

| State | `/healthz` | `/readyz` | Effect |
|-------|------------|-----------|--------|
| RECOVERING | UNHEALTHY | UNHEALTHY | Just started, determining role |
| ACTIVE | **HEALTHY** | **HEALTHY** | Serving agents |
| REPLICATING | UNHEALTHY | UNHEALTHY | GSLB won't route agents here |
| DISCONNECTED | UNHEALTHY | UNHEALTHY | Waiting for timeout |
| STANDBY | **HEALTHY** | **HEALTHY** | Ready! GSLB can route agents here |
| SYNCING | UNHEALTHY | UNHEALTHY | Catching up to current primary |
| FENCED | UNHEALTHY | UNHEALTHY | Self-fenced, rejecting all traffic |

### Agent Connection Handling by State

| State | Agent Connects | Behavior |
|-------|----------------|----------|
| RECOVERING | Reject | `UNAVAILABLE: recovering` |
| ACTIVE | Accept | Normal operation, validate term |
| REPLICATING | Reject | `UNAVAILABLE: not primary` |
| DISCONNECTED | Reject | `UNAVAILABLE: not primary` |
| STANDBY | Accept | Transition to ACTIVE (term++), serve agent |
| SYNCING | Reject | `UNAVAILABLE: syncing` |
| FENCED | Reject | `UNAVAILABLE: fenced` + terminate existing |

---

## Self-Fencing Logic

### Primary Fencing Decision

```go
func (c *HAController) checkFencing() {
    c.mu.Lock()
    defer c.mu.Unlock()

    if c.state != StateActive {
        return
    }

    if time.Since(c.lastPeerAck) <= c.ackTimeout {
        return // ACKs are fresh, all good
    }

    // No ACKs for ackTimeout - determine cause
    peerHealth := c.probePeerHealth()

    if peerHealth == PeerUnreachable {
        // Peer is likely dead, not partitioned - stay active
        log.Warn("peer unreachable but no ACKs, assuming peer dead")
        return
    }

    // Peer is reachable but not ACKing - this is a partition
    // We must fence to prevent split-brain
    log.Warn("peer reachable but not ACKing, self-fencing")
    c.transitionTo(StateFenced)
    c.terminateAllAgentSessions()
}
```

### Fenced Recovery

```go
func (c *HAController) attemptRecovery() {
    // Called periodically while in FENCED state

    c.mu.Lock()
    defer c.mu.Unlock()

    if c.state != StateFenced {
        return
    }

    // Check if peer is still unreachable
    peerHealth := c.probePeerHealth()

    if peerHealth == PeerActive {
        // Peer took over, become replica
        c.transitionTo(StateSyncing)
        return
    }

    if peerHealth == PeerUnreachable {
        // Peer still gone - check if we should unfence
        if c.shouldUnfence() {
            log.Info("peer confirmed dead, unfencing")
            c.transitionTo(StateActive)
        }
    }
}

func (c *HAController) shouldUnfence() bool {
    // Conditions to unfence:
    // 1. Peer still unreachable after recoveryTimeout
    // 2. GSLB has no healthy target OR points to us

    if time.Since(c.fencedAt) < c.recoveryTimeout {
        return false
    }

    // Check GSLB - are we the only option?
    return c.gslbResolvesToMe() || c.gslbHasNoHealthyTarget()
}

func (c *HAController) gslbResolvesToMe() bool {
    ips, err := net.LookupIP(c.gslbHostname)
    if err != nil {
        return false
    }
    for _, ip := range ips {
        if ip.Equal(c.myIP) {
            return true
        }
    }
    return false
}
```

---

## Failure Scenarios

### Scenario 1: Replica Crashes (Clean Failure)

```
Time    PR1 (primary)              PR2 (replica)         GSLB
─────────────────────────────────────────────────────────────────
T+0     Serving agents             💀 Dies               PR1 HEALTHY
T+5s    Heartbeat, no ACK          -                     PR1 HEALTHY
T+15s   No ACKs for ackTimeout     -
        Probes peer: unreachable
        → Stays ACTIVE             -                     PR1 HEALTHY
        (peer dead, not partition)

Result: PR1 continues serving. No outage.
```

### Scenario 2: Primary Crashes (Clean Failure)

```
Time    PR1 (primary)              PR2 (replica)         GSLB
─────────────────────────────────────────────────────────────────
T+0     💀 Dies                    Replication breaks    PR1 was HEALTHY
                                   → DISCONNECTED        PR2 UNHEALTHY
T+30s   -                          failoverTimeout
                                   Probes PR1: unreachable
                                   → STANDBY             PR2 HEALTHY
T+45s   -                          GSLB routes here
                                   Agent connects
                                   → ACTIVE (term++)     PR2 HEALTHY

Result: ~45s RTO. Clean failover.
```

### Scenario 3: Asymmetric Partition (PR1 → PR2 Broken)

```
Time    PR1 (primary)              PR2 (replica)         GSLB
─────────────────────────────────────────────────────────────────
T+0     Can't reach PR2            Replication breaks    PR1 HEALTHY
        Heartbeats fail            → DISCONNECTED        PR2 UNHEALTHY
T+15s   No ACKs for ackTimeout
        Probes PR2: unreachable
        (same network path)
        → Stays ACTIVE                                   PR1 HEALTHY
T+30s   -                          failoverTimeout
                                   Probes PR1: ???

        If PR2 CAN reach PR1:
          PR2 sees PR1 is ACTIVE
          → Stay DISCONNECTED, retry replication
          No split-brain ✓

        If PR2 CANNOT reach PR1:
          → STANDBY                                      PR2 HEALTHY
          Both healthy! GSLB decides.
          Route53 PRIMARY/SECONDARY → PR1 wins
```

### Scenario 4: Full Partition (Neither Can Reach Other)

```
Time    PR1 (primary)              PR2 (replica)         GSLB
─────────────────────────────────────────────────────────────────
T+0     Can't reach PR2            Can't reach PR1       PR1 HEALTHY
T+15s   No ACKs, probes fail
        → Stays ACTIVE             -                     PR1 HEALTHY
        (thinks PR2 dead)
T+30s   -                          failoverTimeout
                                   Probes PR1: fail
                                   → STANDBY             PR2 HEALTHY

        Both HEALTHY! GSLB arbitrates.

        Route53 PRIMARY/SECONDARY:
        → Always returns PR1 (PRIMARY wins)
        → Agents go to PR1
        → PR2 sits idle in STANDBY

        When partition heals:
        → PR2 sees PR1 is ACTIVE
        → PR2 reconnects as replica
```

### Scenario 5: Network Blip (Transient)

```
Time    PR1 (primary)              PR2 (replica)
─────────────────────────────────────────────────────────────────
T+0     Network hiccup             Replication pauses
T+5s    Still no ACKs              Waiting...
T+10s   Network recovers
        Receives ACK               Receives heartbeat
        Stays ACTIVE               Stays REPLICATING

Result: No state change. ackTimeout (15s) > typical blip.
```

---

## Agent Behavior

### Changes Required

| Change | Required? | Description |
|--------|-----------|-------------|
| Handle disconnection | Already exists | Agents already reconnect on disconnect |
| Term in handshake | **New** | Send/receive term on connect |
| Handle stale term | **New** | On `FailedPrecondition` → reconnect |

### Agent Handshake Flow

```
Agent                          Principal
─────────────────────────────────────────────
1. Connect to GSLB endpoint
   (DNS resolves to healthy principal)

2. gRPC handshake
   AgentHello{term: <last known or 0>}
                               ──► If term matches: OK
                               ──► If term stale: FailedPrecondition

3. On FailedPrecondition:
   - Clear cached term
   - Reconnect (get new term from principal)

4. On disconnect (principal fenced/failed):
   - Backoff + retry
   - DNS may resolve to different principal
   - New handshake gets new term
```

### Agent Configuration (Minimal Changes)

```yaml
# agent-config.yaml
agent:
  remote:
    address: principal.argocd.example.com:8403  # GSLB endpoint (unchanged)
  ha:
    termCachePath: /var/lib/argocd-agent/term   # Optional: persist last known term
```

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
                                    Probes peer: unreachable      B: UNHEALTHY
                                    State → STANDBY
                                    Health → HEALTHY              B: HEALTHY ✓

T+45s   (still down)                State: STANDBY               Routes to B!
                                    Ready for agents

T+60s   (still down)                DNS TTL expires              DNS → Region B
                                    First agent connects
                                    Term++ (now term=2)
                                    State → ACTIVE

T+90s                               Serving all agents           Stable
                                    Term: 2
```

### What Happens on Agent Reconnection

Because the Replica has been receiving replicated events:

1. **Agent authenticates** - Same credentials work (shared CA)
2. **Term exchange** - Agent sends last known term, principal sends current term
3. **Replica checks state** - Already knows this agent's resources from replication
4. **Quick checksum verification** - Confirm replica's view matches agent's
5. **Minimal delta sync** - Only sync events that were in-flight during failover

**Key benefit**: No full resync required. RTO is primarily DNS propagation + failover timeout.

---

## Failback Sequence (Manual)

When Region A recovers, it must sync from Region B before becoming primary again.

### Timeline

```
Time    Region A (Old Primary)      Region B (Current Primary)   Operator
────────────────────────────────────────────────────────────────────────────
T+0     Comes back online           State: ACTIVE, term=2        Monitoring
        State: RECOVERING           Serving all agents
        Probes peer...
        Peer is ACTIVE!
        State → SYNCING
        Connects to B as replica

T+30s   Receiving replication       Forwarding events to A       Gets alert:
        State: SYNCING              Continues serving            "Region A online"
        Learning term=2

T+2m    Caught up!                  (still serving)              Sees A synced
        State: STANDBY
        Health: UNHEALTHY
        (waiting for operator)

T+5m    (waiting)                   (still serving)              Decides to
                                                                 failback

T+5m    Receives: "become primary"  Receives: "become replica"   Runs:
+1s     Term++ (now term=3)         State → REPLICATING          $ argocd-agent
        State → ACTIVE              Health → UNHEALTHY             ha failback
        Health → HEALTHY            Connects to A as replica

T+6m    GSLB routes to A            Agents reconnect to A        Done ✓
        Serving all agents          Receiving replication
        Term: 3
```

### Failback Command

```bash
# Check current HA status
$ argocd-agent ha status
Region A (principal.region-a.internal):
  State: STANDBY
  Role: preferred-primary
  Term: 2
  Replication: synced (lag: 0s)
  Last event: 2024-01-15T10:30:00Z

Region B (principal.region-b.internal):
  State: ACTIVE
  Role: preferred-replica
  Term: 2
  Connected agents: 47
  Uptime: 2h 15m

# Trigger failback (requires confirmation)
$ argocd-agent ha failback --to region-a

WARNING: This will:
  1. Promote Region A to primary (term → 3)
  2. Demote Region B to replica
  3. Cause all 47 agents to reconnect

Region A replication status: SYNCED (lag: 0s)

Proceed? [y/N]: y

[1/3] Signaling Region B to become replica... done
[2/3] Signaling Region A to become primary (term=3)... done
[3/3] Waiting for agents to reconnect... 47/47 connected

Failback complete. Region A is now primary (term=3).
```

---

## Configuration

### Principal Configuration

```yaml
# values-region-a.yaml (Preferred Primary)
principal:
  ha:
    enabled: true
    preferredRole: primary
    peerAddress: principal.region-b.internal:8404
    gslbHostname: principal.argocd.example.com  # For unfence detection

    # Timeouts
    heartbeatInterval: 5s    # Send heartbeat every 5s
    ackTimeout: 15s          # Fence if no ACKs for 15s
    failoverTimeout: 30s     # Replica promotes after 30s
    recoveryTimeout: 60s     # Unfence after 60s if peer confirmed dead

    # Optional external witness
    trafficAuthority:
      type: none  # or: aws-arc, consul, k8s-lease

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

```yaml
# values-region-b.yaml (Preferred Replica)
principal:
  ha:
    enabled: true
    preferredRole: replica
    peerAddress: principal.region-a.internal:8404
    gslbHostname: principal.argocd.example.com

    heartbeatInterval: 5s
    ackTimeout: 15s
    failoverTimeout: 30s
    recoveryTimeout: 60s

    trafficAuthority:
      type: none

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

### Important: DNS Role in This Design

DNS/GSLB is **necessary but not sufficient** for split-brain prevention:

- ✅ Routes new connections to healthy principal
- ✅ Provides failover when health checks fail
- ❌ Cannot fence already-active principals
- ❌ Cannot terminate existing gRPC connections
- ❌ Cannot prevent dual-active during partitions

**Self-fencing handles correctness; DNS handles traffic steering.**

### Example: AWS Route53 (Reference Implementation)

```yaml
# AWS Route53 Health Checks + Failover Routing

HostedZone: argocd.example.com

HealthChecks:
  - Id: region-a-health
    Type: HTTPS
    FullyQualifiedDomainName: principal.region-a.internal
    Port: 8080
    ResourcePath: /healthz
    RequestInterval: 10
    FailureThreshold: 3

  - Id: region-b-health
    Type: HTTPS
    FullyQualifiedDomainName: principal.region-b.internal
    Port: 8080
    ResourcePath: /healthz
    RequestInterval: 10
    FailureThreshold: 3

Records:
  # PRIMARY wins when both healthy (prevents dual-active routing)
  - Name: principal.argocd.example.com
    Type: A
    SetIdentifier: region-a-primary
    Failover: PRIMARY
    HealthCheckId: region-a-health
    TTL: 60
    ResourceRecords:
      - <region-a-lb-ip>

  - Name: principal.argocd.example.com
    Type: A
    SetIdentifier: region-b-secondary
    Failover: SECONDARY
    HealthCheckId: region-b-health
    TTL: 60
    ResourceRecords:
      - <region-b-lb-ip>
```

---

## Optional: External Traffic Authority

For environments requiring stronger guarantees, an external traffic authority can act as a third-party witness.

### TrafficAuthority Interface

```go
// Pluggable interface for external witness
type TrafficAuthority interface {
    // Check if this region is authorized to serve
    GetServingState(region string) (TrafficState, error)

    // Request to change serving state (for failback)
    SetServingState(region string, state TrafficState) error
}

type TrafficState string

const (
    TrafficStateOn  TrafficState = "ON"
    TrafficStateOff TrafficState = "OFF"
)
```

### Implementations

| Backend | Use Case |
|---------|----------|
| `NoOpAuthority` | Default - self-fencing only |
| `AWSARCAuthority` | Route53 Application Recovery Controller |
| `ConsulAuthority` | Consul-based distributed lock |
| `K8sLeaseAuthority` | Kubernetes Lease object in multi-AZ control plane |

### AWS Route53 ARC Integration

Route53 ARC provides **routing controls**—explicit on/off switches independent of health checks.

```go
type AWSARCAuthority struct {
    client             *route53recoverycontrolconfig.Client
    routingControlArn  string
    clusterArn         string
}

func (a *AWSARCAuthority) GetServingState(region string) (TrafficState, error) {
    resp, err := a.client.GetRoutingControlState(ctx, &route53recoverycontrolconfig.GetRoutingControlStateInput{
        RoutingControlArn: aws.String(a.routingControlArn),
    })
    if err != nil {
        return TrafficStateOff, err
    }
    if resp.RoutingControlState == types.RoutingControlStateOn {
        return TrafficStateOn, nil
    }
    return TrafficStateOff, nil
}
```

### Using External Authority

When configured, the principal checks both internal state AND external authority:

```go
func (c *HAController) canServeAgents() bool {
    // Must be in serving state internally
    if c.state != StateActive && c.state != StateStandby {
        return false
    }

    // If external authority configured, must also be authorized
    if c.trafficAuthority != nil {
        state, err := c.trafficAuthority.GetServingState(c.region)
        if err != nil || state != TrafficStateOn {
            return false
        }
    }

    return true
}
```

### Configuration with ARC

```yaml
principal:
  ha:
    enabled: true
    trafficAuthority:
      type: aws-arc
      aws:
        routingControlArn: arn:aws:route53-recovery-control::123456789012:controlpanel/abc123/routingcontrol/def456
        clusterArn: arn:aws:route53-recovery-control::123456789012:cluster/xyz789
```

### When to Use External Authority

| Scenario | Self-Fencing Only | With External Authority |
|----------|-------------------|-------------------------|
| Typical DR | ✅ Sufficient | Overkill |
| Regulatory/compliance | ⚠️ May not satisfy auditors | ✅ External witness documented |
| Zero split-brain tolerance | ⚠️ Edge cases exist | ✅ Belt + suspenders |
| Multi-cloud | ✅ Works | Need equivalent per cloud |

---

## Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Split-brain prevention | Self-fencing on lost peer ACK | Primary must prove replica listening; no external quorum needed |
| Consistency vs availability | Safety over availability | Accept brief outage to prevent corruption |
| Term/epoch | Increment on promotion | Reject stale updates even if fencing imperfect |
| Heartbeat interval | 5 seconds | Frequent enough for fast detection |
| ACK timeout | 15 seconds | Balance between false positives and detection speed |
| Failover timeout | 30 seconds | Time for replica to promote after stream breaks |
| Recovery timeout | 60 seconds | Time before unfencing after peer confirmed dead |
| Session termination on fence | Mandatory | Long-lived gRPC survives DNS changes |
| Failback mode | Manual only | Prevents flapping, gives operator control |
| External witness | Optional (pluggable) | Not required for correctness, adds defense in depth |

---

## Open Questions (Future Enhancements)

1. **Multi-Replica**: Should we support multiple replicas for read scaling or additional redundancy? See #186 for horizontal scaling design.

2. **Automatic Failback**: Add option to automatically failback after N minutes if preferred primary is synced and healthy?

3. **Partial Failures**: What if primary is reachable for replication but not for agents (port-specific partition)?

4. **Term Persistence**: Should term be persisted to disk, or is increment-on-startup sufficient?

---

## References

- [argocd-agent Architecture](../README.md)
- [Event System](../internal/event/event.go)
- [Queue Implementation](../internal/queue/queue.go)
- [Resync Protocol](../internal/resync/resync.go)
