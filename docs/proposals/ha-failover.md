## ArgoCD Agent HA Failover

_ai disclosure: diagrams and tables were formatted/generated with ai_

## Overview

This proposal adds active/passive High Availability to the argocd-agent principal, enabling cross-region disaster recovery with operator-driven failover.

**Design philosophy:** All promotion decisions are external (operator CLI or future coordinator). Principals never autonomously decide to go ACTIVE. This eliminates the need for self-fencing, term/epoch systems, and heartbeat protocols — significantly reducing split-brain risk at the cost of requiring operator intervention for failover.

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Recovery Target | 1–5 minutes | Acceptable for DR; limited by DNS TTL + operator response |
| Replication | Principal-to-principal streaming | Reuses existing CloudEvent/gRPC patterns |
| Agent connectivity | Single GSLB/DNS endpoint | Transparent to agents, zero agent changes |
| Failover trigger | Operator CLI (`ha promote/demote`) | No autonomous promotion = no split-brain |
| Consistency | Safety over availability | Brief outage during partition is acceptable |

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Global DNS / GSLB                           │
│                principal.argocd.example.com                     │
│           Health checks: /healthz (200 only when ACTIVE)        │
└──────────────┬──────────────────────────────┬───────────────────┘
               │                              │
               ▼                              ▼
  ┌──────────────────────┐      ┌──────────────────────┐
  │   REGION A (Primary) │      │  REGION B (Replica)  │
  │                      │      │                      │
  │  Principal Server    │◄─────│  Replication Client  │
  │  - gRPC :8443        │      │  - gRPC :8443        │
  │  - Replication :8404 │─────►│  - Mirrors state     │
  │  - /healthz :8003    │      │  - /healthz :8003    │
  │                      │      │                      │
  │  ArgoCD Instance     │      │  ArgoCD Instance     │
  │  (source of truth)   │      │  (standby, receives  │
  │                      │      │   replicated state)  │
  └──────────────────────┘      └──────────────────────┘
               ▲                              ▲
               │    Replication Stream:        │
               │    All events forwarded       │
               │    Primary → Replica          │
               │                               │
       ┌───────┴───────────────────────────────┴───────┐
       │              Remote Clusters                   │
       │    ┌─────────┐  ┌─────────┐  ┌─────────┐      │
       │    │ Agent 1 │  │ Agent 2 │  │ Agent N │      │
       │    └─────────┘  └─────────┘  └─────────┘      │
       └───────────────────────────────────────────────┘
```

Primary and Replica run in **separate Kubernetes clusters**. The Replica's cluster starts empty — Applications, AppProjects, and ApplicationSets are populated entirely via replication.

---

## State Machine

Five states, with only operator-triggered promotion:

```
RECOVERING ──┬── config=primary & peer not ACTIVE ──→ ACTIVE
             └── config=replica OR peer is ACTIVE ──→ SYNCING → REPLICATING

REPLICATING ──── stream breaks ──→ DISCONNECTED
DISCONNECTED ─── stream reconnects ──→ REPLICATING

Operator-only transitions:
  {REPLICATING, DISCONNECTED} ── ha promote ──→ ACTIVE
  ACTIVE ── ha demote ──→ REPLICATING
```

| State | `/healthz` | Accepts Agents | Description |
|-------|-----------|----------------|-------------|
| RECOVERING | 503 | No | Startup, determining role |
| SYNCING | 503 | No | Initial catch-up to primary |
| REPLICATING | 503 | No | Receiving events, in sync |
| DISCONNECTED | 503 | No | Lost replication stream |
| ACTIVE | **200** | **Yes** | Serving agents |

Only ACTIVE returns healthy. GSLB routes agents exclusively to the active principal.

The `promote` command checks whether the local principal is still actively replicating from a peer. If so, it refuses (the peer is likely alive) unless `--force` is passed. This prevents the most common operator error — promoting while the primary is still running.

---

## Replication

### Model

The Replica runs a **Replication Client** that connects to the Primary's **Replication Forwarder** via a bidirectional gRPC stream on port 8404. Unlike regular agents (namespace-scoped), the replication peer receives ALL events across all agents.

### What Gets Replicated

| Data | Method |
|------|--------|
| Applications | Full objects in snapshot + incremental CloudEvents. Written to replica's K8s cluster. |
| AppProjects | Full objects in snapshot + incremental CloudEvents. Written to replica's K8s cluster. |
| ApplicationSets | Full objects in snapshot + incremental CloudEvents. Written to replica's K8s cluster. |
| Agent connection metadata | Snapshot (agent name, mode, connected state) |
| Resource keys | Snapshot + event-driven |
| Queue state | Queue pairs created on snapshot; events flow as queued |

### Protocol

Three RPCs defined in `principal/apis/replication/replication.proto`:

| RPC | Direction | Purpose |
|-----|-----------|---------|
| `Subscribe` | Bidi stream | Replica receives `ReplicatedEvent`s, sends `ReplicationAck`s |
| `GetSnapshot` | Unary | Initial full-state sync on connect |
| `Status` | Unary | Sequence number + lag for monitoring and reconciliation |

Each `ReplicatedEvent` wraps a CloudEvent with: agent name, direction (inbound/outbound), sequence number, and timestamp. Events are tagged with direction so the replica knows whether to update its local state (inbound) or queue for future agent delivery (outbound).

Both `Subscribe` and `GetSnapshot` are protected by mTLS authentication and the allowlist check (see Security section).

### Sync Flow

1. Replica opens `Subscribe` stream — primary begins buffering incremental events
2. Replica calls `GetSnapshot` — receives all agent states with full serialized resources
3. Writes Applications/AppProjects/ApplicationSets to its local K8s cluster (upsert)
4. Sends initial ACK on the `Subscribe` stream with snapshot sequence number
5. Sends periodic ACKs (every 5s) with last processed sequence number
6. Runs periodic reconciliation (every 1m) — compares sequences via `Status` RPC, re-fetches snapshot if gaps detected

Subscribe is opened before GetSnapshot so the primary buffers events during the snapshot transfer. Without this ordering, events occurring between snapshot start and stream open would be silently lost.

### Gap Recovery

The forwarder queue (1000 events) drops events on overflow. The client detects sequence gaps and marks itself for reconciliation. On the next reconciliation tick, it re-fetches a full snapshot to catch up. This bounds drift to at most 1 minute.

---

## Security

The replication server uses mTLS for authentication. The replica presents a client certificate; the primary extracts the SPIFFE URI SAN identity and checks it against the `--ha-allowed-replication-clients` allowlist.

Both streaming (`Subscribe`) and unary (`GetSnapshot`, `Status`) RPCs are protected via separate gRPC interceptors. A stream interceptor alone is insufficient — unary RPCs require a dedicated unary interceptor.

The HA admin server (port 8405) has no TLS and binds `127.0.0.1` only. Operators access it via `kubectl port-forward`; Kubernetes RBAC is the auth boundary.

```
Replica → primary replication server (port 8404)
  TLS: mutual, replica presents cert signed by shared CA
  Auth: primary extracts SPIFFE identity, checks allowlist
  Rejection: codes.PermissionDenied with identity logged at WARN level
```

Configuration:

```
--ha-enabled                       Enable HA mode
--ha-preferred-role primary|replica Role this principal prefers on startup
--ha-peer-address <host:port>      Replication server address of the peer
--ha-allowed-replication-clients   Comma-separated allowlist of replica identities
--ha-replication-tls-cert          TLS cert for replication server
--ha-replication-tls-key           TLS key for replication server
--ha-replication-tls-ca            CA cert for validating replica client certs
```

Or via environment variables (e.g., `ARGOCD_PRINCIPAL_HA_ALLOWED_REPLICATION_CLIENTS=identity1,identity2`).

---

## Failover Scenarios

### Primary Dies

```
T+0s   Primary dies. Replica detects stream break → DISCONNECTED.
T+30s  Operator notified via alert. Both principals unhealthy from GSLB perspective.
T+31s  Operator runs: argocd-agentctl ha promote [--force]
       Replica → ACTIVE. Health → 200.
T+60s  DNS TTL expires. Agents reconnect to Region B via GSLB.
T+90s  Fully operational.
```

No full resync needed — replica already has all resources written to its K8s cluster.

### Clean Switchover (Primary Alive)

```
$ argocd-agentctl ha demote    # on Region A — drops agents, stops serving
$ argocd-agentctl ha promote   # on Region B — becomes ACTIVE
# Update DNS to point to Region B
```

### Failback

```
T+0    Old primary restarts → RECOVERING → SYNCING (peer is ACTIVE)
       Connects to Region B as replica → REPLICATING
T+2m   Caught up (lag: 0s)
       Operator runs:
       $ argocd-agentctl ha demote    # on Region B
       $ argocd-agentctl ha promote   # on Region A
       Update DNS back to Region A.
```

### Agent Reconnection

Because the replica has been continuously replicating and writing resources to its cluster:

1. Agent authenticates (same shared CA)
2. Replica already has all resources — no full resync
3. Quick checksum verification confirms state
4. Only in-flight events during failover need delta sync

---

## CLI

The `argocd-agentctl ha` subcommand manages HA state. It auto port-forwards to the principal pod's admin port (8405) via `--principal-context`, or accepts `--address` for a direct connection. The connection is plain gRPC (no TLS).

| Command | Description |
|---------|-------------|
| `ha status` | Show state, peer status, replication lag, sequence numbers |
| `ha promote` | Transition to ACTIVE. Refuses if peer is ACTIVE unless `--force`. |
| `ha demote` | Transition ACTIVE → REPLICATING. Disconnects all agents. |

---

## GSLB / DNS Setup

Any GSLB or DNS provider that supports health checks works. Requirements:

| Requirement | Detail |
|-------------|--------|
| Health check | Poll `/healthz` on each principal (port 8003) |
| Failover routing | Route to healthy endpoint |
| DNS TTL | Recommend 60s |
| Single endpoint | Agents resolve one DNS name |

DNS is operator-managed. The principal's health endpoint reflects HA state — only ACTIVE returns 200.

For environments that only have simple DNS (no GSLB health checks), the operator manually updates the DNS A record as part of the failover procedure.

### Ports

| Port | Bind | TLS | Purpose |
|------|------|-----|---------|
| 8443 | `0.0.0.0` | mTLS | Agent gRPC |
| 8404 | `0.0.0.0` | mTLS | Principal-to-principal replication |
| 8405 | `127.0.0.1` | None | HAAdmin gRPC (`ha status/promote/demote`) |
| 8003 | `0.0.0.0` | None | Health check HTTP (`/healthz`) |

The admin port (8405) binds localhost only — no TLS. `argocd-agentctl` establishes a `kubectl port-forward` automatically; Kubernetes RBAC is the auth boundary.

---

## Observability

Prometheus metrics exposed for monitoring:

| Metric | Type | Description |
|--------|------|-------------|
| `argocd_agent_ha_state` | Gauge | Current HA state (labeled) |
| `argocd_agent_ha_state_transitions_total` | Counter | State transition count |
| `argocd_agent_ha_failovers_total` | Counter | Failover events |
| `argocd_agent_replication_forwarder_events_total` | Counter | Events forwarded |
| `argocd_agent_replication_forwarder_events_dropped_total` | Counter | Events dropped (queue full) |
| `argocd_agent_replication_forwarder_queue_depth` | Gauge | Pending events in queue |
| `argocd_agent_replication_forwarder_replicas_connected` | Gauge | Connected replicas |
| `argocd_agent_replication_client_events_total` | Counter | Events received by client |
| `argocd_agent_replication_client_lag_seconds` | Gauge | Replication lag |
| `argocd_agent_replication_client_sequence_gaps_total` | Counter | Sequence gaps detected |
| `argocd_agent_replication_client_reconciliations_total` | Counter | Snapshot re-fetches from gap recovery |

Recommended alerts:
- **ReplicationLagHigh**: `client_lag_seconds > 5` for 1m
- **ReplicaDisconnected**: `forwarder_replicas_connected == 0` for 30s
- **QueueNearCapacity**: `forwarder_queue_depth > 900` for 1m

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Failover mode | Manual only (operator promote/demote) | No autonomous promotion; operator controls RTO vs safety |
| Split-brain prevention | Promote refuses if replication stream active | Common error caught; `--force` for emergencies (see Limitations) |
| Peer health detection | Replication stream state | No separate heartbeat/ACK protocol needed |
| State count | 5 | RECOVERING, SYNCING, REPLICATING, DISCONNECTED, ACTIVE |
| Resource replication | Full objects written to replica K8s cluster | Separate clusters, no shared state |
| Replication backpressure | Drop + metric + reconcile | Simple for v1; bounded 1-min drift via reconciliation |
| Sync flow order | Subscribe first, then GetSnapshot | Primary buffers events during snapshot transfer; prevents loss between snapshot point and stream open |
| Interceptor split | Both stream and unary interceptors registered | `grpc.StreamInterceptor` does not cover unary RPCs; `GetSnapshot`/`Status` require a separate unary interceptor |
| Secrets | Operator configures manually | Avoid replicating sensitive data |
| Agent changes | None | GSLB/DNS transparent failover |
| DNS integration | None (operator-managed) | Works with any provider |

---

## Limitations and Edge Cases

**Split-brain is possible with operator error.** The promote safety check only verifies the local replication stream is broken (DISCONNECTED state). It does not RPC to the peer to confirm the peer is down. If two operators in separate regions independently promote during a network partition, both principals go ACTIVE. Mitigation: use `--force` only when the peer is confirmed dead. Future work: add external coordinator (see below) for stronger guarantees, or add peer Status RPC check before promotion.

**RPO depends on replication lag at time of failure.** Events processed by the primary but not yet delivered to the replica are lost on failover. RPO = time since last replica ACK + any events in the forwarder queue. Under normal operation this is sub-second; under burst load with queue overflow, it can be up to 60s (bounded by reconciliation interval).

**Full snapshot on gap recovery is O(N).** When sequence gaps are detected, the client re-fetches a complete snapshot of all agents and resources. At scale (thousands of Applications), this can be expensive. Future work: incremental catch-up using `since_sequence_num` to fetch only missing events.

**Forwarder queue has no backpressure.** The 1000-event queue drops events on overflow with only a metric increment. Sustained burst traffic can cause repeated gaps and reconciliation storms. Future work: configurable queue size, backpressure signaling, or ring buffer with eviction.

**Demote→promote sequence has a brief window.** During clean switchover, after demoting the primary and before promoting the replica, both principals are unhealthy. Agents cannot connect during this window. The window is typically sub-second but depends on operator speed. Future work: atomic switchover command.

**Both-die scenario requires manual intervention.** If both principals die and restart simultaneously, both enter RECOVERING. The one configured as `preferredRole: primary` goes ACTIVE; the other goes SYNCING. If configuration is identical or missing, behavior is undefined. Ensure `preferredRole` is always set.

---

## Future Work

1. **External coordinator interface** — Pluggable `Coordinator` interface that the principal polls to determine whether it should be ACTIVE. This removes operator error as a split-brain vector — the coordinator is the single source of truth for which principal serves traffic.

   ```go
   type Coordinator interface {
       ShouldBeActive(ctx context.Context) (bool, error)
   }
   ```

   The principal polls periodically and transitions accordingly: if the coordinator says "active" and the principal isn't, it promotes; if it says "not active" and the principal is, it demotes and disconnects agents.

   Candidate implementations:

   | Backend | How It Works |
   |---------|-------------|
   | **AWS Route53 ARC** | Routing controls provide explicit on/off switches per region with safety rules preventing both from being ON simultaneously. Failover = flip the routing control via console, CLI, or ARC's health-check automation. |
   | **Consul** | Distributed lock / session-based leader election. Principal holds a Consul session; losing the session triggers demotion. |
   | **Kubernetes Lease** | Lease object in a shared control plane (multi-AZ). Principal that holds the lease is ACTIVE. Works for single-cluster or multi-AZ setups, not cross-region. |
   | **HashiCorp Vault** | Vault's HA backend (Consul/Raft) for distributed lock. |
   | **etcd** | Direct etcd lease for environments already running etcd. |

   Configuration would look like:
   ```yaml
   principal:
     ha:
       enabled: true
       mode: coordinator        # "manual" (default) or "coordinator"
       coordinator:
         type: aws-arc          # or: consul, k8s-lease
         pollInterval: 10s
         aws:
           routingControlArn: arn:aws:route53-recovery-control::123456789012:...
   ```

   With a coordinator, failover becomes: flip the external control (via cloud console, CLI, or the coordinator's own health-check automation). The principal sees the change on next poll and transitions automatically. Manual `ha promote/demote` commands remain available as an override.

2. **Peer status RPC in promote** — Before allowing promotion, call peer's Status RPC to confirm it is not ACTIVE. Strengthen the safety check beyond local state.
3. **Incremental gap recovery** — Use `since_sequence_num` in snapshot requests to fetch only missing events instead of full state.
4. **Multi-replica** — Multiple replicas for additional redundancy.
5. **Automatic failback** — Auto-failback when preferred primary is synced and healthy for N minutes.
6. **CLI-integrated DNS** — `ha failover` optionally updates DNS records directly.

---

## References

- [argocd-agent Architecture](../../README.md)
- [Replication Proto](../../principal/apis/replication/replication.proto)
- [HA Controller](../../pkg/ha/controller.go)
- [Replication Client](../../pkg/replication/client.go)
- [Replication Forwarder](../../pkg/replication/forwarder.go)
- [Replication Server (auth)](../../principal/apis/replication/server.go)
- [ApplicationSet Replication](../../principal/callbacks.go)
