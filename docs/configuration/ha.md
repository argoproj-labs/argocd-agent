# High Availability Configuration

!!! important "HA Feature Stability"
    Principal HA & Replication is currently in Beta.

This page covers configuration for running the principal in active/passive HA mode. See [HA concepts](../concepts/ha.md) for an overview of how it works.

## Prerequisites

- Two principal deployments in **separate Kubernetes clusters**
- Shared CA (see [Shared CA Setup](#shared-ca-setup)) so both principals can verify each other's client certificates
- A dedicated **client certificate** for each principal to authenticate to its peer for replication (see [Replication Client Certificate](#replication-client-certificate)). This is separate from the principal's server certificate — the server certificate stays `serverAuth` only.
- A DNS/GSLB endpoint (or simple DNS A record) pointing to the active principal

## Shared CA Setup

Both principals must be signed by, and trust, the same CA. If each principal has its own independent CA, certificates issued on one side are not trusted on the other. If the active principal fails over to the other region, agents cannot reconnect to the newly active principal. Their client certificates were signed by a CA the new principal does not trust. The new principal's server certificate was signed by a CA the agents do not trust. TLS handshakes fail in both directions and agents stay disconnected until the CA mismatch is fixed.

Set this up before issuing any other certificates:

1. Run `argocd-agentctl pki init` on primary principal cluster:

    ```bash
    argocd-agentctl pki init \
      --principal-context region-a \
      --principal-namespace argocd
    ```

2. Copy that CA, including its private key, into replica principal cluster, using `pki propagate-ca`:

    ```bash
    argocd-agentctl pki propagate-ca \
      --principal-context region-a \
      --principal-namespace argocd \
      --target-context region-b \
      --target-namespace argocd
    ```

    This creates the same `argocd-agent-ca` secret in replica principal, so that certificates issued against either cluster are signed by, and trusted by, the identical CA. The private key is intentionally included here, unlike an agent, replica principal must be able to independently issue and renew its own server and replication client certificates, including during a failover when the original primary cluster may be unreachable.

3. Now proceed to issue the principal server, resource proxy, and replication client certificates for both principals, each `pki issue` invocation signs using whatever CA secret already exists in the context you point it at, so both sides need the shared CA in place first.

!!! note "This is different from agent CA propagation"
    `pki propagate` and `pki issue agent` copy only the CA's public certificate to an agent cluster, because agents never issue their own certificates. `pki propagate-ca` copies the private key too, because replica principal does.

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

**Example:** `argocd-agent-principal-peer` (the common name of the certificate issued by `argocd-agentctl pki issue principal-peer`, see [Replication Client Certificate](#replication-client-certificate))

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

### Replication Client Certificate

When connecting to its peer for replication, a replica principal authenticates with a **dedicated client certificate** that is separate from its server certificate. The server certificate remains `serverAuth` only; mixing `serverAuth` and `clientAuth` into one certificate is discouraged and rejected by many CAs.

The certificate must be signed by the shared CA, and its common name (or whatever identity the peer's `--auth` method extracts) must appear in the peer's [`--ha-allowed-replication-clients`](#allowed-replication-clients).

Both flags must be provided together:

| | |
|---|---|
| **CLI Flags** | `--ha-peer-tls-cert`, `--ha-peer-tls-key` |
| **Environment Variables** | `ARGOCD_PRINCIPAL_HA_PEER_TLS_CERT`, `ARGOCD_PRINCIPAL_HA_PEER_TLS_KEY` |
| **Type** | String (file path) |
| **Default** | `""` |

**Issue the replication client certificate using the CLI:**

```bash
argocd-agentctl pki issue principal-peer
```

This issues a `clientAuth`-only certificate (common name `argocd-agent-principal-peer`) signed by the principal's CA and stores it in the `argocd-agent-ha-peer-tls` secret in the principal namespace. Mount that secret into the principal and point `--ha-peer-tls-cert` / `--ha-peer-tls-key` at it.

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
  --ha-allowed-replication-clients=argocd-agent-principal-peer \
  --ha-peer-tls-cert=/tls/peer/tls.crt \
  --ha-peer-tls-key=/tls/peer/tls.key \
  --ha-admin-auth="mtls:subject:CN=ha-admin"
```

**Region B (preferred replica):**

```bash
argocd-agent principal \
  --ha-enabled \
  --ha-preferred-role=replica \
  --ha-peer-address=principal.region-a.internal:8443 \
  --ha-allowed-replication-clients=argocd-agent-principal-peer \
  --ha-peer-tls-cert=/tls/peer/tls.crt \
  --ha-peer-tls-key=/tls/peer/tls.key \
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
