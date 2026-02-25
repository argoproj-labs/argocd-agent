# High Availability Configuration

!!! important "HA Feature Stability"
    Principal HA & Replication is currentl in Beta.

This page covers configuration for running the principal in active/passive HA mode. See [HA concepts](../concepts/ha.md) for an overview of how it works.

## Prerequisites

- Two principal deployments in **separate Kubernetes clusters**
- Shared CA so both principals can verify each other's client certificates
- A DNS/GSLB endpoint (or simple DNS A record) pointing to the active principal

## Principal Configuration

All HA flags have `ARGOCD_PRINCIPAL_HA_*` environment variable equivalents.

### Enable HA

| | |
|---|---|
| **CLI Flag** | `--ha-enabled` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HA_ENABLED` |
| **Type** | Boolean |
| **Default** | `false` |

Must be set to `true` on both principals.

### Preferred Role

| | |
|---|---|
| **CLI Flag** | `--ha-preferred-role` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HA_PREFERRED_ROLE` |
| **Type** | String |
| **Default** | `primary` |
| **Valid values** | `primary`, `replica` |

Role this principal prefers on startup. On startup, a principal configured as `primary` will go ACTIVE if it cannot detect an active peer. A principal configured as `replica` will always start in SYNCING.

### Peer Address

| | |
|---|---|
| **CLI Flag** | `--ha-peer-address` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HA_PEER_ADDRESS` |
| **Type** | String |
| **Default** | `""` |
| **Format** | `host:port` |

Address of the peer principal's gRPC server. Required on the replica; optional on the primary (used for status checks).

**Example:** `principal.region-b.internal:8443`

### Replication Auth

| | |
|---|---|
| **CLI Flag** | `--ha-replication-auth` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HA_REPLICATION_AUTH` |
| **Type** | String |
| **Default** | `""` |

Authentication method for verifying the connecting replication peer's identity. Only `mtls` is supported.

| Format | Source | Example |
|--------|--------|---------|
| `mtls:subject:<regex>` | Subject CN | `mtls:subject:CN=(.+)` |
| `mtls:uri:<regex>` | URI SAN (SPIFFE) | `mtls:uri:spiffe://example.com/principal/(.+)` |

The first capture group is the extracted peer identity, which is matched against `--ha-allowed-replication-clients`.

### Allowed Replication Clients

| | |
|---|---|
| **CLI Flag** | `--ha-allowed-replication-clients` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HA_ALLOWED_REPLICATION_CLIENTS` |
| **Type** | String slice (comma-separated) |
| **Default** | `[]` (any authenticated peer allowed) |

Explicit allowlist of peer identities permitted to connect for replication. Identities are extracted from the certificate using `--ha-replication-auth`.

**Example:** `region-b,principal-replica`

### Admin Port

| | |
|---|---|
| **CLI Flag** | `--ha-admin-port` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HA_ADMIN_PORT` |
| **Type** | Integer |
| **Default** | `8405` |

Port for the localhost-only HAAdmin gRPC server used by `argocd-agentctl ha` commands. Set to `0` to use the default.

## Ports Summary

| Port | Bind | TLS | Purpose |
|------|------|-----|---------|
| 8443 | `0.0.0.0` | mTLS | Agent gRPC + replication (shared) |
| 8405 | `127.0.0.1` | None | HAAdmin gRPC (status/promote/demote) |

## Example: Two-Region Setup

**Region A (preferred primary):**

```bash
argocd-agent principal \
  --ha-enabled \
  --ha-preferred-role=primary \
  --ha-peer-address=principal.region-b.internal:8443 \
  --ha-replication-auth=mtls:uri:spiffe://example.com/principal/(.+) \
  --ha-allowed-replication-clients=region-b
```

**Region B (preferred replica):**

```bash
argocd-agent principal \
  --ha-enabled \
  --ha-preferred-role=replica \
  --ha-peer-address=principal.region-a.internal:8443 \
  --ha-replication-auth=mtls:uri:spiffe://example.com/principal/(.+) \
  --ha-allowed-replication-clients=region-a
```

**Agents (unchanged):**

```bash
argocd-agent \
  --server-address=principal.argocd.example.com:8443
```

Agents connect to a single DNS endpoint. GSLB routes them to whichever principal is ACTIVE.

## GSLB / DNS Setup

Any DNS provider that supports health checks works. Configure health checks against `/healthz` on port 8003 — only ACTIVE principals return 200.

| Setting | Value |
|---------|-------|
| Health check path | `GET /healthz` on port 8003 |
| Healthy response | HTTP 200 |
| Unhealthy response | HTTP 503 |
| Recommended DNS TTL | 60s |

For environments without GSLB health checks, update the DNS A record manually as part of the failover procedure.

!!! note "Agent configuration is unchanged"
    Agents connect to the shared DNS name and reconnect automatically after failover once DNS TTL expires. No changes to agent configuration, certificates, or manifests are needed.
