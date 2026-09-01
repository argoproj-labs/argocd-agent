# High Availability Configuration

!!! important "HA Feature Stability"
    Principal HA & Replication is currently in Beta.

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

Role this principal prefers on startup. On startup, a principal configured as `primary` starts in ACTIVE. A principal configured as `replica` starts in SYNCING.

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

### Allowed Replication Clients

| | |
|---|---|
| **CLI Flag** | `--ha-allowed-replication-clients` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HA_ALLOWED_REPLICATION_CLIENTS` |
| **Type** | String slice (comma-separated) |
| **Default** | `[]` (any authenticated peer allowed) |

Explicit allowlist of peer identities permitted to connect for replication. Identities are extracted using the server's `--auth` method.

**Example:** `region-b,principal-replica`

### Admin Port

| | |
|---|---|
| **CLI Flag** | `--ha-admin-port` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HA_ADMIN_PORT` |
| **Type** | Integer |
| **Default** | `8405` |

Port for the HAAdmin gRPC server used by `argocd-agentctl ha` commands. Set to `0` to use the default.

### Admin Auth

| | |
|---|---|
| **CLI Flag** | `--ha-admin-auth` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HA_ADMIN_AUTH` |
| **Type** | String |
| **Default** | `""` |
| **Format** | `mtls:subject:<regex>` or `mtls:uri:<regex>` |

Authorization policy for the HA admin endpoint. When TLS is active, the admin endpoint inherits mTLS from the main server (unless independent admin TLS is configured). The regex is matched against the client certificate's subject DN or URI SANs.

!!! warning "Secure by Default"
    When TLS is active and `--ha-admin-auth` is **not** set, all admin calls are denied. You must explicitly configure this flag to allow access.

**Examples:**

- Allow only certs with `CN=ha-admin`: `mtls:subject:CN=ha-admin`
- Allow any cert in the `HA-Admins` org: `mtls:subject:O=HA-Admins`
- Allow SPIFFE identity: `mtls:uri:spiffe://cluster\.local/ns/argocd/sa/ha-admin`

### Independent Admin TLS

By default the admin endpoint inherits the server certificate and CA from the main gRPC server. When the main server uses SPIFFE/SPIRE or another external PKI, you can configure independent TLS for the admin endpoint so that operators can authenticate with traditional certificates.

All three flags must be provided together:

| | |
|---|---|
| **CLI Flags** | `--ha-admin-tls-cert`, `--ha-admin-tls-key`, `--ha-admin-ca` |
| **Environment Variables** | `ARGOCD_PRINCIPAL_HA_ADMIN_TLS_CERT`, `ARGOCD_PRINCIPAL_HA_ADMIN_TLS_KEY`, `ARGOCD_PRINCIPAL_HA_ADMIN_CA` |
| **Type** | String (file path) |
| **Default** | `""` (inherit from main gRPC server) |

When set, the admin endpoint uses its own server certificate and a separate CA for client verification, completely independent of the main gRPC server's TLS.

**Issue an admin client certificate using the CLI:**

```bash
argocd-agentctl pki issue ha-admin
argocd-agentctl pki issue ha-admin --name admin1 --upsert
```

## Ports Summary

| Port | Bind | TLS | Purpose |
|------|------|-----|---------|
| 8443 | `0.0.0.0` | mTLS | Agent gRPC + replication (shared) |
| 8405 | `0.0.0.0` | mTLS (inherited) | HAAdmin gRPC — when TLS is active |
| 8405 | `127.0.0.1` | None | HAAdmin gRPC — plaintext/service mesh mode |

## Example: Two-Region Setup

**Region A (preferred primary):**

```bash
argocd-agent principal \
  --ha-enabled \
  --ha-preferred-role=primary \
  --ha-peer-address=principal.region-b.internal:8443 \
  --ha-allowed-replication-clients=region-b \
  --ha-admin-auth="mtls:subject:CN=ha-admin"
```

**Region B (preferred replica):**

```bash
argocd-agent principal \
  --ha-enabled \
  --ha-preferred-role=replica \
  --ha-peer-address=principal.region-a.internal:8443 \
  --ha-allowed-replication-clients=region-a \
  --ha-admin-auth="mtls:subject:CN=ha-admin"
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
