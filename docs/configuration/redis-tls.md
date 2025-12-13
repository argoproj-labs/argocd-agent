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

```text
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

**Redis TLS is REQUIRED for all E2E tests** 
Tests will fail if Redis TLS is not properly configured. The `make setup-e2e` command automatically configures Redis TLS.

**Quick E2E Setup:**
```bash
make setup-e2e    # Automatically includes Redis TLS setup
make start-e2e    # Start principal and agents
make test-e2e     # Run tests (ONLY with Redis TLS enabled)
```

**E2E Test Environment Variables:**
```bash
# Automatically set by hack/dev-env/start-e2e:
export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS="localhost:6380"
export MANAGED_AGENT_REDIS_ADDR="localhost:6381"
export AUTONOMOUS_AGENT_REDIS_ADDR="localhost:6382"
```

#### What `make setup-e2e` Does

The `make setup-e2e` command performs a comprehensive setup, including all Redis TLS configuration:

1. **Creates vclusters** (control-plane, agent-managed, agent-autonomous)
2. **Generates mTLS certificates** for agent authentication
3. **Creates agent configuration** (cluster secrets, RBAC)
4. **Generates Redis TLS certificates** (calls `gen-redis-tls-certs.sh`)
   - ✅ Includes your local IP in SANs
   - ✅ Includes `rathole-container-internal` for reverse tunnel
5. **Configures Redis for TLS** (calls `configure-redis-tls.sh` for each vcluster)
   - Creates `argocd-redis-tls` secrets
   - Patches Redis Deployments for TLS
6. **Configures Argo CD components** (calls `configure-argocd-redis-tls.sh` for each vcluster)
   - Sets `redis.server` to DNS names (not ClusterIPs)
   - Adds TLS configuration to server, repo-server, application-controller
   - Restarts all components

After `make setup-e2e` completes, your environment is **fully configured** for Redis TLS and ready to run tests.

### Local Development Environments

Your local development setup determines whether additional configuration is needed:

#### Setup 1: Local vclusters (Recommended)
- **Description:** vclusters run on local microk8s/k3d/kind on your workstation
- **Connectivity:** Direct via LoadBalancer or port-forwards
- **Requirements:** 
  - ✅ Port-forwards (`localhost:6380/6381/6382`)
  - ❌ No reverse tunnel needed
- **Example:** CI environment, local k3d setup

#### Setup 2: Remote vclusters + Local Mac
- **Description:** vclusters run on remote cluster (e.g., OpenShift on remote machine), but you run principal/agents locally on your Mac
- **Connectivity:** Remote Argo CD components need to reach your local Redis Proxy
- **Requirements:**
  - ✅ Port-forwards for local access
  - ✅ **Reverse tunnel (rathole) REQUIRED** for remote → local connectivity
  - ✅ Certificate SANs must include tunnel hostname (already configured in `gen-redis-tls-certs.sh`)
  - ✅ **LoadBalancer support** on remote K8s cluster (e.g., MetalLB, Red Hat clusterbot)
- **Setup:**
  ```bash
  # 1. Run standard E2E setup first
  make setup-e2e
  
  # 2. Set up reverse tunnel using the provided script
  # This script:
  #   - Deploys rathole server to vcluster-control-plane
  #   - Patches cluster secrets to route through the tunnel
  #   - Starts a local rathole client container (runs in foreground)
  ./hack/dev-env/reverse-tunnel/setup.sh
  
  # 3. In a NEW terminal, start the principal and agents
  make start-e2e
  
  # 4. In another terminal, run tests
  make test-e2e
  ```

- **How the tunnel works:**
  ```
  Argo CD Server (remote vcluster) 
      → rathole Deployment (remote) 
      → rathole Container (local Mac) 
      → Principal process (local Mac)
  ```

- **Note:** The rathole client runs in foreground and may show initial connection errors while waiting for LoadBalancer. These messages are safe to ignore:
  - `"Failed to run the control channel: Name or service not known"` - Waiting for LoadBalancer
  - `"Failed to connect to 127.0.0.1:6379: Connection refused"` - Need to run `make start-e2e`

- **See also:** `hack/dev-env/reverse-tunnel/README.md` for detailed configuration

**Manual Testing:**

#### Step 1: Generate Certificates

```bash
./hack/dev-env/gen-redis-tls-certs.sh
```

This script generates:
- **CA certificate** (`ca.crt`, `ca.key`) - Used to sign all Redis certificates
- **Redis server certificates** for each vcluster:
  - `redis-control-plane.{crt,key}` - For principal's Redis
  - `redis-managed.{crt,key}` - For managed agent's Redis
  - `redis-autonomous.{crt,key}` - For autonomous agent's Redis
- **Redis proxy certificate** (`redis-proxy.{crt,key}`) - For principal's Redis proxy
  - **Automatically includes** your Mac's local IP in SANs (detected via `ipconfig` or `ip`)
  - **Already includes** `rathole-container-internal` in SANs for reverse tunnel support

**Certificate SANs include:**
- `argocd-redis`, `argocd-redis.argocd.svc.cluster.local` (Kubernetes DNS)
- `localhost`, `127.0.0.1` (port-forward support)
- Your Mac's IP address (direct access)
- `rathole-container-internal` (reverse tunnel support)

#### Step 2: Configure Redis for TLS

```bash
./hack/dev-env/configure-redis-tls.sh vcluster-control-plane
./hack/dev-env/configure-redis-tls.sh vcluster-agent-managed
./hack/dev-env/configure-redis-tls.sh vcluster-agent-autonomous
```

This script **for each cluster**:
1. Creates `argocd-redis-tls` secret with TLS certificate, key, and CA
2. Patches `argocd-redis` Deployment:
   - Adds `redis-tls` volume (from secret)
   - Adds volume mount to `/app/tls`
   - Updates Redis arguments:
     - `--tls-port 6379` (enable TLS on port 6379)
     - `--port 0` (disable plain TCP)
     - `--tls-cert-file`, `--tls-key-file`, `--tls-ca-cert-file` (certificate paths)
     - `--tls-auth-clients no` (client certs not required)
3. Waits for deployment rollout to complete

#### Step 3: Configure Argo CD Components for Redis TLS

```bash
./hack/dev-env/configure-argocd-redis-tls.sh vcluster-control-plane
./hack/dev-env/configure-argocd-redis-tls.sh vcluster-agent-managed
./hack/dev-env/configure-argocd-redis-tls.sh vcluster-agent-autonomous
```

This script **for each cluster**:
1. **For agent clusters only:** Updates `argocd-cmd-params-cm` ConfigMap:
   - Sets `redis.server: argocd-redis:6379` (uses DNS name, not ClusterIP!)
2. Patches Argo CD components (`argocd-server`, `argocd-repo-server`, `argocd-application-controller`):
   - Adds `redis-tls-ca` volume (CA certificate from `argocd-redis-tls` secret)
   - Adds volume mount to `/app/config/redis/tls`
   - Adds args: `--redis-use-tls`, `--redis-ca-certificate=/app/config/redis/tls/ca.crt`
3. Restarts all Argo CD components to apply changes

#### Step 4: Start Principal with TLS

```bash
./hack/dev-env/start-principal.sh
```

The script automatically:
- Detects Redis TLS certificates in `hack/dev-env/creds/redis-tls/`
- Enables Redis TLS with `--redis-tls-enabled=true`
- Configures Redis proxy server TLS (for incoming connections from Argo CD)
- Configures upstream Redis TLS (for connections to principal's Redis)

#### Step 5: Start Agents with TLS

```bash
# In separate terminals
./hack/dev-env/start-agent-managed.sh
./hack/dev-env/start-agent-autonomous.sh
```

The scripts automatically:
- Detect Redis TLS certificates
- Enable Redis TLS with `--redis-tls-enabled=true`
- Configure Redis CA certificate for TLS validation

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
   - Must include Subject Alternative Names (SANs) for all connection methods:
     - `argocd-redis`, `argocd-redis.argocd.svc.cluster.local` (Kubernetes DNS)
     - `localhost`, `127.0.0.1` (for port-forward connections)

**Note:** The same certificate is used for both the Redis server and the principal's Redis proxy for simplicity

### Generating Certificates

#### Using OpenSSL (Manual)

> **Note:** For E2E tests and local development, use `./hack/dev-env/gen-redis-tls-certs.sh` instead, which automates all of this and includes appropriate SANs automatically.

For production or custom setups, you can generate certificates manually:

```bash
# Generate CA
openssl genrsa -out ca.key 4096
openssl req -new -x509 -days 3650 -key ca.key -out ca.crt \
    -subj "/CN=Redis CA"

# Generate Redis server certificate
openssl genrsa -out redis.key 4096
openssl req -new -key redis.key -out redis.csr \
    -subj "/CN=argocd-redis"

# Create SAN extension file (customize for your environment)
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
export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS=localhost:6380  # Redis address (with port-forward)
export ARGOCD_PRINCIPAL_REDIS_TLS_ENABLED=true
export ARGOCD_PRINCIPAL_REDIS_SERVER_TLS_CERT_PATH=/app/config/redis-server-tls/tls.crt
export ARGOCD_PRINCIPAL_REDIS_SERVER_TLS_KEY_PATH=/app/config/redis-server-tls/tls.key
export ARGOCD_PRINCIPAL_REDIS_UPSTREAM_CA_PATH=/app/config/redis-upstream-tls/ca.crt
```

#### All Principal Redis TLS Options

| Flag | Env Var | Description | Default |
|------|---------|-------------|---------|
| `--redis-server-address` | `ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS` | Redis server address (host:port) | `argocd-redis:6379` |
| `--redis-tls-enabled` | `ARGOCD_PRINCIPAL_REDIS_TLS_ENABLED` | Enable TLS for Redis connections | `true` |
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
export ARGOCD_AGENT_REDIS_ADDRESS=localhost:6381  # Redis address (with port-forward)
export ARGOCD_AGENT_REDIS_TLS_ENABLED=true
export ARGOCD_AGENT_REDIS_TLS_CA_PATH=/app/config/redis-tls/ca.crt
```

#### All Agent Redis TLS Options

| Flag | Env Var | Description | Default |
|------|---------|-------------|---------|
| `--redis-addr` | `ARGOCD_AGENT_REDIS_ADDRESS` | Redis server address (host:port) | Service discovery |
| `--redis-tls-enabled` | `ARGOCD_AGENT_REDIS_TLS_ENABLED` | Enable TLS for Redis connections | `true` |
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

### Understanding Script Output

The E2E setup scripts provide detailed output. Here's what to expect:

**gen-redis-tls-certs.sh:**
```
Generating Redis TLS certificates in hack/dev-env/creds/redis-tls...
Generating CA key and certificate...
Generating redis-control-plane certificate...
Generating redis-proxy certificate...
Generating redis-autonomous certificate...
Generating redis-managed certificate...
Redis TLS certificates generated successfully!
```

**configure-redis-tls.sh:**
```
╔══════════════════════════════════════════════════════════╗
║  Configure Redis Deployment for TLS                     ║
╚══════════════════════════════════════════════════════════╝

Using certificates: redis-control-plane.{crt,key}
Creating TLS secret...
Secret created
Patching Redis deployment for TLS...
Adding redis-tls volume...
Adding redis-tls volumeMount...
Deployment patched
Waiting for deployment rollout...
deployment "argocd-redis" successfully rolled out
✅ Redis pod argocd-redis-xxx is running with TLS
```

**configure-argocd-redis-tls.sh:**
```
╔══════════════════════════════════════════════════════════╗
║  Configure Argo CD Components for Redis TLS             ║
╚══════════════════════════════════════════════════════════╝

Note: This is for E2E tests and local development only
TLS encryption enabled with CA certificate validation

Configuring argocd-server for Redis TLS...
  argocd-server configured
Configuring argocd-repo-server for Redis TLS...
  argocd-repo-server configured
Configuring argocd-application-controller for Redis TLS...
  argocd-application-controller configured

✅ Restarting Argo CD components to apply Redis TLS configuration...
```

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

### Port-Forward Instability During Long Test Runs

**Problem:** `kubectl port-forward` dies during long-running E2E tests, causing "connection reset by peer" or "EOF" errors

**Symptoms:**
- Tests pass initially but fail after several minutes
- Error messages: `dial tcp: connection reset by peer`, `EOF`, `context deadline exceeded`
- Port-forward process exits unexpectedly

**Solutions:**

1. **Resilient test cleanup** (already implemented in `test/e2e/fixture/fixture.go`):
   - Tests log warnings instead of failing when cleanup encounters Redis connectivity issues
   - Prevents cascading failures when port-forwards die

2. **For long test sessions, use in-cluster testing:**
   ```bash
   # Deploy components in-cluster instead of running locally
   ARGOCD_AGENT_IN_CLUSTER=true make setup-e2e
   ```

3. **Monitor port-forwards:**
   ```bash
   # Check if port-forwards are still running
   ps aux | grep "kubectl port-forward"
   
   # Restart if needed (using goreman)
   make start-e2e
   ```

**Note:** CI environments don't use port-forwards (direct LoadBalancer connectivity), so this is a local development issue only.

### Script Failures or Need to Reconfigure

**Problem:** A setup script failed partway through, or you need to update certificate SANs

**Solution:**

1. **Scripts are idempotent** - You can safely re-run any script:
   ```bash
   # Re-run certificate generation (won't overwrite existing certs)
   ./hack/dev-env/gen-redis-tls-certs.sh
   
   # Re-run Redis TLS configuration
   ./hack/dev-env/configure-redis-tls.sh vcluster-control-plane
   
   # Re-run Argo CD component configuration
   ./hack/dev-env/configure-argocd-redis-tls.sh vcluster-control-plane
   ```

2. **Force regenerate certificates:**
   ```bash
   # Delete existing certificates
   rm -rf hack/dev-env/creds/redis-tls/*
   
   # Regenerate with your changes
   ./hack/dev-env/gen-redis-tls-certs.sh
   
   # Reconfigure all clusters
   for ctx in vcluster-control-plane vcluster-agent-managed vcluster-agent-autonomous; do
     ./hack/dev-env/configure-redis-tls.sh $ctx
     ./hack/dev-env/configure-argocd-redis-tls.sh $ctx
   done
   ```

3. **Clean slate - recreate vclusters:**
   ```bash
   make teardown-e2e
   make setup-e2e
   ```

### Certificate Verification Failed

**Problem:** TLS handshake fails with "certificate verify failed" or "certificate is valid for X, not Y"

**Common Causes:**
1. **Using ClusterIP instead of DNS name**
   - ❌ Bad: `redis.server: 172.30.181.175:6379` (ClusterIP)
   - ✅ Good: `redis.server: argocd-redis:6379` (DNS name)
   
2. **Certificate SANs don't match connection hostname**

**Solution:**
- Ensure CA certificate matches the one used to sign server certificates
- Verify certificate SANs include all connection methods:
  ```bash
  openssl x509 -in redis.crt -text -noout | grep -A1 "Subject Alternative Name"
  ```
- **Always use DNS service names in Argo CD configuration:**
  ```bash
  kubectl patch configmap argocd-cmd-params-cm -n argocd \
    --patch '{"data":{"redis.server":"argocd-redis:6379"}}'
  ```
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

### Remote vcluster Connectivity Issues

**Problem:** Local Mac + remote vclusters - Argo CD components can't reach local Redis Proxy

**Solution:** See [Setup 2: Remote vclusters + Local Mac](#setup-2-remote-vclusters--local-mac) for the reverse tunnel setup using the provided script:
```bash
./hack/dev-env/reverse-tunnel/setup.sh
```

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
