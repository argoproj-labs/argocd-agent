# High Availability

argocd-agent supports active/passive High Availability for the principal component, enabling cross-region disaster recovery with operator-driven failover.

!!! important "HA Feature Stability"
    Principal HA & Replication is currently in Beta.

## Overview

Two principal instances run in **separate Kubernetes clusters**. One is ACTIVE (serving agents), the other is a replica (receiving replicated state). If the active principal fails, an operator promotes the replica to take over.

Agents connect through a single DNS/GSLB endpoint and are unaware of the HA topology. No agent configuration changes are needed for failover.

```text
                    Global DNS / GSLB
             principal.argocd.example.com
        Health checks: /healthz (200 when ACTIVE)
                  |                |
                  v                v
  +-----------------------+  +-----------------------+
  |  REGION A (Primary)   |  |  REGION B (Replica)   |
  |                       |  |                       |
  |  Principal (ACTIVE)   |  |  Replication Client   |
  |  - gRPC :8443     <------+  - Mirrors state      |
  |  - HAAdmin :8405      |  |  - /healthz :8003     |
  |  - /healthz :8003     |  |                       |
  |                       |  |  ArgoCD Instance      |
  |  ArgoCD Instance      |  |  (standby)            |
  |  (source of truth)    |  |                       |
  +-----------------------+  +-----------------------+
            ^                          ^
            |                          |
    +-------+--------------------------+-------+
    |              Remote Clusters             |
    |   [Agent 1]    [Agent 2]    [Agent N]    |
    +------------------------------------------+
```

## Design Philosophy

All promotion decisions are **external** (operator CLI or future coordinator). Principals never autonomously promote themselves. This eliminates self-fencing, term/epoch systems, and heartbeat protocols — significantly reducing split-brain risk at the cost of requiring operator intervention.

## State Machine

The HA controller manages five states:

| State | `/healthz` | Accepts Agents | Description |
|-------|-----------|----------------|-------------|
| RECOVERING | 503 | No | Startup, determining role |
| SYNCING | 503 | No | Initial catch-up to primary |
| REPLICATING | 503 | No | Receiving events, in sync |
| DISCONNECTED | 503 | No | Lost replication stream |
| ACTIVE | **200** | **Yes** | Serving agents |

Only ACTIVE returns a healthy response. GSLB routes agents exclusively to the active principal.

**Transitions:**

```text
RECOVERING ── config=primary ──> ACTIVE
RECOVERING ── config=replica ──> SYNCING --> REPLICATING
REPLICATING ── stream breaks ──> DISCONNECTED
DISCONNECTED ── stream reconnects ──> REPLICATING

Operator-only:
  {REPLICATING, DISCONNECTED} ── ha promote ──> ACTIVE
  ACTIVE ── ha demote ──> REPLICATING
```

## Replication

The replica runs a **Replication Client** that connects to the primary's gRPC server (port 8443) over a bidirectional stream. The replication service shares the same server and mTLS infrastructure as the agent API.

### What Gets Replicated

| Data | Method |
|------|--------|
| Applications | Full snapshot + incremental CloudEvents, written to replica's K8s cluster |
| AppProjects | Full snapshot + incremental CloudEvents, written to replica's K8s cluster |
| Agent connection metadata | Snapshot (agent name, mode, connected state) |
| Resource keys | Snapshot + event-driven |
| Queue state | Queue pairs created on snapshot; events flow as queued |

### Sync Flow

1. Replica connects to primary
2. Opens `Subscribe` stream first (events are buffered server-side)
3. Calls `GetSnapshot` — receives all agent states with full serialized resources
4. Writes Applications/AppProjects to its local K8s cluster (upsert)
5. Sends initial ACK to flush buffered events, then periodic ACKs with last processed sequence number
6. Runs periodic reconciliation — compares sequences via `Status` RPC, re-fetches snapshot if gaps detected

### Gap Recovery

The forwarder queue (1000 events) drops events on overflow. The client detects sequence gaps and marks itself for reconciliation. On the next reconciliation tick (default 1 minute), it re-fetches a full snapshot.

## Security

Replication RPCs are served on the main gRPC port (8443) alongside agent traffic. The server's interceptors route replication methods through a separate auth path using the HA controller's `AuthMethod` and `AllowedReplicationClients`.

The admin server (`ha status/promote/demote`) binds to `127.0.0.1:8405` with no TLS. Access requires `kubectl port-forward` — Kubernetes RBAC is the gate.

## Limitations

- **Preferred Role is startup configuration only.** Preferred role is configured via flags/env and is not changed by HA admin APIs (`status/promote/demote` only).
- **Split-brain is possible with operator error.** The promote safety check only verifies the local replication stream state. If two operators independently promote during a partition, both go ACTIVE. Mitigate by using `--force` only when the peer is confirmed dead.
- **Recovery time depends on replication lag at failure time.** Events not yet delivered to the replica are lost. Under normal load this is sub-second; under burst with queue overflow, up to 60s (bounded by reconciliation).
- **Full snapshot on gap recovery is O(N).** At scale, re-fetching all resources can be expensive.
- **Both-die scenario requires manual intervention.** If both principals restart simultaneously, the one configured as `preferredRole: primary` goes ACTIVE. Ensure `preferredRole` is always set.
