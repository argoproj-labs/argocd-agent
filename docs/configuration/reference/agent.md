# Agent Configuration Reference

Complete reference for all agent component configuration parameters.

## Server Connection

### Server Address

| | |
|---|---|
| **CLI Flag** | `--server-address` |
| **Environment Variable** | `ARGOCD_AGENT_REMOTE_SERVER` |
| **ConfigMap Entry** | `agent.server.address` |
| **Type** | String |
| **Default** | `""` |
| **Required** | Yes |

Address of the principal server to connect to.

**Example:** `argocd-agent-principal.example.com`

### Server Port

| | |
|---|---|
| **CLI Flag** | `--server-port` |
| **Environment Variable** | `ARGOCD_AGENT_REMOTE_PORT` |
| **ConfigMap Entry** | `agent.server.port` |
| **Type** | Integer |
| **Default** | `443` |
| **Range** | 1-65535 |

Port on the principal server to connect to.

## Agent Operation

### Agent Mode

| | |
|---|---|
| **CLI Flag** | `--agent-mode` |
| **Environment Variable** | `ARGOCD_AGENT_MODE` |
| **ConfigMap Entry** | `agent.mode` |
| **Type** | String |
| **Default** | `autonomous` |
| **Valid Values** | `autonomous`, `managed` |

Mode of operation for the agent.

### Namespace

| | |
|---|---|
| **CLI Flag** | `--namespace`, `-n` |
| **Environment Variable** | `ARGOCD_AGENT_NAMESPACE` |
| **ConfigMap Entry** | `agent.namespace` |
| **Type** | String |
| **Default** | `argocd` |
| **Required** | Yes |

Namespace to manage applications in.

### Credentials

| | |
|---|---|
| **CLI Flag** | `--creds` |
| **Environment Variable** | `ARGOCD_AGENT_CREDS` |
| **ConfigMap Entry** | `agent.creds` |
| **Type** | String |
| **Default** | `""` |
| **Format** | `<method>:<configuration>` |

Credentials to use when connecting to server.

**Valid Methods:**

| Method | Format | Description |
|--------|--------|-------------|
| `mtls` | `mtls:` | Mutual TLS authentication using client certificate |
| `header` | `header:` | Header-based authentication for service mesh environments |
| `userpass` | `userpass:<path>` | **[DEPRECATED]** Username/password authentication |

**Examples:**

- mTLS: `mtls:`
- Service mesh: `header:`
- Userpass (deprecated): `userpass:/app/config/creds/userpass.creds`

## TLS Configuration

### Insecure TLS

| | |
|---|---|
| **CLI Flag** | `--insecure-tls` |
| **Environment Variable** | `ARGOCD_AGENT_TLS_INSECURE` |
| **ConfigMap Entry** | `agent.tls.client.insecure` |
| **Type** | Boolean |
| **Default** | `false` |

Skip verification of remote TLS certificate. **Development only.**

### Root CA Secret Name

| | |
|---|---|
| **CLI Flag** | `--root-ca-secret-name` |
| **Environment Variable** | `ARGOCD_AGENT_TLS_ROOT_CA_SECRET_NAME` |
| **ConfigMap Entry** | `agent.tls.root-ca-secret-name` |
| **Type** | String |
| **Default** | `argocd-agent-ca` |

Name of the secret containing the root CA certificate.

### Root CA Path

| | |
|---|---|
| **CLI Flag** | `--root-ca-path` |
| **Environment Variable** | `ARGOCD_AGENT_TLS_ROOT_CA_PATH` |
| **ConfigMap Entry** | `agent.tls.root-ca-path` |
| **Type** | String |
| **Default** | `""` |

Path to file containing root CA certificate for verifying remote TLS.

### TLS Secret Name

| | |
|---|---|
| **CLI Flag** | `--tls-secret-name` |
| **Environment Variable** | `ARGOCD_AGENT_TLS_SECRET_NAME` |
| **ConfigMap Entry** | `agent.tls.secret-name` |
| **Type** | String |
| **Default** | `argocd-agent-client-tls` |

Name of the secret containing the TLS client certificate.

### TLS Client Certificate

| | |
|---|---|
| **CLI Flag** | `--tls-client-cert` |
| **Environment Variable** | `ARGOCD_AGENT_TLS_CLIENT_CERT_PATH` |
| **ConfigMap Entry** | `agent.tls.client.cert-path` |
| **Type** | String |
| **Default** | `""` |

Path to TLS client certificate file.

### TLS Client Key

| | |
|---|---|
| **CLI Flag** | `--tls-client-key` |
| **Environment Variable** | `ARGOCD_AGENT_TLS_CLIENT_KEY_PATH` |
| **ConfigMap Entry** | `agent.tls.client.key-path` |
| **Type** | String |
| **Default** | `""` |

Path to TLS client private key file.

### TLS Minimum Version

| | |
|---|---|
| **CLI Flag** | `--tls-min-version` |
| **Environment Variable** | `ARGOCD_AGENT_TLS_MIN_VERSION` |
| **ConfigMap Entry** | `agent.tls.min-version` |
| **Type** | String |
| **Default** | `""` (Go default) |
| **Valid Values** | `tls1.1`, `tls1.2`, `tls1.3` |

Minimum TLS version to use when connecting to the principal.

### TLS Maximum Version

| | |
|---|---|
| **CLI Flag** | `--tls-max-version` |
| **Environment Variable** | `ARGOCD_AGENT_TLS_MAX_VERSION` |
| **ConfigMap Entry** | `agent.tls.max-version` |
| **Type** | String |
| **Default** | `""` (highest available) |
| **Valid Values** | `tls1.1`, `tls1.2`, `tls1.3` |

Maximum TLS version to use when connecting to the principal.

### TLS Cipher Suites

| | |
|---|---|
| **CLI Flag** | `--tls-ciphersuites` |
| **Environment Variable** | `ARGOCD_AGENT_TLS_CIPHERSUITES` |
| **ConfigMap Entry** | `agent.tls.ciphersuites` |
| **Type** | String (comma-separated) |
| **Default** | `""` (Go defaults) |

Comma-separated list of TLS cipher suites to use. Use `--tls-ciphersuites=list` to display available options.

## Logging and Debugging

### Log Level

| | |
|---|---|
| **CLI Flag** | `--log-level` |
| **Environment Variable** | `ARGOCD_AGENT_LOG_LEVEL` |
| **ConfigMap Entry** | `agent.log.level` |
| **Type** | String (comma-separated list) |
| **Default** | `info` |
| **Format** | `[<component>=]<level>` |
| **Valid Values (component)** | `resource-proxy`, `redis-proxy`, `grpc-event` |
| **Valid Values (level)** | `trace`, `debug`, `info`, `warning`, `error` |

The log level for the general logger and subsystem loggers for the agent.

### Log Format

| | |
|---|---|
| **CLI Flag** | `--log-format` |
| **Environment Variable** | `ARGOCD_AGENT_LOG_FORMAT` |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `text` |
| **Valid Values** | `text`, `json` |

The log format to use.

### Profiling Port

| | |
|---|---|
| **CLI Flag** | `--pprof-port` |
| **Environment Variable** | `ARGOCD_AGENT_PPROF_PORT` |
| **ConfigMap Entry** | N/A |
| **Type** | Integer |
| **Default** | `0` (disabled) |
| **Range** | 0, 1024-65535 |

Port the pprof server will listen on. Set to 0 to disable.

## Monitoring and Health

### Metrics Port

| | |
|---|---|
| **CLI Flag** | `--metrics-port` |
| **Environment Variable** | `ARGOCD_AGENT_METRICS_PORT` |
| **ConfigMap Entry** | `agent.metrics.port` |
| **Type** | Integer |
| **Default** | `8181` |
| **Range** | 1024-65535 |

Port the metrics server will listen on.

### Health Check Port

| | |
|---|---|
| **CLI Flag** | `--healthz-port` |
| **Environment Variable** | `ARGOCD_AGENT_HEALTH_CHECK_PORT` |
| **ConfigMap Entry** | `agent.healthz.port` |
| **Type** | Integer |
| **Default** | `8001` |
| **Range** | 1024-65535 |

Port the health check server will listen on.

## Startup Behavior

### Informer Sync Timeout

| | |
|---|---|
| **CLI Flag** | `--informer-sync-timeout` |
| **Environment Variable** | `ARGOCD_AGENT_INFORMER_SYNC_TIMEOUT` |
| **ConfigMap Entry** | `agent.informer-sync-timeout` |
| **Type** | Duration |
| **Default** | `10s` |

How long the agent waits for its informers (Application, AppProject, Repository, GPG, Namespace) to complete the initial cache sync at startup. If the informers do not sync within this window, the agent fails to start.

Increase this value on large clusters or clusters under high API server load where the initial list-and-watch round-trips take longer than the default.

**Example:** `30s`

## Network and Performance

### Enable WebSocket

| | |
|---|---|
| **CLI Flag** | `--enable-websocket` |
| **Environment Variable** | `ARGOCD_AGENT_ENABLE_WEBSOCKET` |
| **ConfigMap Entry** | N/A |
| **Type** | Boolean |
| **Default** | `false` |

Use gRPC over WebSocket to stream events to the Principal.

### Keep Alive Ping Interval

| | |
|---|---|
| **CLI Flag** | `--keep-alive-ping-interval` |
| **Environment Variable** | `ARGOCD_AGENT_KEEP_ALIVE_PING_INTERVAL` |
| **ConfigMap Entry** | N/A |
| **Type** | Duration |
| **Default** | `0` (disabled) |

HTTP/2 PING frame interval to detect dead connections (transport-level keepalive).

**Note:** HTTP/2 PING frames do NOT count as requests for service mesh idle timeouts. For service mesh deployments (like Istio), use `--heartbeat-interval` instead.

**Example:** `30s`

### Heartbeat Interval

| | |
|---|---|
| **CLI Flag** | `--heartbeat-interval` |
| **Environment Variable** | `ARGOCD_AGENT_HEARTBEAT_INTERVAL` |
| **ConfigMap Entry** | N/A |
| **Type** | Duration |
| **Default** | `0` (disabled) |

Interval for application-level heartbeat messages over the Subscribe stream. Sends ping events that count as requests to prevent service mesh idle timeouts.

**Best Practice:** Set to 50-75% of your service mesh's `idleTimeout` (e.g., if Istio `idleTimeout` is 60s, use 30-45s).

**Example:** `30s`

### Enable Compression

| | |
|---|---|
| **CLI Flag** | `--enable-compression` |
| **Environment Variable** | `ARGOCD_AGENT_ENABLE_COMPRESSION` |
| **ConfigMap Entry** | N/A |
| **Type** | Boolean |
| **Default** | `false` |

Use compression while sending data between Principal and Agent using gRPC.

## Redis Configuration

### Redis Address

| | |
|---|---|
| **CLI Flag** | `--redis-addr` |
| **Environment Variable** | `REDIS_ADDR` |
| **ConfigMap Entry** | `agent.redis.address` |
| **Type** | String |
| **Default** | `argocd-redis:6379` |

The redis host to connect to.

### Redis Credentials directory path

| | |
|---|---|
| **CLI Flag** | `--redis-creds-dir-path` |
| **Environment Variable** | `REDIS_CREDS_DIR_PATH` |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `""` |

The directory with `auth_username` file for Redis username (optional) and `auth` for Redis password.
In kubernetes, this is intended to read a Secret mounted as a directory.

Cannot be used together with `--redis-username` or `--redis-password`, or their respective environment variables.

### Redis Username

| | |
|---|---|
| **CLI Flag** | `--redis-username` |
| **Environment Variable** | `REDIS_USERNAME` |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `""` |

The username to connect to redis with. Prefer `--redis-creds-dir-path` for added security benefits.

### Redis Password

| | |
|---|---|
| **CLI Flag** | `--redis-password` |
| **Environment Variable** | `REDIS_PASSWORD` |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `""` |

The password to connect to redis with. Prefer `--redis-creds-dir-path` for added security benefits.

## Resource Proxy Configuration

### Enable Resource Proxy

| | |
|---|---|
| **CLI Flag** | `--enable-resource-proxy` |
| **Environment Variable** | `ARGOCD_AGENT_ENABLE_RESOURCE_PROXY` |
| **ConfigMap Entry** | `agent.resource-proxy.enable` |
| **Type** | Boolean |
| **Default** | `true` |

Enable the resource proxy to allow access to live resources on this agent cluster from the principal.

**Use Cases for Disabling:**

- Security policies that require restricted resource access
- Performance optimization when live resource viewing is not needed
- Troubleshooting resource proxy related issues

## Resource Filtering

### Label Selector

| | |
|---|---|
| **CLI Flag** | `--label-selector` |
| **Environment Variable** | `ARGOCD_AGENT_LABEL_SELECTOR` |
| **ConfigMap Entry** | `agent.label-selector` |
| **Type** | String |
| **Default** | `""` (no additional filtering) |

Kubernetes label selector that restricts which resources the agent watches.
Only resources matching this selector will be listed, watched, and processed
by the agent. This is combined with the default selector that already excludes
resources with the ignore sync label.

## Kubernetes Configuration

### Kubeconfig

| | |
|---|---|
| **CLI Flag** | `--kubeconfig` |
| **Environment Variable** | N/A |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `""` (uses in-cluster config) |

Path to a kubeconfig file to use.

### Kube Context

| | |
|---|---|
| **CLI Flag** | `--kubecontext` |
| **Environment Variable** | N/A |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `""` (uses current context) |

Override the default kube context.

## Managed Mode Options

### Ignore Unmanaged Apps

| | |
|---|---|
| **CLI Flag** | `--ignore-unmanaged-apps` |
| **Environment Variable** | `ARGOCD_AGENT_IGNORE_UNMANAGED_APPS` |
| **ConfigMap Entry** | N/A |
| **Type** | Boolean |
| **Default** | `false` |

Ignore applications without the source UID annotation during resync instead of logging errors.

In managed mode, applications created via the agent have a source UID annotation that links them to the principal. Pre-existing applications (created before the agent was installed, or created directly on the cluster) lack this annotation.

When disabled (default), the agent logs errors for applications without the annotation during resync. When enabled, these applications are silently skipped with a debug log message.

**Use Cases:**

- Clusters with pre-existing Argo CD applications not managed by the agent
- Gradual migration to agent-managed applications
- Mixed environments with both managed and unmanaged applications

**Example:**

```bash
argocd-agent agent --ignore-unmanaged-apps
```

### Source UID Mismatch Policy

| | |
|---|---|
| **CLI Flag** | `--source-uid-mismatch-policy` |
| **Environment Variable** | `ARGOCD_AGENT_SOURCE_UID_MISMATCH_POLICY` |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `recreate` |

Controls the agent's behavior when a source-UID mismatch is detected on an incoming managed resource.

In managed mode, the agent stamps each resource with a source UID annotation (`argocd.argoproj.io/source-uid`) recording the resource's Kubernetes UID on the principal. When the agent receives an event for a resource that already exists locally with a different source UID, it means the resource was deleted and recreated on the principal side. This flag determines how the agent handles that situation.

**Policies:**

- `recreate` *(default)*: Delete the existing resource on the agent, then create the incoming one. Guarantees the agent copy exactly reflects the principal state.
- `upsert`: Update the existing resource in-place without deleting it. Safer for sensitive resources where destructive replacement is undesirable.

**Per-resource override:**

Individual resources can override the global policy via the annotation `argocd.argoproj.io/source-uid-mismatch-policy` set on the resource on the principal side. The annotation value must be `recreate` or `upsert`; unknown values fall back to the global policy with a warning log.

**Use Cases:**

- Set `upsert` globally when managing sensitive resources like `argocd-gpg-keys-cm` where deletion is unacceptable
- Use the per-resource annotation to apply `upsert` selectively to specific resources while keeping `recreate` as the default

**Example:**

```bash
argocd-agent agent --source-uid-mismatch-policy=upsert
```

Per-resource annotation example (set on the principal):

```yaml
metadata:
  annotations:
    argocd.argoproj.io/source-uid-mismatch-policy: upsert
```

### On Application Recreate

| | |
|---|---|
| **CLI Flag** | `--on-application-recreate` |
| **Environment Variable** | `ARGOCD_AGENT_ON_APPLICATION_RECREATE` |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `ignore` |
| **Valid Values** | `ignore`, `clear-status`, `resync` |

Controls the agent's behavior after it reverts an unauthorized application deletion in managed mode.

When a user or external process deletes an Application directly on the agent cluster (bypassing the principal), the agent detects this as an unauthorized deletion and recreates the Application. However, if the deleted Application had the `resources-finalizer.argocd.argoproj.io` finalizer, the recreated Application inherits a stale `operationState` from the previous sync. This stale state prevents Argo CD's auto-sync from triggering, leaving the Application stuck in `OutOfSync` or `Missing` status indefinitely.

**Actions:**

- `ignore` *(default)*: Take no corrective action after recreation. The Application may remain stuck in `OutOfSync` if it had a finalizer. Use this if you want to manually investigate unauthorized deletions.
- `clear-status`: Clear the `operationState` on the recreated Application. This allows Argo CD's automated sync policy to re-trigger naturally, bringing the Application back to `Synced` and `Healthy`.
- `resync`: Set a sync operation on the recreated Application to force an immediate re-sync. This is the most aggressive option and brings the Application back to `Synced` fastest.

**Example:**

```bash
argocd-agent agent --on-application-recreate=clear-status
```

Or via environment variable:

```bash
ARGOCD_AGENT_ON_APPLICATION_RECREATE=clear-status
```

### Application Adoption

| | |
|---|---|
| **CLI Flag** | `--adoption-policy` |
| **Environment Variable** | `ARGOCD_AGENT_ADOPTION_POLICY` |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `always` |
| **Valid Values** | `always`, `never` |

This flag sets the adoption policy for existing applications in managed mode. Adoption occurs when the application that the principal is trying to create already exists on the agent.

**Policies:**

- `always`: always adopt existing applications
- `never`: never adopt existing applications

This setting can also be set on the application level by including the annotation `argocd.argoproj.io/adoption-policy` to the value of a valid policy.

**Use Case:** The main use case for this would be to transfer applications from a multi-instance Argo CD set up to using the agent set up.
