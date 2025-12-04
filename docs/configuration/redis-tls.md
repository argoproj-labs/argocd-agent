# Redis TLS Configuration

> **Note:** Redis TLS is **enabled by default** in Kubernetes and Helm installations. This guide explains the configuration options and how to customize the setup.

This guide explains how to configure TLS encryption for Redis connections in argocd-agent to secure sensitive data in transit.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Quick Start](#quick-start)
- [Certificate Management](#certificate-management)
- [Configuration](#configuration)
- [Kubernetes Installation](#kubernetes-installation)
- [Troubleshooting](#troubleshooting)
- [Security Best Practices](#security-best-practices)

## Overview

The argocd-agent Redis proxy handles communication between Argo CD components and Redis. Without TLS, this traffic is transmitted in plain text, potentially exposing sensitive information such as application configurations, secrets references, and cluster state.

**Redis TLS is enabled by default** in Kubernetes and Helm installations to ensure secure communication.

Redis TLS provides encryption for:
- **Principal's Redis Proxy**: Connections from Argo CD components (server, repo-server) to the principal's Redis proxy
- **Principal's Upstream Redis**: Connections from the principal's Redis proxy to its local argocd-redis instance
- **Agent's Redis**: Connections from agents to their local argocd-redis instances

## Architecture

```
Argo CD Server/Repo-Server
        |
        | (TLS encrypted)
        |
    [Principal Redis Proxy] ───────────────── Redis Proxy Server TLS
        |                                      Cert + Key required
        | (TLS encrypted)
        |
  [Principal argocd-redis] ─────────────────  Upstream Redis TLS
                                               CA cert required
        
Agent Component
        |
        | (TLS encrypted)
        |
  [Agent argocd-redis] ─────────────────────  Agent Redis TLS
                                              CA cert required
```

### TLS Configuration Points

1. **Redis Proxy Server TLS** (Principal only)
   - Secures incoming connections from Argo CD to the principal's Redis proxy
   - Requires: TLS certificate and private key
   - Configured via: `--redis-server-tls-cert`, `--redis-server-tls-key` or `--redis-server-tls-secret-name`

2. **Upstream Redis TLS** (Principal only)
   - Secures connections from the principal's Redis proxy to its local argocd-redis
   - Requires: CA certificate to validate the Redis server's certificate
   - Configured via: `--redis-upstream-ca-path` or `--redis-upstream-ca-secret-name`

3. **Agent Redis TLS** (Agent only)
   - Secures connections from agents to their local argocd-redis
   - Requires: CA certificate to validate the Redis server's certificate
   - Configured via: `--redis-tls-ca-path`

## Quick Start

### For Development/Testing

**Redis TLS is required for all E2E tests.** The `make setup-e2e` command automatically configures Redis TLS.

**Quick E2E Setup:**
```bash
make setup-e2e    # Automatically includes Redis TLS setup
make start-e2e    # Start principal and agents
make test-e2e     # Run tests
```

**Manual Testing:**
1. **Generate certificates:**
   ```bash
   ./hack/dev-env/gen-redis-tls-certs.sh
   ```

2. **Configure Redis for TLS:**
   ```bash
   ./hack/dev-env/configure-redis-tls.sh vcluster-control-plane
   ./hack/dev-env/configure-redis-tls.sh vcluster-agent-managed
   ./hack/dev-env/configure-redis-tls.sh vcluster-agent-autonomous
   ```

3. **Configure Argo CD components for Redis TLS:**
   ```bash
   ./hack/dev-env/configure-argocd-redis-tls.sh vcluster-control-plane
   ./hack/dev-env/configure-argocd-redis-tls.sh vcluster-agent-managed
   ./hack/dev-env/configure-argocd-redis-tls.sh vcluster-agent-autonomous
   ```

4. **Start principal with TLS:**
   ```bash
   ./hack/dev-env/start-principal.sh
   ```

5. **Start agents with TLS:**
   ```bash
   # In separate terminals
   ./hack/dev-env/start-agent-managed.sh
   ./hack/dev-env/start-agent-autonomous.sh
   ```

### For Production (Kubernetes)

See the [Kubernetes Installation](#kubernetes-installation) section below.

## Certificate Management

### Certificate Requirements

For production deployments, you'll need:

1. **CA Certificate** (`ca.crt`, `ca.key`)
   - Used to sign Redis server certificates
   - Distributed to principal and agents for certificate validation

2. **Redis Server Certificate** (`tls.crt`, `tls.key`)
   - For the argocd-redis Deployment in each cluster
   - Must include Subject Alternative Names (SANs) for:
     - `argocd-redis`
     - `argocd-redis.argocd.svc.cluster.local`
     - Any other DNS names or IPs used to access Redis

**Note:** The same certificate is used for both the Redis server and the principal's Redis proxy for simplicity

### Generating Certificates

#### Using OpenSSL

```bash
# Generate CA
openssl genrsa -out ca.key 4096
openssl req -new -x509 -days 3650 -key ca.key -out ca.crt \
    -subj "/CN=Redis CA"

# Generate Redis server certificate
openssl genrsa -out redis.key 4096
openssl req -new -key redis.key -out redis.csr \
    -subj "/CN=argocd-redis"

# Create SAN extension file
cat > redis.ext <<EOF
subjectAltName = @alt_names
[alt_names]
DNS.1 = argocd-redis
DNS.2 = argocd-redis.argocd.svc.cluster.local
DNS.3 = localhost
IP.1 = 127.0.0.1
EOF

# Sign the certificate
openssl x509 -req -in redis.csr -CA ca.crt -CAkey ca.key \
    -CAcreateserial -out redis.crt -days 365 -extfile redis.ext
```

## Configuration

### Principal Configuration

#### Command-line Flags

```bash
argocd-agent principal \
  --redis-tls-enabled=true \
  --redis-server-tls-cert=/app/config/redis-server-tls/tls.crt \
  --redis-server-tls-key=/app/config/redis-server-tls/tls.key \
  --redis-upstream-ca-path=/app/config/redis-upstream-tls/ca.crt
```

#### Environment Variables

```bash
export ARGOCD_PRINCIPAL_REDIS_TLS_ENABLED=true
export ARGOCD_PRINCIPAL_REDIS_SERVER_TLS_CERT_PATH=/app/config/redis-server-tls/tls.crt
export ARGOCD_PRINCIPAL_REDIS_SERVER_TLS_KEY_PATH=/app/config/redis-server-tls/tls.key
export ARGOCD_PRINCIPAL_REDIS_UPSTREAM_CA_PATH=/app/config/redis-upstream-tls/ca.crt
```

#### All Principal Redis TLS Options

| Flag | Env Var | Description | Default |
|------|---------|-------------|---------|
| `--redis-tls-enabled` | `ARGOCD_PRINCIPAL_REDIS_TLS_ENABLED` | Enable TLS for Redis connections | `true` (Kubernetes/Helm), `false` (CLI) |
| `--redis-server-tls-cert` | `ARGOCD_PRINCIPAL_REDIS_SERVER_TLS_CERT_PATH` | Path to Redis proxy server TLS certificate | `""` |
| `--redis-server-tls-key` | `ARGOCD_PRINCIPAL_REDIS_SERVER_TLS_KEY_PATH` | Path to Redis proxy server TLS private key | `""` |
| `--redis-server-tls-secret-name` | `ARGOCD_PRINCIPAL_REDIS_SERVER_TLS_SECRET_NAME` | Kubernetes secret containing Redis proxy TLS cert/key | `argocd-redis-tls` |
| `--redis-upstream-ca-path` | `ARGOCD_PRINCIPAL_REDIS_UPSTREAM_CA_PATH` | Path to CA certificate for upstream Redis TLS | `""` |
| `--redis-upstream-ca-secret-name` | `ARGOCD_PRINCIPAL_REDIS_UPSTREAM_CA_SECRET_NAME` | Kubernetes secret containing CA certificate | `argocd-redis-tls` |
| `--redis-upstream-tls-insecure` | `ARGOCD_PRINCIPAL_REDIS_UPSTREAM_TLS_INSECURE` | INSECURE: Skip upstream Redis TLS verification | `false` |

### Agent Configuration

#### Command-line Flags

```bash
argocd-agent agent \
  --redis-tls-enabled=true \
  --redis-tls-ca-path=/app/config/redis-tls/ca.crt
```

#### Environment Variables

```bash
export ARGOCD_AGENT_REDIS_TLS_ENABLED=true
export ARGOCD_AGENT_REDIS_TLS_CA_PATH=/app/config/redis-tls/ca.crt
```

#### All Agent Redis TLS Options

| Flag | Env Var | Description | Default |
|------|---------|-------------|---------|
| `--redis-tls-enabled` | `ARGOCD_AGENT_REDIS_TLS_ENABLED` | Enable TLS for Redis connections | `true` (Kubernetes/Helm), `false` (CLI) |
| `--redis-tls-ca-path` | `ARGOCD_AGENT_REDIS_TLS_CA_PATH` | Path to CA certificate for Redis TLS | `""` |
| `--redis-tls-insecure` | `ARGOCD_AGENT_REDIS_TLS_INSECURE` | INSECURE: Skip Redis TLS verification | `false` |

## Kubernetes Installation

> **Important:** The default Kubernetes and Helm installations have Redis TLS **pre-configured** with `principal.redis.tls.enabled: "true"` and `agent.redis.tls.enabled: "true"`. You only need to create the TLS secrets with your certificates.

### 1. Create TLS Secret

First, generate certificates (see [Certificate Management](#certificate-management)), then create a Kubernetes secret containing all TLS materials:

```bash
# Create a single secret with server cert, key, and CA cert
# This secret is used by both the principal and agents
kubectl create secret generic argocd-redis-tls \
  --from-file=tls.crt=redis.crt \
  --from-file=tls.key=redis.key \
  --from-file=ca.crt=ca.crt \
  -n argocd
```

**Note:** The same secret is used for:
- Redis server TLS (tls.crt, tls.key) - for Redis Deployment
- Redis proxy server TLS (tls.crt, tls.key) - for principal's Redis proxy
- Client validation (ca.crt) - for principal and agents to validate Redis

### 2. Configure Redis to Use TLS

Configure the `argocd-redis` Deployment to enable TLS:

```bash
# Add TLS volume (using "-" to append to array, works even if array exists)
kubectl patch deployment argocd-redis -n argocd --type='json' -p='[
  {
    "op": "add",
    "path": "/spec/template/spec/volumes/-",
    "value": {"name": "redis-tls", "secret": {"secretName": "argocd-redis-tls"}}
  }
]'

# Add volume mount (using "-" to append to array, works even if array exists)
kubectl patch deployment argocd-redis -n argocd --type='json' -p='[
  {
    "op": "add",
    "path": "/spec/template/spec/containers/0/volumeMounts/-",
    "value": {"name": "redis-tls", "mountPath": "/app/tls"}
  }
]'

# Update Redis args for TLS
kubectl patch deployment argocd-redis -n argocd --type='json' -p='[
  {
    "op": "replace",
    "path": "/spec/template/spec/containers/0/args",
    "value": [
      "--save", "",
      "--appendonly", "no",
      "--requirepass", "$(REDIS_PASSWORD)",
      "--tls-port", "6379",
      "--port", "0",
      "--tls-cert-file", "/app/tls/tls.crt",
      "--tls-key-file", "/app/tls/tls.key",
      "--tls-ca-cert-file", "/app/tls/ca.crt",
      "--tls-auth-clients", "no"
    ]
  }
]'

# Wait for rollout
kubectl rollout status deployment/argocd-redis -n argocd --timeout=120s
```

**Note:** These commands configure Redis to:
- Listen on TLS port 6379 (`--tls-port 6379`)
- Disable plain TCP (`--port 0`)
- Use the mounted TLS certificates
- Not require client certificates (`--tls-auth-clients no`)

### 3. Verify the Installation

The default Kubernetes installation already has Redis TLS **fully configured**:

**Principal ConfigMap** (`install/kubernetes/principal/principal-params-cm.yaml`):
```yaml
data:
  principal.redis.tls.enabled: "true"
  principal.redis.server.tls.cert-path: "/app/config/redis-server-tls/tls.crt"
  principal.redis.server.tls.key-path: "/app/config/redis-server-tls/tls.key"
  principal.redis.server.tls.secret-name: "argocd-redis-tls"
  principal.redis.upstream.ca-path: "/app/config/redis-upstream-tls/ca.crt"
  principal.redis.upstream.ca-secret-name: "argocd-redis-tls"
```

**Agent ConfigMap** (`install/kubernetes/agent/agent-params-cm.yaml`):
```yaml
data:
  agent.redis.tls.enabled: "true"
  agent.redis.tls.ca-path: "/app/config/redis-tls/ca.crt"
```

**Deployments already have volume mounts configured** - no manual changes needed.

## Troubleshooting

### Connection Refused

**Problem:** Redis connections fail with "connection refused"

**Solution:**
- Verify Redis is listening on the TLS port:
  ```bash
  # Get the Redis pod name
  REDIS_POD=$(kubectl get pod -n argocd -l app.kubernetes.io/name=argocd-redis -o jsonpath='{.items[0].metadata.name}')
  
  # Test TLS connection
  kubectl exec -it $REDIS_POD -n argocd -- redis-cli --tls --cert /app/tls/tls.crt --key /app/tls/tls.key --cacert /app/tls/ca.crt ping
  ```
- Check Redis configuration in Deployment includes `--tls-port 6379` and `--port 0`

### Certificate Verification Failed

**Problem:** TLS handshake fails with "certificate verify failed"

**Solution:**
- Ensure CA certificate matches the one used to sign server certificates
- Verify certificate SANs include all necessary DNS names and IPs
- Check certificate validity: `openssl x509 -in cert.crt -text -noout`

### Principal Can't Connect to Upstream Redis

**Problem:** Principal logs show "unable to connect to principal redis"

**Solution:**
- Verify `--redis-upstream-ca-path` points to the correct CA certificate
- Check Redis server certificate was signed by the same CA
- Ensure Redis Deployment has TLS properly configured

### Agent Can't Connect to Local Redis

**Problem:** Agent logs show "failed to connect to redis"

**Solution:**
- Verify `--redis-tls-ca-path` is set correctly on the agent
- Ensure the agent's Redis instance has TLS enabled
- Check agent namespace has the `argocd-redis-tls` secret

### Debugging TLS Handshake

Enable trace-level logging to see detailed TLS handshake information:

```bash
--log-level=trace
```

Test Redis TLS connection manually from within the Redis pod:
```bash
# Get the Redis pod name
REDIS_POD=$(kubectl get pod -n argocd -l app.kubernetes.io/name=argocd-redis -o jsonpath='{.items[0].metadata.name}')

# Test connection using openssl
kubectl exec -it $REDIS_POD -n argocd -- sh -c \
  "openssl s_client -connect localhost:6379 -CAfile /app/tls/ca.crt"
```

## Security Best Practices

1. **Use Strong Certificates**
   - Use 4096-bit RSA keys or equivalent EC keys
   - Set appropriate certificate validity periods (1 year recommended)
   - Ensure SANs include all necessary DNS names and IPs

2. **Protect Private Keys**
   - Store private keys in Kubernetes secrets with restricted RBAC
   - Use `readOnly: true` for volume mounts containing keys
   - Never commit private keys to version control

3. **Certificate Rotation**
   - Implement a certificate rotation strategy
   - Monitor certificate expiration dates
   - Test rotation procedures in non-production environments

4. **Disable Insecure Options**
   - Never use `--redis-upstream-tls-insecure` or `--redis-tls-insecure` in production
   - These options disable certificate verification and are insecure

## Additional Resources

- [Redis TLS Support](https://redis.io/docs/latest/operate/oss_and_stack/management/security/encryption/)
- [OpenSSL Certificate Management](https://www.openssl.org/docs/man1.1.1/man1/openssl.html)
