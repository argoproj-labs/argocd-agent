# Principal Configuration Reference

Complete reference for all principal component configuration parameters.

## Server Configuration

### Listen Host

| | |
|---|---|
| **CLI Flag** | `--listen-host` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_LISTEN_HOST` |
| **ConfigMap Entry** | `principal.listen.host` |
| **Type** | String |
| **Default** | `""` (all interfaces) |

Name of the host to listen on. Empty string means all interfaces.

### Listen Port

| | |
|---|---|
| **CLI Flag** | `--listen-port` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_LISTEN_PORT` |
| **ConfigMap Entry** | `principal.listen.port` |
| **Type** | Integer |
| **Default** | `8443` |
| **Range** | 1-65535 |

Port the gRPC server will listen on.

## Namespace Management

### Namespace

| | |
|---|---|
| **CLI Flag** | `--namespace`, `-n` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_NAMESPACE` |
| **ConfigMap Entry** | `principal.namespace` |
| **Type** | String |
| **Default** | `""` (uses pod namespace) |

The namespace the server will use for configuration.

### Allowed Namespaces

| | |
|---|---|
| **CLI Flag** | `--allowed-namespaces` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_ALLOWED_NAMESPACES` |
| **ConfigMap Entry** | `principal.allowed-namespaces` |
| **Type** | String slice (comma-separated) |
| **Default** | `[]` (empty list) |

List of namespaces the server is allowed to operate in. Supports shell-style wildcards.

**Example:** `argocd,argocd-*,production`

### Namespace Create Enable

| | |
|---|---|
| **CLI Flag** | `--namespace-create-enable` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_NAMESPACE_CREATE_ENABLE` |
| **ConfigMap Entry** | `principal.namespace-create.enable` |
| **Type** | Boolean |
| **Default** | `false` |

Whether to allow automatic namespace creation for autonomous agents.

### Namespace Create Pattern

| | |
|---|---|
| **CLI Flag** | `--namespace-create-pattern` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_NAMESPACE_CREATE_PATTERN` |
| **ConfigMap Entry** | `principal.namespace-create.pattern` |
| **Type** | String (regex pattern) |
| **Default** | `""` (no restriction) |

Only automatically create namespaces matching this regex pattern.

**Example:** `^agent-.*$`

### Namespace Create Labels

| | |
|---|---|
| **CLI Flag** | `--namespace-create-labels` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_NAMESPACE_CREATE_LABELS` |
| **ConfigMap Entry** | `principal.namespace-create.labels` |
| **Type** | String slice (comma-separated key=value) |
| **Default** | `[]` |

Labels to apply to auto-created namespaces.

**Example:** `managed-by=argocd-agent,environment=production`

## TLS Configuration

### TLS Secret Name

| | |
|---|---|
| **CLI Flag** | `--tls-secret-name` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_SECRET_NAME` |
| **ConfigMap Entry** | `principal.tls.secret-name` |
| **Type** | String |
| **Default** | `argocd-agent-principal-tls` |

Secret name of TLS certificate and key.

### TLS Certificate Path

| | |
|---|---|
| **CLI Flag** | `--tls-cert` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_SERVER_CERT_PATH` |
| **ConfigMap Entry** | `principal.tls.server.cert-path` |
| **Type** | String |
| **Default** | `""` |

Path to TLS certificate file. Overrides secret when set.

### TLS Key Path

| | |
|---|---|
| **CLI Flag** | `--tls-key` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_SERVER_KEY_PATH` |
| **ConfigMap Entry** | `principal.tls.server.key-path` |
| **Type** | String |
| **Default** | `""` |

Path to TLS private key file. Overrides secret when set.

### Insecure TLS Generate

| | |
|---|---|
| **CLI Flag** | `--insecure-tls-generate` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_SERVER_ALLOW_GENERATE` |
| **ConfigMap Entry** | `principal.tls.server.allow-generate` |
| **Type** | Boolean |
| **Default** | `false` |

Generate and use temporary TLS cert and key. **Development only.**

### Insecure Plaintext Mode

| | |
|---|---|
| **CLI Flag** | `--insecure-plaintext` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_INSECURE_PLAINTEXT` |
| **ConfigMap Entry** | `principal.tls.insecure-plaintext` |
| **Type** | Boolean |
| **Default** | `false` |

Run gRPC server without TLS. **Required for service mesh deployments with header authentication.**

### TLS CA Secret Name

| | |
|---|---|
| **CLI Flag** | `--tls-ca-secret-name` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_SECRET_NAME` |
| **ConfigMap Entry** | `principal.tls.server.root-ca-secret-name` |
| **Type** | String |
| **Default** | `argocd-agent-ca` |

Secret name of TLS CA certificate.

### Root CA Path

| | |
|---|---|
| **CLI Flag** | `--root-ca-path` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_PATH` |
| **ConfigMap Entry** | `principal.tls.server.root-ca-path` |
| **Type** | String |
| **Default** | `""` |

Path to file containing root CA certificate for verifying client certs.

### Require Client Certificates

| | |
|---|---|
| **CLI Flag** | `--require-client-certs` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_CLIENT_CERT_REQUIRE` |
| **ConfigMap Entry** | `principal.tls.client-cert.require` |
| **Type** | Boolean |
| **Default** | `false` |

Whether to require agents to present a client certificate.

### Client Certificate Subject Match

| | |
|---|---|
| **CLI Flag** | `--client-cert-subject-match` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_CLIENT_CERT_MATCH_SUBJECT` |
| **ConfigMap Entry** | `principal.tls.client-cert.match-subject` |
| **Type** | Boolean |
| **Default** | `false` |

Whether a client cert's subject must match the agent name.

### TLS Minimum Version

| | |
|---|---|
| **CLI Flag** | `--tls-min-version` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_MIN_VERSION` |
| **ConfigMap Entry** | `principal.tls.min-version` |
| **Type** | String |
| **Default** | `tls1.3` |
| **Valid Values** | `tls1.1`, `tls1.2`, `tls1.3` |

Minimum TLS version to accept from connecting agents.

### TLS Maximum Version

| | |
|---|---|
| **CLI Flag** | `--tls-max-version` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_MAX_VERSION` |
| **ConfigMap Entry** | `principal.tls.max-version` |
| **Type** | String |
| **Default** | `""` (highest available) |
| **Valid Values** | `tls1.1`, `tls1.2`, `tls1.3` |

Maximum TLS version to accept from connecting agents.

### TLS Cipher Suites

| | |
|---|---|
| **CLI Flag** | `--tls-ciphersuites` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_TLS_CIPHERSUITES` |
| **ConfigMap Entry** | `principal.tls.ciphersuites` |
| **Type** | String (comma-separated) |
| **Default** | `""` (Go defaults) |

Comma-separated list of TLS cipher suites to use. Use `--tls-ciphersuites=list` to display available options.

## Resource Proxy Configuration

### Enable Resource Proxy

| | |
|---|---|
| **CLI Flag** | `--enable-resource-proxy` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_ENABLE_RESOURCE_PROXY` |
| **ConfigMap Entry** | N/A |
| **Type** | Boolean |
| **Default** | `true` |

Whether to enable the resource proxy.

### Resource Proxy Secret Name

| | |
|---|---|
| **CLI Flag** | `--resource-proxy-secret-name` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_RESOURCE_PROXY_SECRET_NAME` |
| **ConfigMap Entry** | `principal.resource-proxy.secret-name` |
| **Type** | String |
| **Default** | `argocd-agent-resource-proxy-tls` |

Secret name of the resource proxy TLS certificate.

### Resource Proxy Certificate Path

| | |
|---|---|
| **CLI Flag** | `--resource-proxy-cert-path` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_RESOURCE_PROXY_TLS_CERT_PATH` |
| **ConfigMap Entry** | `principal.resource-proxy.tls.cert-path` |
| **Type** | String |
| **Default** | `""` |

Path to file containing the resource proxy's TLS certificate.

### Resource Proxy Key Path

| | |
|---|---|
| **CLI Flag** | `--resource-proxy-key-path` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_RESOURCE_PROXY_TLS_KEY_PATH` |
| **ConfigMap Entry** | `principal.resource-proxy.tls.key-path` |
| **Type** | String |
| **Default** | `""` |

Path to file containing the resource proxy's TLS private key.

### Resource Proxy CA Secret Name

| | |
|---|---|
| **CLI Flag** | `--resource-proxy-ca-secret-name` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_RESOURCE_PROXY_CA_SECRET_NAME` |
| **ConfigMap Entry** | `principal.resource-proxy.ca.secret-name` |
| **Type** | String |
| **Default** | `argocd-agent-ca` |

Secret name of the resource proxy's CA certificate.

### Resource Proxy CA Path

| | |
|---|---|
| **CLI Flag** | `--resource-proxy-ca-path` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_RESOURCE_PROXY_TLS_CA_PATH` |
| **ConfigMap Entry** | `principal.resource-proxy.ca.path` |
| **Type** | String |
| **Default** | `""` |

Path to file containing the resource proxy's TLS CA data.

## JWT Configuration

### JWT Secret Name

| | |
|---|---|
| **CLI Flag** | `--jwt-secret-name` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_JWT_SECRET_NAME` |
| **ConfigMap Entry** | `principal.jwt.secret-name` |
| **Type** | String |
| **Default** | `argocd-agent-jwt` |

Secret name of the JWT signing key.

### JWT Key Path

| | |
|---|---|
| **CLI Flag** | `--jwt-key` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_JWT_KEY_PATH` |
| **ConfigMap Entry** | `principal.jwt.key-path` |
| **Type** | String |
| **Default** | `""` |

Path to JWT signing key file. Overrides secret when set.

### Insecure JWT Generate

| | |
|---|---|
| **CLI Flag** | `--insecure-jwt-generate` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_JWT_ALLOW_GENERATE` |
| **ConfigMap Entry** | `principal.jwt.allow-generate` |
| **Type** | Boolean |
| **Default** | `false` |

Generate and use temporary JWT signing key. **Development only.**

## Authentication Configuration

### Authentication Method

| | |
|---|---|
| **CLI Flag** | `--auth` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_AUTH` |
| **ConfigMap Entry** | `principal.auth` |
| **Type** | String |
| **Default** | `""` |
| **Format** | `<method>:<configuration>` |

Authentication method and corresponding configuration.

**Valid Methods:**

| Method | Format | Description |
|--------|--------|-------------|
| `mtls` | `mtls:<regex>` | Mutual TLS authentication. Regex extracts agent ID from certificate subject. |
| `header` | `header:<header-name>:<regex>` | Header-based authentication. First capture group becomes agent ID. |
| `userpass` | `userpass:<path>` | **[DEPRECATED]** Username/password authentication. |

**Examples:**

- mTLS: `mtls:CN=([^,]+)`
- Istio: `header:x-forwarded-client-cert:^.*URI=spiffe://[^/]+/ns/[^/]+/sa/([^,;]+)`
- Custom header: `header:x-client-id:^(.+)$`

## Logging and Debugging

### Log Level

| | |
|---|---|
| **CLI Flag** | `--log-level` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_LOG_LEVEL` |
| **ConfigMap Entry** | `principal.log.level` |
| **Type** | String |
| **Default** | `info` |
| **Valid Values** | `trace`, `debug`, `info`, `warn`, `error` |

The log level to use.

### Log Format

| | |
|---|---|
| **CLI Flag** | `--log-format` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_LOG_FORMAT` |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `text` |
| **Valid Values** | `text`, `json` |

The log format to use.

### Profiling Port

| | |
|---|---|
| **CLI Flag** | `--pprof-port` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_PPROF_PORT` |
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
| **Environment Variable** | `ARGOCD_PRINCIPAL_METRICS_PORT` |
| **ConfigMap Entry** | `principal.metrics.port` |
| **Type** | Integer |
| **Default** | `8000` |
| **Range** | 1024-65535 |

Port the metrics server will listen on.

### Health Check Port

| | |
|---|---|
| **CLI Flag** | `--healthz-port` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_HEALTH_CHECK_PORT` |
| **ConfigMap Entry** | `principal.healthz.port` |
| **Type** | Integer |
| **Default** | `8003` |
| **Range** | 1024-65535 |

Port the health check server will listen on.

## Network and Performance

### Enable WebSocket

| | |
|---|---|
| **CLI Flag** | `--enable-websocket` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET` |
| **ConfigMap Entry** | N/A |
| **Type** | Boolean |
| **Default** | `false` |

Use gRPC over WebSocket to stream events to agents.

### Keep Alive Minimum Interval

| | |
|---|---|
| **CLI Flag** | `--keepalive-min-interval` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_KEEP_ALIVE_MIN_INTERVAL` |
| **ConfigMap Entry** | N/A |
| **Type** | Duration |
| **Default** | `0` (disabled) |

Drop agent connections that send keepalive pings more often than specified interval.

**Example:** `30s`

## Redis Configuration

### Redis Server Address

| | |
|---|---|
| **CLI Flag** | `--redis-server-address` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS` |
| **ConfigMap Entry** | `principal.redis.server.address` |
| **Type** | String |
| **Default** | `argocd-redis:6379` |

Redis server hostname and port.

### Redis Compression Type

| | |
|---|---|
| **CLI Flag** | `--redis-compression-type` |
| **Environment Variable** | `ARGOCD_PRINCIPAL_REDIS_COMPRESSION_TYPE` |
| **ConfigMap Entry** | N/A |
| **Type** | String |
| **Default** | `gzip` |
| **Valid Values** | `gzip`, `none` |

Compression algorithm required by Redis.

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
