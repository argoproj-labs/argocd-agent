# Principal Component Configuration

The argocd-agent **principal** component runs on the control plane cluster and manages connections from agent components running on workload clusters. The principal can be configured using command line flags, environment variables, or entries in a Kubernetes ConfigMap.

## Configuration Methods

Configuration parameters can be specified in three ways, listed in order of precedence:

1. **Command line flags** (highest precedence)
2. **Environment variables** (medium precedence)
3. **ConfigMap entries** (lowest precedence, recommended for production)

The recommended approach for production deployments is to use ConfigMap entries mounted to the principal deployment.

## Core Configuration Parameters

### Server Configuration

#### Listen Host
- **Command Line Flag**: `--listen-host`
- **Environment Variable**: `ARGOCD_PRINCIPAL_LISTEN_HOST`
- **ConfigMap Entry**: `principal.listen.host`
- **Description**: Name of the host to listen on (empty for all interfaces)
- **Type**: String
- **Default**: `""` (all interfaces)
- **Example**: `"0.0.0.0"`

#### Listen Port
- **Command Line Flag**: `--listen-port`
- **Environment Variable**: `ARGOCD_PRINCIPAL_LISTEN_PORT`
- **ConfigMap Entry**: `principal.listen.port`
- **Description**: Port the gRPC server will listen on
- **Type**: Integer
- **Default**: `8443`
- **Range**: 1-65535
- **Example**: `"8443"`

### Namespace Management

#### Namespace
- **Command Line Flag**: `--namespace` or `-n`
- **Environment Variable**: `ARGOCD_PRINCIPAL_NAMESPACE`
- **ConfigMap Entry**: `principal.namespace`
- **Description**: The namespace the server will use for configuration
- **Type**: String
- **Default**: `""` (uses pod namespace when running in-cluster)
- **Example**: `"argocd"`

#### Allowed Namespaces
- **Command Line Flag**: `--allowed-namespaces`
- **Environment Variable**: `ARGOCD_PRINCIPAL_ALLOWED_NAMESPACES`
- **ConfigMap Entry**: `principal.allowed-namespaces`
- **Description**: List of namespaces the server is allowed to operate in
- **Type**: String slice (comma-separated)
- **Default**: `[]` (empty list)
- **Format**: Comma-separated list, supports shell-style wildcards
- **Example**: `"argocd,argocd-*,production"`

#### Namespace Create Enable
- **Command Line Flag**: `--namespace-create-enable`
- **Environment Variable**: `ARGOCD_PRINCIPAL_NAMESPACE_CREATE_ENABLE`
- **ConfigMap Entry**: `principal.namespace-create.enable`
- **Description**: Whether to allow automatic namespace creation for autonomous agents
- **Type**: Boolean
- **Default**: `false`
- **Example**: `"true"`

#### Namespace Create Pattern
- **Command Line Flag**: `--namespace-create-pattern`
- **Environment Variable**: `ARGOCD_PRINCIPAL_NAMESPACE_CREATE_PATTERN`
- **ConfigMap Entry**: `principal.namespace-create.pattern`
- **Description**: Only automatically create namespaces matching this regex pattern
- **Type**: String (regex pattern)
- **Default**: `""` (no restriction)
- **Example**: `"^agent-.*$"`

#### Namespace Create Labels
- **Command Line Flag**: `--namespace-create-labels`
- **Environment Variable**: `ARGOCD_PRINCIPAL_NAMESPACE_CREATE_LABELS`
- **ConfigMap Entry**: `principal.namespace-create.labels`
- **Description**: Labels to apply to auto-created namespaces
- **Type**: String slice (comma-separated key=value pairs)
- **Default**: `[]` (empty list)
- **Format**: `key1=value1,key2=value2`
- **Example**: `"managed-by=argocd-agent,environment=production"`

### TLS Configuration

#### TLS Secret Name
- **Command Line Flag**: `--tls-secret-name`
- **Environment Variable**: `ARGOCD_PRINCIPAL_TLS_SECRET_NAME`
- **ConfigMap Entry**: `principal.tls.secret-name`
- **Description**: Secret name of TLS certificate and key
- **Type**: String
- **Default**: `"argocd-agent-principal-tls"`
- **Example**: `"argocd-agent-principal-tls"`

#### TLS Certificate Path
- **Command Line Flag**: `--tls-cert`
- **Environment Variable**: `ARGOCD_PRINCIPAL_TLS_SERVER_CERT_PATH`
- **ConfigMap Entry**: `principal.tls.server.cert-path`
- **Description**: Use TLS certificate from path
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/server.crt"`

#### TLS Key Path
- **Command Line Flag**: `--tls-key`
- **Environment Variable**: `ARGOCD_PRINCIPAL_TLS_SERVER_KEY_PATH`
- **ConfigMap Entry**: `principal.tls.server.key-path`
- **Description**: Use TLS private key from path
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/server.key"`

#### Insecure TLS Generate
- **Command Line Flag**: `--insecure-tls-generate`
- **Environment Variable**: `ARGOCD_PRINCIPAL_TLS_SERVER_ALLOW_GENERATE`
- **ConfigMap Entry**: `principal.tls.server.allow-generate`
- **Description**: Generate and use temporary TLS cert and key (INSECURE)
- **Type**: Boolean
- **Default**: `false`
- **Security Warning**: Only use for development purposes
- **Example**: `"false"`

#### Insecure Plaintext Mode
- **Command Line Flag**: `--insecure-plaintext`
- **Environment Variable**: `ARGOCD_PRINCIPAL_INSECURE_PLAINTEXT`
- **ConfigMap Entry**: `principal.tls.insecure-plaintext`
- **Description**: Run gRPC server without TLS (use with service mesh like Istio)
- **Type**: Boolean
- **Default**: `false`
- **Security Warning**: Only use when running behind a service mesh that handles mTLS termination at the sidecar level. Required when using `header` authentication method.
- **Example**: `"true"`

#### TLS CA Secret Name
- **Command Line Flag**: `--tls-ca-secret-name`
- **Environment Variable**: `ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_SECRET_NAME`
- **ConfigMap Entry**: `principal.tls.server.root-ca-secret-name`
- **Description**: Secret name of TLS CA certificate
- **Type**: String
- **Default**: `"argocd-agent-ca"`
- **Example**: `"argocd-agent-ca"`

#### Root CA Path
- **Command Line Flag**: `--root-ca-path`
- **Environment Variable**: `ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_PATH`
- **ConfigMap Entry**: `principal.tls.server.root-ca-path`
- **Description**: Path to file containing root CA certificate for verifying client certs
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/ca.crt"`

#### Require Client Certificates
- **Command Line Flag**: `--require-client-certs`
- **Environment Variable**: `ARGOCD_PRINCIPAL_TLS_CLIENT_CERT_REQUIRE`
- **ConfigMap Entry**: `principal.tls.client-cert.require`
- **Description**: Whether to require agents to present a client certificate
- **Type**: Boolean
- **Default**: `false`
- **Example**: `"true"`

#### Client Certificate Subject Match
- **Command Line Flag**: `--client-cert-subject-match`
- **Environment Variable**: `ARGOCD_PRINCIPAL_TLS_CLIENT_CERT_MATCH_SUBJECT`
- **ConfigMap Entry**: `principal.tls.client-cert.match-subject`
- **Description**: Whether a client cert's subject must match the agent name
- **Type**: Boolean
- **Default**: `false`
- **Example**: `"true"`

### Resource Proxy Configuration

#### Enable Resource Proxy
- **Command Line Flag**: `--enable-resource-proxy`
- **Environment Variable**: `ARGOCD_PRINCIPAL_ENABLE_RESOURCE_PROXY`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Whether to enable the resource proxy
- **Type**: Boolean
- **Default**: `true`
- **Example**: `"true"`

#### Resource Proxy Secret Name
- **Command Line Flag**: `--resource-proxy-secret-name`
- **Environment Variable**: `ARGOCD_PRINCIPAL_RESOURCE_PROXY_SECRET_NAME`
- **ConfigMap Entry**: `principal.resource-proxy.secret-name`
- **Description**: Secret name of the resource proxy TLS certificate
- **Type**: String
- **Default**: `"argocd-agent-resource-proxy-tls"`
- **Example**: `"argocd-agent-resource-proxy-tls"`

#### Resource Proxy Certificate Path
- **Command Line Flag**: `--resource-proxy-cert-path`
- **Environment Variable**: `ARGOCD_PRINCIPAL_RESOURCE_PROXY_TLS_CERT_PATH`
- **ConfigMap Entry**: `principal.resource-proxy.tls.cert-path`
- **Description**: Path to file containing the resource proxy's TLS certificate
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/proxy.crt"`

#### Resource Proxy Key Path
- **Command Line Flag**: `--resource-proxy-key-path`
- **Environment Variable**: `ARGOCD_PRINCIPAL_RESOURCE_PROXY_TLS_KEY_PATH`
- **ConfigMap Entry**: `principal.resource-proxy.tls.key-path`
- **Description**: Path to file containing the resource proxy's TLS private key
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/proxy.key"`

#### Resource Proxy CA Secret Name
- **Command Line Flag**: `--resource-proxy-ca-secret-name`
- **Environment Variable**: `ARGOCD_PRINCIPAL_RESOURCE_PROXY_CA_SECRET_NAME`
- **ConfigMap Entry**: `principal.resource-proxy.ca.secret-name`
- **Description**: Secret name of the resource proxy's CA certificate
- **Type**: String
- **Default**: `"argocd-agent-ca"`
- **Example**: `"argocd-agent-ca"`

#### Resource Proxy CA Path
- **Command Line Flag**: `--resource-proxy-ca-path`
- **Environment Variable**: `ARGOCD_PRINCIPAL_RESOURCE_PROXY_TLS_CA_PATH`
- **ConfigMap Entry**: `principal.resource-proxy.ca.path`
- **Description**: Path to file containing the resource proxy's TLS CA data
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/proxy-ca.crt"`

### JWT Configuration

#### JWT Secret Name
- **Command Line Flag**: `--jwt-secret-name`
- **Environment Variable**: `ARGOCD_PRINCIPAL_JWT_SECRET_NAME`
- **ConfigMap Entry**: `principal.jwt.secret-name`
- **Description**: Secret name of the JWT signing key
- **Type**: String
- **Default**: `"argocd-agent-jwt"`
- **Example**: `"argocd-agent-jwt"`

#### JWT Key Path
- **Command Line Flag**: `--jwt-key`
- **Environment Variable**: `ARGOCD_PRINCIPAL_JWT_KEY_PATH`
- **ConfigMap Entry**: `principal.jwt.key-path`
- **Description**: Use JWT signing key from path
- **Type**: String
- **Default**: `""` (empty)
- **Example**: `"/app/certs/jwt.key"`

#### Insecure JWT Generate
- **Command Line Flag**: `--insecure-jwt-generate`
- **Environment Variable**: `ARGOCD_PRINCIPAL_JWT_ALLOW_GENERATE`
- **ConfigMap Entry**: `principal.jwt.allow-generate`
- **Description**: Generate and use temporary JWT signing key (INSECURE)
- **Type**: Boolean
- **Default**: `false`
- **Security Warning**: Only use for development purposes
- **Example**: `"false"`

### Authentication Configuration

#### Authentication Method
- **Command Line Flag**: `--auth`
- **Environment Variable**: `ARGOCD_PRINCIPAL_AUTH`
- **ConfigMap Entry**: `principal.auth`
- **Description**: Authentication method and corresponding configuration
- **Type**: String
- **Default**: `""` (empty)
- **Format**: `<method>:<configuration>`
- **Valid Methods**:
  - `userpass:/path/to/creds/file` - Username/password authentication **[DEPRECATED - not suited for use outside development environments]**
  - `mtls:regex_pattern` - Mutual TLS authentication with agent ID extraction
  - `header:<header-name>:<extraction-regex>` - Generic header-based authentication **(Recommended for Istio)**
    - Extracts agent ID from any HTTP header using a regex pattern
    - First capture group becomes the agent ID
    - Must be used with `--insecure-plaintext` (service mesh handles mTLS)
- **Examples**:
  - Istio with SPIFFE URIs: `"header:x-forwarded-client-cert:^.*URI=spiffe://[^/]+/ns/[^/]+/sa/([^,;]+)"`
  - Custom identity header: `"header:x-client-id:^(.+)$"`
  - Traditional mTLS: `"mtls:CN=([^,]+)"`

#### Insecure Plaintext Mode (Service Mesh Authentication)
- **Command Line Flag**: `--insecure-plaintext`
- **Environment Variable**: `ARGOCD_PRINCIPAL_INSECURE_PLAINTEXT`
- **ConfigMap Entry**: `principal.tls.insecure-plaintext`
- **Description**: Disables TLS termination on the gRPC server, allowing the principal to run in plaintext mode. This flag is **required** when using `header:<header-name>:<extraction-regex>` authentication behind a service mesh.
- **Type**: Boolean
- **Default**: `false`
- **Valid Values**: `true` or `false`
- **When to Use**:
  - **REQUIRED** when using header-based authentication (`header:...`)
  - When running behind a service mesh (Istio, Linkerd, etc.) that terminates mTLS at the sidecar level
  - The service mesh must provide transport security and inject identity headers (e.g., `x-forwarded-client-cert`)
- **Security Implications**:
  - **CRITICAL**: Never enable this flag without a service mesh providing mTLS
  - When enabled, the principal accepts unencrypted gRPC connections
  - All transport security must be provided by the service mesh sidecar
  - Identity verification depends entirely on headers injected by the mesh
  - Exposing the plaintext port outside the service mesh creates a severe security vulnerability
- **Authentication Pairing**:
  - `--insecure-plaintext=true` + `--auth=header:...` → **Correct** (service mesh handles mTLS)
  - `--insecure-plaintext=false` + `--auth=mtls:...` → **Correct** (direct TLS to principal)
  - `--insecure-plaintext=true` + `--auth=mtls:...` → **Invalid** (no client certs in plaintext mode)
  - `--insecure-plaintext=false` + `--auth=header:...` → **Invalid** (headers not injected without mesh)
- **Example**: `"true"` (when using Istio with header authentication)

### Logging and Debugging

#### Log Level
- **Command Line Flag**: `--log-level`
- **Environment Variable**: `ARGOCD_PRINCIPAL_LOG_LEVEL`
- **ConfigMap Entry**: `principal.log.level`
- **Description**: The log level to use
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
- **Environment Variable**: `ARGOCD_PRINCIPAL_PPROF_PORT`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Port the pprof server will listen on (0 disables)
- **Type**: Integer
- **Default**: `0` (disabled)
- **Range**: 0, 1024-65535
- **Example**: `6060`

### Monitoring and Health

#### Metrics Port
- **Command Line Flag**: `--metrics-port`
- **Environment Variable**: `ARGOCD_PRINCIPAL_METRICS_PORT`
- **ConfigMap Entry**: `principal.metrics.port`
- **Description**: Port the metrics server will listen on
- **Type**: Integer
- **Default**: `8000`
- **Range**: 1024-65535
- **Example**: `"8000"`

#### Health Check Port
- **Command Line Flag**: `--healthz-port`
- **Environment Variable**: `ARGOCD_PRINCIPAL_HEALTH_CHECK_PORT`
- **ConfigMap Entry**: `principal.healthz.port`
- **Description**: Port the health check server will listen on
- **Type**: Integer
- **Default**: `8003`
- **Range**: 1024-65535
- **Example**: `"8003"`

### Network and Performance

#### Enable WebSocket
- **Command Line Flag**: `--enable-websocket`
- **Environment Variable**: `ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Use gRPC over WebSocket to stream events to agents
- **Type**: Boolean
- **Default**: `false`
- **Example**: `"false"`

#### Keep Alive Minimum Interval
- **Command Line Flag**: `--keepalive-min-interval`
- **Environment Variable**: `ARGOCD_PRINCIPAL_KEEP_ALIVE_MIN_INTERVAL`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Drop agent connections that send keepalive pings more often than specified interval
- **Type**: Duration
- **Default**: `0` (disabled)
- **Format**: Duration string (e.g., "30s", "5m", "1h")
- **Note**: Should be less than agent's `keep-alive-ping-interval`
- **Example**: `"30s"`

### Redis Configuration

#### Redis Server Address
- **Command Line Flag**: `--redis-server-address`
- **Environment Variable**: `ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS`
- **ConfigMap Entry**: `principal.redis.server.address`
- **Description**: Redis server hostname and port
- **Type**: String
- **Default**: `"argocd-redis:6379"`
- **Example**: `"argocd-redis:6379"`

#### Redis Compression Type
- **Command Line Flag**: `--redis-compression-type`
- **Environment Variable**: `ARGOCD_PRINCIPAL_REDIS_COMPRESSION_TYPE`
- **ConfigMap Entry**: *(Not available in ConfigMap)*
- **Description**: Compression algorithm required by Redis
- **Type**: String
- **Default**: `"gzip"`
- **Valid Values**: `"gzip"`, `"none"`
- **Example**: `"gzip"`

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
argocd-agent principal \
  --listen-host=0.0.0.0 \
  --listen-port=8443 \
  --namespace=argocd \
  --log-level=info \
  --auth=mtls:CN=([^,]+) \
  --require-client-certs=true
```

### Using Environment Variables
```bash
export ARGOCD_PRINCIPAL_LISTEN_HOST=0.0.0.0
export ARGOCD_PRINCIPAL_LISTEN_PORT=8443
export ARGOCD_PRINCIPAL_NAMESPACE=argocd
export ARGOCD_PRINCIPAL_LOG_LEVEL=info
export ARGOCD_PRINCIPAL_AUTH="mtls:CN=([^,]+)"
export ARGOCD_PRINCIPAL_TLS_CLIENT_CERT_REQUIRE=true
argocd-agent principal
```

### Using ConfigMap (Recommended)
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
data:
  principal.listen.host: "0.0.0.0"
  principal.listen.port: "8443"
  principal.log.level: "info"
  principal.namespace: "argocd"
  principal.metrics.port: "8000"
  principal.healthz.port: "8003"
  principal.tls.secret-name: "argocd-agent-principal-tls"
  principal.tls.server.root-ca-secret-name: "argocd-agent-ca"
  principal.tls.client-cert.require: "true"
  principal.tls.client-cert.match-subject: "true"
  principal.jwt.secret-name: "argocd-agent-jwt"
  principal.auth: "mtls:CN=([^,]+)"
  principal.resource-proxy.secret-name: "argocd-agent-resource-proxy-tls"
  principal.resource-proxy.ca.secret-name: "argocd-agent-ca"
  principal.namespace-create.enable: "false"
```

The ConfigMap should be mounted to the principal container and the parameters will be automatically read by the principal on startup.

### Istio Service Mesh with Header Authentication
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
data:
  principal.listen.host: "0.0.0.0"
  principal.listen.port: "8443"
  principal.log.level: "info"
  principal.namespace: "argocd"
  principal.metrics.port: "8000"
  principal.healthz.port: "8003"
  # Disable TLS - Istio sidecar handles mTLS
  principal.tls.insecure-plaintext: "true"
  # Extract agent ID from SPIFFE URI in x-forwarded-client-cert header
  # Format: spiffe://cluster.local/ns/<namespace>/sa/<service-account>
  # This extracts the service account name as the agent ID
  principal.auth: "header:x-forwarded-client-cert:^.*URI=spiffe://[^/]+/ns/[^/]+/sa/([^,;]+)"
  principal.jwt.secret-name: "argocd-agent-jwt"
  principal.resource-proxy.secret-name: "argocd-agent-resource-proxy-tls"
  principal.resource-proxy.ca.secret-name: "argocd-agent-ca"
  principal.namespace-create.enable: "false"
```

## Security Considerations

- Always use proper TLS certificates in production (avoid `insecure-*-generate` options)
- Store sensitive configuration like JWT keys and TLS certificates in Kubernetes Secrets, not ConfigMaps
- **Authentication Security**:
  - Use mutual TLS authentication (`mtls`) when agents connect directly to the principal
  - Use header-based authentication (`header`) when running behind a service mesh (Istio, Linkerd, etc.) that terminates mTLS
  - **Never** use `--insecure-plaintext` without a service mesh providing transport security and restricting network access to the principal's service (e.g. by setting `--listen-host` to `127.0.0.1` and using network policies).'
  - Ensure header extraction regex patterns properly validate agent identity
- Regularly rotate TLS certificates, JWT signing keys, and authentication credentials
- Restrict network access to the principal's metrics and health endpoints
- Carefully configure namespace creation permissions to prevent unauthorized access
- Use strong regex patterns for client certificate subject matching when enabled

## Performance Tuning

- Adjust `keepalive-min-interval` based on your network conditions and agent count
- Enable compression for high-throughput environments with `redis-compression-type: "gzip"`
- Monitor metrics endpoint to identify performance bottlenecks
- Scale Redis appropriately for your deployment size 