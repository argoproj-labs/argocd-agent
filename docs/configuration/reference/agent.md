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
