# HA Failover Operations

This page covers operating HA principals day-to-day: checking status, performing failovers, and monitoring replication health. See [HA concepts](../concepts/ha.md) and [HA configuration](../configuration/ha.md) for setup.

## CLI Reference

The `argocd-agentctl ha` commands manage HA state. By default they auto-detect the principal pod via `--principal-context` and port-forward to the admin port (8405). Use `--address` for a direct connection.

### ha status

Show current HA state, peer reachability, and replication info.

```bash
argocd-agentctl ha status
argocd-agentctl ha status -o json
argocd-agentctl ha status -o yaml
```

Example output:

```text
HA Status
---------
State:             active
Preferred Role:    primary
Peer Address:      principal.region-b.internal:8443
Peer Reachable:    true
Connected Replicas: 1
Connected Agents:  12
Last Event:        2s ago
Last Sequence:     48291
```

### ha promote

Transition to ACTIVE. Refuses if the replication stream is still connected (the peer is likely alive) unless `--force` is passed.

```bash
argocd-agentctl ha promote
argocd-agentctl ha promote --force   # skip safety check
```

### ha demote

Transition ACTIVE to REPLICATING. Disconnects all connected agents.

```bash
argocd-agentctl ha demote
argocd-agentctl ha demote --force    # skip confirmation prompt
```

## Procedures

### Unplanned Failover (Primary Dies)

```text
T+0s   Primary dies. Replica detects stream break -> DISCONNECTED.
T+30s  Operator notified via alert.
T+31s  Operator runs:
         argocd-agentctl ha promote --force
       Replica -> ACTIVE. /healthz -> 200.
T+60s  DNS TTL expires. Agents reconnect to Region B.
T+90s  Fully operational.
```

No full resync — the replica already has all resources written to its K8s cluster.

**Steps:**

1. Confirm the primary is actually down (check pod, node, network)
2. Check replica status: `argocd-agentctl ha status`
3. Promote: `argocd-agentctl ha promote --force`
4. Verify: `argocd-agentctl ha status` shows `active`
5. If using manual DNS, update the A record to point to Region B

### Clean Switchover (Primary Alive)

When both principals are healthy and you want to switch the active role.

**Steps:**

1. Check both principals: `argocd-agentctl ha status` on each
2. Demote the current primary:
   ```bash
   argocd-agentctl ha demote
   ```
3. Promote the replica:
   ```bash
   argocd-agentctl ha promote
   ```
4. Update DNS if not using GSLB with health checks
5. Verify agents reconnect: `argocd-agentctl ha status` shows agents connecting

### Failback

Restore the original primary after an unplanned failover.

1. Old primary restarts -> RECOVERING -> SYNCING -> REPLICATING
2. Wait for replication to catch up:
   ```bash
   argocd-agentctl ha status   # on old primary, look for "replicating"
   ```
3. Once caught up, perform a clean switchover (demote Region B, promote Region A)
4. Update DNS back to Region A

## Monitoring

### Metrics

Replication metrics are exposed at the principal's metrics endpoint (default port 8000).

**Forwarder (active principal):**

| Metric | Type | Description |
|--------|------|-------------|
| `argocd_agent_replication_events_queued_total` | Counter | Events queued for replication |
| `argocd_agent_replication_events_dropped_total` | Counter | Events dropped (queue full) |
| `argocd_agent_replication_events_forwarded_total` | Counter | Events sent to replicas |
| `argocd_agent_replication_replicas_connected` | Gauge | Connected replica count |
| `argocd_agent_replication_queue_size` | Gauge | Pending events in queue |
| `argocd_agent_replication_forwarding_errors_total` | Counter | Send errors |
| `argocd_agent_replication_last_sequence_number` | Gauge | Latest sequence number |

**Client (replica principal):**

| Metric | Type | Description |
|--------|------|-------------|
| `argocd_agent_replication_client_events_received_total` | Counter | Events received from primary |
| `argocd_agent_replication_client_lag_seconds` | Gauge | Replication lag |
| `argocd_agent_replication_client_sequence_gaps_total` | Counter | Sequence gaps detected |
| `argocd_agent_replication_client_reconciliations_total` | Counter | Full snapshot re-fetches |

**HA state:**

| Metric | Type | Description |
|--------|------|-------------|
| `argocd_agent_ha_state` | Gauge | Current HA state (labeled) |
| `argocd_agent_ha_transitions_total` | Counter | State transition count |
| `argocd_agent_ha_failovers_total` | Counter | Promotion events |

### Recommended Alerts

| Alert | PromQL | For |
|-------|--------|-----|
| ReplicationLagHigh | `argocd_agent_replication_client_lag_seconds > 5` | 1m |
| ReplicaDisconnected | `argocd_agent_replication_replicas_connected == 0` | 30s |
| QueueNearCapacity | `argocd_agent_replication_queue_size > 900` | 1m |
| BothPrincipalsActive | `count(argocd_agent_ha_state{state="active"}) > 1` | 0s |

## Troubleshooting

### Replica stuck in DISCONNECTED

Check network connectivity between regions:

```bash
# From the replica pod
kubectl exec -it <replica-pod> -- curl -sS http://<primary-addr>:8003/healthz
```

Check that replication auth is configured correctly — the replica needs a client certificate the primary trusts, and the extracted identity must be in `--ha-allowed-replication-clients`.

### Promote refuses (replication stream active)

The safety check prevents promoting while still receiving events from a peer. If you're certain the primary is dead but the stream hasn't timed out:

```bash
argocd-agentctl ha promote --force
```

### Agents not reconnecting after failover

- Check DNS TTL — agents won't resolve the new address until TTL expires (default recommendation: 60s)
- Verify `/healthz` returns 200 on the newly promoted principal
- Check GSLB health check configuration points to port 8003

### Sequence gaps and frequent reconciliation

The `sequence_gaps_total` metric incrementing means events are being dropped in the forwarder queue. This is normal during burst traffic and self-corrects on the next reconciliation cycle (default 1 minute). Sustained gaps indicate the replica can't keep up. Check network latency and resource utilization.
