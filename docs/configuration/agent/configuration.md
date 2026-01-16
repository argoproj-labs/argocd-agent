# Agent Component Configuration

The argocd-agent **agent** component runs on workload clusters and connects to the principal component running on the control plane cluster. The agent can be configured using command line flags, environment variables, or entries in a Kubernetes ConfigMap.

## Configuration Methods

Configuration parameters can be specified in three ways, listed in order of precedence:

1. **Command line flags** (highest precedence)
2. **Environment variables** (medium precedence)  
3. **ConfigMap entries** (lowest precedence, recommended for production)

The recommended approach for production deployments is to use ConfigMap entries mounted to the agent deployment.

## Core Configuration Parameters

### Server Connection

#### Server Address
- **Command Line Flag**: `--server-address`
- **Environment Variable**: `ARGOCD_AGENT_REMOTE_SERVER`
- **ConfigMap Entry**: `agent.server.address`
- **Description**: Address of the principal server to connect to
- **Type**: String
- **Default**: `""` (empty)
- **Required**: Yes
- **Example**: `"argocd-agent-principal.example.com"`

#### Server Port
- **Command Line Flag**: `--server-port`
- **Environment Variable**: `ARGOCD_AGENT_REMOTE_PORT`
- **ConfigMap Entry**: `agent.server.port`
- **Description**: Port on the principal server to connect to
- **Type**: Integer
- **Default**: `443`
- **Range**: 1-65535
- **Example**: `"8443"`

### Agent Operation

#### Agent Mode
- **Command Line Flag**: `--agent-mode`
- **Environment Variable**: `ARGOCD_AGENT_MODE`
- **ConfigMap Entry**: `agent.mode`
- **Description**: Mode of operation for the agent
- **Type**: String
- **Default**: `"autonomous"`
- **Valid Values**: `"autonomous"`, `"managed"`
- **Example**: `"autonomous"`

#### Namespace
- **Command Line Flag**: `--namespace` or `-n`
- **Environment Variable**: `ARGOCD_AGENT_NAMESPACE`
- **ConfigMap Entry**: `agent.namespace`
- **Description**: Namespace to manage applications in
- **Type**: String
- **Default**: `"argocd"`
- **Required**: Yes
- **Example**: `"argocd"`

#### Credentials
- **Command Line Flag**: `--creds`
- **Environment Variable**: `ARGOCD_AGENT_CREDS`
- **ConfigMap Entry**: `agent.creds`
- **Description**: Credentials to use when connecting to server
- **Type**: String
- **Default**: `""` (empty)
- **Format**: `<method>:<configuration>`
- **Valid Methods**:
  - `userpass:/path/to/creds/file` - Username/password authentication **[DEPRECATED - not suited for use outside development environments]**
  - `mtls:regex_pattern` - Mutual TLS authentication with agent ID extraction **(Recommended)**
- **Example**: `"mtls:^CN=(.+)$"`

### TLS Configuration

#### Insecure TLS
- **Command Line Flag**: `--insecure-tls`
- **Environment Variable**: `ARGOCD_AGENT_TLS_INSECURE`
- **ConfigMap Entry**: `agent.tls.client.insecure`
- **Description**: Skip verification of remote TLS certificate (INSECURE)
- **Type**: Boolean
- **Default**: `false`
- **Security Warning**: Only use for development purposes
- **Example**: `"false"`

#### Root CA Secret Name
- **Command Line Flag**: `--root-ca-secret-name`
- **Environment Variable**: `ARGOCD_AGENT_TLS_ROOT_CA_SECRET_NAME`
- **ConfigMap Entry**: `agent.tls.root-ca-secret-name`
- **Description**: Name of the secret containing the root CA certificate
- **Type**: String
- **Default**: `"argocd-agent-ca"`
- **Example**: `"argocd-agent-ca"`

#### Root CA Path
- **Command Line Flag**: `--root-ca-path`
- **Environment Variable**: `ARGOCD_AGENT_TLS_ROOT_CA_PATH`
- **ConfigMap Entry**: `agent.tls.root-ca-path`
- **Description**: Path to file containing root CA certificate for verifying remote TLS
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/ca.crt"`

#### TLS Secret Name
- **Command Line Flag**: `--tls-secret-name`
- **Environment Variable**: `ARGOCD_AGENT_TLS_SECRET_NAME`
- **ConfigMap Entry**: `agent.tls.secret-name`
- **Description**: Name of the secret containing the TLS client certificate
- **Type**: String
- **Default**: `"argocd-agent-client-tls"`
- **Example**: `"argocd-agent-client-tls"`

#### TLS Client Certificate
- **Command Line Flag**: `--tls-client-cert`
- **Environment Variable**: `ARGOCD_AGENT_TLS_CLIENT_CERT_PATH`
- **ConfigMap Entry**: `agent.tls.client.cert-path`
- **Description**: Path to TLS client certificate file
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/client.crt"`

#### TLS Client Key
- **Command Line Flag**: `--tls-client-key`
- **Environment Variable**: `ARGOCD_AGENT_TLS_CLIENT_KEY_PATH`
- **ConfigMap Entry**: `agent.tls.client.key-path`
- **Description**: Path to TLS client private key file
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/client.key"`

#### TLS Minimum Version
- **Command Line Flag**: `--tls-min-version`
- **Environment Variable**: `ARGOCD_AGENT_TLS_MIN_VERSION`
- **ConfigMap Entry**: `agent.tls.min-version`
- **Description**: Minimum TLS version to use when connecting to the principal
- **Type**: String
- **Default**: `""` (uses Go default)
- **Valid Values**: `"tls1.1"`, `"tls1.2"`, `"tls1.3"`
- **Example**: `"tls1.2"`

#### TLS Maximum Version
- **Command Line Flag**: `--tls-max-version`
- **Environment Variable**: `ARGOCD_AGENT_TLS_MAX_VERSION`
- **ConfigMap Entry**: `agent.tls.max-version`
- **Description**: Maximum TLS version to use when connecting to the principal
- **Type**: String
- **Default**: `""` (uses highest available)
- **Valid Values**: `"tls1.1"`, `"tls1.2"`, `"tls1.3"`
- **Example**: `"tls1.3"`

#### TLS Cipher Suites
- **Command Line Flag**: `--tls-ciphersuites`
- **Environment Variable**: `ARGOCD_AGENT_TLS_CIPHERSUITES`
- **ConfigMap Entry**: `agent.tls.ciphersuites`
- **Description**: Comma-separated list of TLS cipher suites to use
- **Type**: String (comma-separated list)
- **Default**: `""` (uses Go defaults)
- **Note**: Use `--tls-ciphersuites=list` to display all available cipher suites and their supported TLS versions
- **Example**: `"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"`

### Logging and Debugging

#### Log Level
- **Command Line Flag**: `--log-level`
- **Environment Variable**: `ARGOCD_AGENT_LOG_LEVEL`
- **ConfigMap Entry**: `agent.log.level`
- **Description**: The log level for the agent
- **Type**: String
- **Default**: `"info"`
- **Valid Values**: `"trace"`, `"debug"`, `"info"`, `"warn"`, `"error"`
- **Example**: `"info"`

#### Log Format
- **Command Line Flag**: `--log-format`
- **Environment Variable**: `ARGOCD_PRINCIPAL_LOG_FORMAT`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: The log format to use
- **Type**: String
- **Default**: `"text"`
- **Valid Values**: `"text"`, `"json"`
- **Example**: `"text"`

#### Profiling Port
- **Command Line Flag**: `--pprof-port`
- **Environment Variable**: `ARGOCD_AGENT_PPROF_PORT`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Port the pprof server will listen on (0 disables)
- **Type**: Integer
- **Default**: `0` (disabled)
- **Range**: 0, 1024-65535
- **Example**: `6060`

### Monitoring and Health

#### Metrics Port
- **Command Line Flag**: `--metrics-port`
- **Environment Variable**: `ARGOCD_AGENT_METRICS_PORT`
- **ConfigMap Entry**: `agent.metrics.port`
- **Description**: Port the metrics server will listen on
- **Type**: Integer
- **Default**: `8181`
- **Range**: 1024-65535
- **Example**: `"8181"`

#### Health Check Port
- **Command Line Flag**: `--healthz-port`
- **Environment Variable**: `ARGOCD_AGENT_HEALTH_CHECK_PORT`
- **ConfigMap Entry**: `agent.healthz.port`
- **Description**: Port the health check server will listen on
- **Type**: Integer
- **Default**: `8001`
- **Range**: 1024-65535
- **Example**: `"8001"`

### Network and Performance

#### Enable WebSocket
- **Command Line Flag**: `--enable-websocket`
- **Environment Variable**: `ARGOCD_AGENT_ENABLE_WEBSOCKET`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Use gRPC over WebSocket to stream events to the Principal
- **Type**: Boolean
- **Default**: `false`
- **Example**: `"false"`

#### Keep Alive Ping Interval
- **Command Line Flag**: `--keep-alive-ping-interval`
- **Environment Variable**: `ARGOCD_AGENT_KEEP_ALIVE_PING_INTERVAL`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: HTTP/2 PING frame interval to detect dead connections (transport-level keepalive)
- **Type**: Duration
- **Default**: `0` (disabled)
- **Format**: Duration string (e.g., "30s", "5m", "1h")
- **Example**: `"30s"`
- **Note**: This uses HTTP/2 PING frames which do NOT count as requests for service mesh idle timeouts. For service mesh deployments (like Istio), use `--heartbeat-interval` instead.

#### Heartbeat Interval
- **Command Line Flag**: `--heartbeat-interval`
- **Environment Variable**: `ARGOCD_AGENT_HEARTBEAT_INTERVAL`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Interval for application-level heartbeat messages over the Subscribe stream. Sends ping events that count as requests to prevent service mesh idle timeouts (e.g., Istio's `idleTimeout`). Set to a value less than your service mesh's idle timeout.
- **Type**: Duration
- **Default**: `0` (disabled)
- **Format**: Duration string (e.g., "30s", "5m", "1h")
- **Example**: `"30s"`
- **Recommended for**: Deployments behind service meshes like Istio, Linkerd, or other proxies with idle connection timeouts
- **Best Practice**: Set to 50-75% of your service mesh's `idleTimeout` (e.g., if Istio `idleTimeout` is 60s, use 30-45s)

#### Enable Compression
- **Command Line Flag**: `--enable-compression`
- **Environment Variable**: `ARGOCD_AGENT_ENABLE_COMPRESSION`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Use compression while sending data between Principal and Agent using gRPC
- **Type**: Boolean
- **Default**: `false`
- **Example**: `"false"`

### Redis Configuration

#### Redis Address
- **Command Line Flag**: `--redis-addr`
- **Environment Variable**: `REDIS_ADDR`
- **ConfigMap Entry**: `agent.redis.address`
- **Description**: The redis host to connect to
- **Type**: String
- **Default**: `"argocd-redis:6379"`
- **Example**: `"argocd-redis:6379"`

#### Redis Username
- **Command Line Flag**: `--redis-username`
- **Environment Variable**: `REDIS_USERNAME`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: The username to connect to redis with
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"redis-user"`

#### Redis Password
- **Command Line Flag**: `--redis-password`
- **Environment Variable**: `REDIS_PASSWORD`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: The password to connect to redis with
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"redis-password"`

### Resource Proxy Configuration

#### Enable Resource Proxy
- **Command Line Flag**: `--enable-resource-proxy`
- **Environment Variable**: `ARGOCD_AGENT_ENABLE_RESOURCE_PROXY`
- **ConfigMap Entry**: `agent.resource-proxy.enable`
- **Description**: Enable the resource proxy to allow access to live resources on this agent cluster from the principal
- **Type**: Boolean
- **Default**: `true`
- **Example**: `"true"`
- **Use Cases for Disabling**:
  - Security policies that require restricted resource access
  - Performance optimization when live resource viewing is not needed
  - Troubleshooting resource proxy related issues

### Kubernetes Configuration

#### Kubeconfig
- **Command Line Flag**: `--kubeconfig`
- **Environment Variable**: *(Not available)*
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Path to a kubeconfig file to use
- **Type**: String
- **Default**: `""` (uses in-cluster config)
- **Example**: `"/home/user/.kube/config"`

#### Kube Context
- **Command Line Flag**: `--kubecontext`
- **Environment Variable**: *(Not available)*
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Override the default kube context
- **Type**: String
- **Default**: `""` (uses current context)
- **Example**: `"my-cluster-context"`



## Configuration Examples

### Using Command Line Flags
```bash
argocd-agent agent \
  --server-address=argocd-agent-principal.example.com \
  --server-port=8443 \
  --agent-mode=autonomous \
  --namespace=argocd \
  --log-level=info \
  --enable-resource-proxy=true
```

### Using Environment Variables
```bash
export ARGOCD_AGENT_REMOTE_SERVER=argocd-agent-principal.example.com
export ARGOCD_AGENT_REMOTE_PORT=8443
export ARGOCD_AGENT_MODE=autonomous
export ARGOCD_AGENT_NAMESPACE=argocd
export ARGOCD_AGENT_LOG_LEVEL=info
export ARGOCD_AGENT_ENABLE_RESOURCE_PROXY=true
argocd-agent agent
```

### Using ConfigMap (Recommended)
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
data:
  agent.server.address: "argocd-agent-principal.example.com"
  agent.server.port: "8443"
  agent.mode: "autonomous"
  agent.namespace: "argocd"
  agent.log.level: "info"
  agent.creds: "mtls:^CN=(.+)$"  # Use mTLS (recommended); userpass is deprecated
  agent.tls.client.insecure: "false"
  agent.tls.root-ca-secret-name: "argocd-agent-ca"
  agent.tls.secret-name: "argocd-agent-client-tls"
  agent.tls.min-version: ""
  agent.tls.max-version: ""
  agent.tls.ciphersuites: ""
  agent.metrics.port: "8181"
  agent.healthz.port: "8001"
```

The ConfigMap should be mounted to the agent container and the parameters will be automatically read by the agent on startup.

## Service Mesh Considerations

When running argocd-agent behind a service mesh like Istio, Linkerd, or other proxies, you may encounter connection timeout issues. Service meshes typically have idle connection timeouts that close connections without recent HTTP activity.

### Understanding Keepalive Options

argocd-agent provides two keepalive mechanisms:

| Option | Type | Counts as Request | Use Case |
|--------|------|-------------------|----------|
| `--keep-alive-ping-interval` | HTTP/2 PING frames | No | Detecting dead TCP connections |
| `--heartbeat-interval` | Application events | Yes | Preventing service mesh idle timeouts |

### Istio Configuration Example

Istio's `DestinationRule` allows configuring `idleTimeout` for HTTP connections. Per [Istio documentation](https://istio.io/latest/docs/reference/config/networking/destination-rule/#ConnectionPoolSettings-HTTPSettings), HTTP/2 PINGs do not keep connections alive for request-based timeouts.

**Recommended agent configuration for Istio:**
```bash
argocd-agent agent \
  --heartbeat-interval=30s \
  --keep-alive-ping-interval=30s
```

Or via environment variables:
```yaml
env:
  - name: ARGOCD_AGENT_HEARTBEAT_INTERVAL
    value: "30s"
  - name: ARGOCD_AGENT_KEEP_ALIVE_PING_INTERVAL
    value: "30s"
```

**Istio DestinationRule (optional, to increase timeout):**
```yaml
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: argocd-agent-principal
spec:
  host: argocd-agent-principal.argocd.svc.cluster.local
  trafficPolicy:
    connectionPool:
      http:
        idleTimeout: 300s  # 5 minutes
```

### Troubleshooting Connection Issues

If you see errors like:
- `stream terminated by RST_STREAM with error code: NO_ERROR`
- `context canceled` on the principal side
- Frequent reconnections in agent logs

These often indicate idle timeout issues. Enable heartbeats with:
```bash
--heartbeat-interval=30s
```

## Security Considerations

- Always use TLS certificates in production (`agent.tls.client.insecure: "false"`)
- Store sensitive configuration like credentials in Kubernetes Secrets, not ConfigMaps
- Use mutual TLS (`mtls`) authentication when possible for enhanced security
- Regularly rotate TLS certificates and authentication credentials
- Restrict network access to the agent's metrics and health endpoints
- Consider disabling resource proxy (`--enable-resource-proxy=false`) if live resource access is not required for enhanced security isolation
- Configure appropriate TLS minimum version (`tls.min-version`) based on your security requirements
- Use `--tls-ciphersuites=list` to view available cipher suites before configuring custom cipher suites

## Resource Proxy Considerations

When the resource proxy is **enabled** (default):
- Users can view live resources for applications on this agent cluster through the Argo CD UI
- The agent processes resource requests from the principal and proxies them to the local Kubernetes API
- All resource access is limited to resources managed by Argo CD applications

When the resource proxy is **disabled**:
- Live resource viewing will not work for applications on this agent cluster
- The Argo CD UI will show application status but not allow inspection of individual resources
- Application synchronization and management operations continue to work normally
- Reduces attack surface and network communication between principal and agent

For detailed information about how the resource proxy works and additional configuration options, see the [Live Resources](../../user-guide/live-resources.md) user guide. 
