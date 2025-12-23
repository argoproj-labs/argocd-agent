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
- [Testing Guide](#testing-guide)
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
   - Configured via: `--redis-proxy-server-tls-cert`, `--redis-proxy-server-tls-key` or `--redis-proxy-server-tls-secret-name`

2. **Upstream Redis TLS** (Principal only)
   - Secures connections from the principal's Redis proxy to its local argocd-redis
   - Requires: CA certificate to validate the Redis server's certificate
   - Configured via: `--redis-ca-path` or `--redis-ca-secret-name`

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
make test-e2e     # Run tests
```

**For CI:**
```bash
make setup-e2e
make start-e2e &
sleep 10
make test-e2e
```

> **Note:** No manual environment variables needed! The scripts automatically detect your environment (macOS vs Linux) and configure appropriately.

**What Happens Automatically:**
- **Local macOS**: Port-forwards to `localhost:6380/6381/6382` are used
- **CI/Linux**: Direct LoadBalancer IPs are detected and used
- **With reverse tunnel**: Traffic routes through the tunnel to your local principal

#### What `make setup-e2e` Does

The `make setup-e2e` command performs a comprehensive setup, including all Redis TLS configuration:

1. **Creates vclusters** (control-plane, agent-managed, agent-autonomous)
2. **Generates mTLS certificates** for agent authentication
3. **Creates agent configuration** (cluster secrets, RBAC)
4. **Waits for LoadBalancer IPs** - Ensures Redis services have external IPs assigned (critical for CI)
5. **Generates Redis TLS certificates** (calls `gen-redis-tls-certs.sh`)
   - ✅ Includes your local IP in SANs
   - ✅ Includes LoadBalancer IPs/hostnames in SANs (fixes TLS handshake failures in CI)
   - ✅ Includes `rathole-container-internal` for reverse tunnel
6. **Configures Redis for TLS** (calls `configure-redis-tls.sh` for each vcluster)
   - Creates `argocd-redis-tls` secrets
   - Patches Redis Deployments for TLS
7. **Configures Argo CD components** (calls `configure-argocd-redis-for-tls.sh` for each vcluster)
   - Sets `redis.server` to DNS names (not ClusterIPs)
   - Adds TLS configuration to server, repo-server, application-controller
   - Restarts all components

After `make setup-e2e` completes, your environment is **fully configured** for Redis TLS and ready to run tests. **All E2E tests pass successfully** ✅

### Local Development Environments

Your local development setup determines whether additional configuration is needed:

#### Setup 1: Local vclusters (Recommended)
- **Description:** vclusters run on local microk8s/k3d/kind on your workstation
- **Connectivity:** Direct via LoadBalancer or port-forwards
- **Requirements:** 
  - ✅ Port-forwards (`localhost:6380/6381/6382`) on macOS
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

> **For detailed manual testing steps**, see the comprehensive [Testing Guide](#testing-guide) section below.

## Certificate Management

### Certificate Requirements

For production deployments, you'll need:

1. **CA Certificate** (`ca.crt`, `ca.key`)
   - **Redis TLS uses the same agent CA** used for agent/principal mTLS authentication
   - This simplifies certificate management by consolidating to a single CA
   - The CA signs both agent client certificates and Redis server certificates

2. **Redis Server Certificate** (`tls.crt`, `tls.key`)
   - For the argocd-redis Deployment in each cluster
   - Signed by the agent CA
   - Must include Subject Alternative Names (SANs) for all connection methods:
     - `argocd-redis`, `argocd-redis.argocd.svc.cluster.local` (Kubernetes DNS)
     - `localhost`, `127.0.0.1` (for port-forward connections)

**Note:** 
- The same certificate is used for both the Redis server and the principal's Redis proxy for simplicity
- For dev/E2E environments, `gen-redis-tls-certs.sh` automatically uses the existing agent CA

### Generating Certificates

#### Using OpenSSL (Manual)

> **Note:** For E2E tests and local development, use `./hack/dev-env/gen-redis-tls-certs.sh` instead, which automatically uses the existing agent CA and includes appropriate SANs.

For production or custom setups, you can generate certificates manually.

**Important:** Redis TLS uses the same CA as agent/principal mTLS. Use your existing agent CA instead of generating a new one:

```bash
# Use your existing agent CA (e.g., from argocd-agent-ca secret)
# If you don't have one yet, generate it:
# openssl genrsa -out ca.key 4096
# openssl req -new -x509 -days 3650 -key ca.key -out ca.crt \
#     -subj "/CN=Agent CA"

# Generate Redis server certificate (signed by agent CA)
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
  --redis-proxy-server-tls-cert=/app/config/redis-proxy-server-tls/tls.crt \
  --redis-proxy-server-tls-key=/app/config/redis-proxy-server-tls/tls.key \
  --redis-ca-path=/app/config/redis-tls/ca.crt
```

#### Environment Variables

```bash
export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS=localhost:6380  # Redis address (with port-forward)
export ARGOCD_PRINCIPAL_REDIS_TLS_ENABLED=true
export ARGOCD_PRINCIPAL_REDIS_PROXY_SERVER_TLS_CERT_PATH=/app/config/redis-proxy-server-tls/tls.crt
export ARGOCD_PRINCIPAL_REDIS_PROXY_SERVER_TLS_KEY_PATH=/app/config/redis-proxy-server-tls/tls.key
export ARGOCD_PRINCIPAL_REDIS_CA_PATH=/app/config/redis-tls/ca.crt
```

#### All Principal Redis TLS Options

| Flag | Env Var | Description | Default |
|------|---------|-------------|---------|
| `--redis-server-address` | `ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS` | Redis server address (host:port) | `argocd-redis:6379` |
| `--redis-tls-enabled` | `ARGOCD_PRINCIPAL_REDIS_TLS_ENABLED` | Enable TLS for Redis connections | `true` |
| `--redis-proxy-server-tls-cert` | `ARGOCD_PRINCIPAL_REDIS_PROXY_SERVER_TLS_CERT_PATH` | Path to Redis proxy server TLS certificate | `""` |
| `--redis-proxy-server-tls-key` | `ARGOCD_PRINCIPAL_REDIS_PROXY_SERVER_TLS_KEY_PATH` | Path to Redis proxy server TLS private key | `""` |
| `--redis-proxy-server-tls-secret-name` | `ARGOCD_PRINCIPAL_REDIS_PROXY_SERVER_TLS_SECRET_NAME` | Kubernetes secret containing Redis proxy TLS cert/key | `argocd-redis-tls` |
| `--redis-ca-path` | `ARGOCD_PRINCIPAL_REDIS_CA_PATH` | Path to CA certificate for upstream Redis TLS | `""` |
| `--redis-ca-secret-name` | `ARGOCD_PRINCIPAL_REDIS_CA_SECRET_NAME` | Kubernetes secret containing CA certificate | `argocd-redis-tls` |
| `--redis-tls-insecure` | `ARGOCD_PRINCIPAL_REDIS_TLS_INSECURE` | INSECURE: Skip upstream Redis TLS verification | `false` |

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
  principal.redis.proxy.server.tls.cert-path: "/app/config/redis-proxy-server-tls/tls.crt"
  principal.redis.proxy.server.tls.key-path: "/app/config/redis-proxy-server-tls/tls.key"
  principal.redis.proxy.server.tls.secret-name: "argocd-redis-tls"
  principal.redis.ca-path: "/app/config/redis-tls/ca.crt"
  principal.redis.ca-secret-name: "argocd-redis-tls"
```

**Agent ConfigMap** (`install/kubernetes/agent/agent-params-cm.yaml`):
```yaml
data:
  agent.redis.tls.enabled: "true"
  agent.redis.tls.ca-path: "/app/config/redis-tls/ca.crt"
```

**Deployments already have volume mounts configured** - no manual changes needed.

## Testing Guide

This guide walks through testing Redis TLS in a local development environment using the E2E setup scripts.

### Prerequisites

- ✅ Run `make setup-e2e` first (sets up vclusters with Argo CD and LoadBalancer IPs)
- ✅ Code changes compiled
- ✅ Three terminal windows available

### Terminal Layout

| Terminal | Purpose |
|----------|---------|
| Terminal 1 | Principal process (auto-starts control plane Redis port-forward on 6380) |
| Terminal 2 | Agent process (requires port-forward on 6381 for agent Redis) |
| Terminal 3 | Port-forwards (agent Redis 6381, UI 8080) & test commands |

---

### Step 1: Generate Certificates

```bash
cd hack/dev-env
./gen-redis-tls-certs.sh
```

**Expected:** Certificate files created in `creds/redis-tls/`:
- `ca.crt`, `ca.key` (CA certificate authority)
- `redis-control-plane.{crt,key}` (for control plane Redis)
- `redis-proxy.{crt,key}` (for principal's Redis proxy)
- `redis-managed.{crt,key}` (for managed agent Redis)
- `redis-autonomous.{crt,key}` (for autonomous agent Redis)

---

### Step 2: Configure Argo CD Components for Redis TLS

**Important:** Configure Argo CD components BEFORE enabling Redis TLS to prevent connection errors.

```bash
# Control plane
./configure-argocd-redis-for-tls.sh vcluster-control-plane

# Managed agent
./configure-argocd-redis-for-tls.sh vcluster-agent-managed

# Autonomous agent (optional, if testing autonomous mode)
./configure-argocd-redis-for-tls.sh vcluster-agent-autonomous
```

**What this does:**
- ✅ Creates `argocd-redis-tls` Kubernetes secret with certificates
- ✅ Mounts CA certificate to Argo CD components
- ✅ Configures `--redis-use-tls` with `--redis-ca-certificate` flag
- ✅ Waits for pods to restart with new configuration
- ✅ **Enables proper certificate validation** (no insecure skip!)

**Expected output:**
```
✅ Argo CD Redis TLS Configuration Complete!
Argo CD components will now connect to Redis using TLS with proper certificate validation.
```

---

### Step 3: Enable Redis TLS

Now that Argo CD components are ready, enable TLS on Redis:

```bash
# Control plane
./configure-redis-tls.sh vcluster-control-plane

# Managed agent
./configure-redis-tls.sh vcluster-agent-managed

# Autonomous agent (optional)
./configure-redis-tls.sh vcluster-agent-autonomous
```

**What this does:**
- ✅ Uses existing `argocd-redis-tls` secret (created in Step 2)
- ✅ Patches Redis deployment for TLS-only mode
- ✅ Waits for rollout to complete

**Expected output:**
```
✅ Redis TLS Configuration Complete!
Redis is now running in TLS-only mode.
```

---

### Step 4: Verify No Errors

Wait 30 seconds for everything to stabilize, then check for errors:

```bash
sleep 30

# Control plane - should be EMPTY (no errors)
kubectl logs -n argocd -l app.kubernetes.io/name=argocd-redis --context vcluster-control-plane --since=1m | grep -i "error\|wrong version\|bad certificate"

# Managed agent - should be EMPTY (no errors)
kubectl logs -n argocd -l app.kubernetes.io/name=argocd-redis --context vcluster-agent-managed --since=1m | grep -i "error\|wrong version\|bad certificate"
```

**Expected:** No output (indicates no TLS errors)

---

### Step 5: Test Redis TLS Connections

Test direct Redis connections to verify TLS is working:

**Control Plane:**
```bash
REDIS_PASSWORD=$(kubectl get secret argocd-redis -n argocd --context vcluster-control-plane -o jsonpath='{.data.auth}' | base64 -d)
kubectl exec -n argocd --context vcluster-control-plane -it deployment/argocd-redis -- redis-cli -h localhost -p 6379 --tls --insecure -a "$REDIS_PASSWORD" PING
```

**Expected:** `PONG`

**Managed Agent:**
```bash
REDIS_PASSWORD=$(kubectl get secret argocd-redis -n argocd --context vcluster-agent-managed -o jsonpath='{.data.auth}' | base64 -d)
kubectl exec -n argocd --context vcluster-agent-managed -it deployment/argocd-redis -- redis-cli -h localhost -p 6379 --tls --insecure -a "$REDIS_PASSWORD" PING
```

**Expected:** `PONG`

---

### Step 6: Start Port-Forward for Agent Redis (Terminal 3)

The agent needs local access to its Redis. Start the port-forward:

```bash
kubectl port-forward svc/argocd-redis -n argocd 6381:6379 --context vcluster-agent-managed
```

**Keep this running!**

**Note:** The control plane Redis port-forward (6380) is automatically started by `start-principal.sh` in Step 7.

**Test the port-forward (in another terminal):**
```bash
REDIS_PASSWORD=$(kubectl get secret argocd-redis -n argocd --context vcluster-agent-managed -o jsonpath='{.data.auth}' | base64 -d)
redis-cli -h localhost -p 6381 --tls --insecure -a "$REDIS_PASSWORD" PING
```

**Expected:** `PONG` ✅

---

### Step 7: Start Principal (Terminal 1)

```bash
cd hack/dev-env
./start-principal.sh
```

**The script automatically:**
1. Sets up port-forward to control plane Redis on `localhost:6380`
2. Loads Redis TLS certificates
3. Connects with **proper TLS certificate validation** using the CA certificate
4. Starts Redis proxy with TLS enabled on port 6379

**Wait for these success messages:**
```
✅ Starting port-forward to Redis on localhost:6380...
✅ Connected to Redis via port-forward at localhost:6380
✅ Redis TLS certificates found, enabling TLS for Redis connections
✅ Loading Redis upstream CA certificate from file
✅ level=info msg="Starting argocd-agent (server) v99.9.9-unreleased"
✅ level=info msg="Now listening on [::]:8443"
✅ level=info msg="Redis proxy started on 0.0.0.0:6379 with TLS"
✅ level=info msg="Application informer synced and ready"
```

**✅ Full TLS with Certificate Validation:** The port-forward allows connection via `localhost`, which is in the certificate's SANs, enabling proper hostname verification!

**Keep this terminal running!**

---

### Step 8: Start Agent (Terminal 2)

```bash
cd hack/dev-env
./start-agent-managed.sh
```

**The script automatically:**
1. Uses `localhost:6381` for Redis (requires the port-forward from Step 6)
2. Loads Redis TLS certificates
3. Enables TLS with **proper certificate validation** using the CA certificate
4. Connects to the principal with mTLS authentication

**Wait for these success messages:**
```
✅ Using default Redis address for local development: localhost:6381
✅ Redis TLS certificates found, enabling TLS for Redis connections
✅ level=info msg="Loading Redis CA certificate from file"
✅ level=debug msg="Using CA certificate for Redis TLS"
✅ level=info msg="Starting argocd-agent (agent)"
✅ level=info msg="Authentication successful"
✅ level=info msg="Connected to argocd-agent"
```

**✅ Full TLS with Certificate Validation:** The agent validates certificates properly via `localhost` (in cert SANs)!

**Keep this terminal running!**

---

### Step 9: Create Test Application

Get the cluster server URL:

```bash
kubectl get secret cluster-agent-managed -n argocd --context vcluster-control-plane -o jsonpath='{.data.server}' | base64 -d && echo
```

**Example output:** `https://192.168.1.2:9090?agentName=agent-managed`

Create an application (replace the server URL with yours):

```bash
cat <<EOF | kubectl apply --context vcluster-control-plane -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: guestbook
  namespace: agent-managed
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: guestbook
  destination:
    server: https://192.168.1.2:9090?agentName=agent-managed
    namespace: default
EOF
```

**Note:** Applications are created in the `agent-managed` namespace on the control plane.

---

### Step 10: Verify Application Sync

**Watch the application on control plane:**

```bash
kubectl get application guestbook -n agent-managed --context vcluster-control-plane
```

**Expected:** Progresses to `Synced` + `Healthy` (1-2 minutes)

**Check the application on managed agent:**

```bash
sleep 15
kubectl get application guestbook -n argocd --context vcluster-agent-managed
```

**Expected:** Shows `Synced` + `Healthy`

**Verify pods are running:**

```bash
kubectl get pods -n default --context vcluster-agent-managed
```

**Expected:** `guestbook-ui-*` pod in `Running` state

---

### Step 11: Access Argo CD UI

Get the admin password:

```bash
kubectl -n argocd get secret argocd-initial-admin-secret --context vcluster-control-plane -o jsonpath="{.data.password}" | base64 -d && echo
```

Start port-forward to the UI (in Terminal 3, after stopping agent Redis port-forward with Ctrl+C):

```bash
kubectl port-forward svc/argocd-server -n argocd 8080:443 --context vcluster-control-plane
```

Open browser: **http://localhost:8080**

- Username: `admin`
- Password: (from command above)

**Verify:** Application shows `Synced` + `Healthy` in the UI

---

### Step 12: Final Verification

Check for any Redis TLS errors during the entire test:

```bash
# Control plane (should return 0)
kubectl logs -n argocd -l app.kubernetes.io/name=argocd-redis --context vcluster-control-plane --since=10m | grep -i "error\|wrong version\|bad certificate" | wc -l

# Managed agent (should return 0)
kubectl logs -n argocd -l app.kubernetes.io/name=argocd-redis --context vcluster-agent-managed --since=10m | grep -i "error\|wrong version\|bad certificate" | wc -l
```

**Expected:** Both commands return `0` (no errors)

---

### ✅ Success Criteria

All must be true:

1. ✅ No Redis TLS errors in logs
2. ✅ Principal started with "Loading Redis upstream CA certificate from file"
3. ✅ Agent connected successfully with "Authentication successful"
4. ✅ Application synced to managed cluster
5. ✅ Pods deployed and running
6. ✅ Application visible in UI as Synced/Healthy

---

### Common Issues and Solutions

#### ❌ Error: "secret 'argocd-redis-tls' not found"

**When:** Running `configure-argocd-redis-for-tls.sh`

**Cause:** Redis TLS secret doesn't exist yet

**Fix:** This shouldn't happen if you followed the steps in order. The secret is created by `configure-argocd-redis-for-tls.sh` itself. If you see this error:

```bash
# Re-run the command - it creates the secret on first run
./configure-argocd-redis-for-tls.sh vcluster-control-plane
```

---

#### ❌ Error: "wrong version number" in Redis logs

**When:** After enabling Redis TLS

**Cause:** An Argo CD component is connecting without TLS

**Fix:** Argo CD component wasn't configured for TLS. Find which pod has the error:

```bash
kubectl get pods -n argocd -o wide --context vcluster-control-plane
```

Match the IP in the error to the pod, then reconfigure:

```bash
./configure-argocd-redis-for-tls.sh vcluster-control-plane
```

---

#### ❌ Error: "bad certificate" in Redis logs

**When:** After enabling Redis TLS

**Cause:** Component can't validate the certificate

**This shouldn't happen** if you used `configure-argocd-redis-for-tls.sh`!

**Fix:** Verify CA is mounted in the pod:

```bash
kubectl exec -n argocd <POD_NAME> --context vcluster-control-plane -- ls -la /app/config/redis-tls/
```

Should show `ca.crt`. If missing, reconfigure:

```bash
./configure-argocd-redis-for-tls.sh vcluster-control-plane
```

---

#### ❌ Port-forward keeps disconnecting

**Cause:** Network interruption or kubectl timeout

**Fix:** Use auto-restart wrapper:

```bash
while true; do
  kubectl port-forward svc/argocd-redis -n argocd 6381:6379 --context vcluster-agent-managed
  echo "Port-forward died, restarting in 2 seconds..."
  sleep 2
done
```

---

#### ❌ Agent can't connect to Redis

**Symptom:** Agent logs show "connection refused" or TLS errors

**Causes:**
1. Port-forward not running (Step 6)
2. Wrong port (should be 6381)
3. TLS not configured properly

**Fix:**
```bash
# 1. Verify port-forward is running
lsof -i :6381

# 2. Test Redis connection
REDIS_PASSWORD=$(kubectl get secret argocd-redis -n argocd --context vcluster-agent-managed -o jsonpath='{.data.auth}' | base64 -d)
redis-cli -h localhost -p 6381 --tls --insecure -a "$REDIS_PASSWORD" PING

# 3. If PONG works, restart agent
# If PONG fails, restart port-forward in Terminal 3
```

---

### Cleanup

Stop all processes:

```bash
# Terminal 1: Stop principal (Ctrl+C)
# Terminal 2: Stop agent (Ctrl+C)
# Terminal 3: Stop port-forwards (Ctrl+C)

# Delete test application
kubectl delete application guestbook -n agent-managed --context vcluster-control-plane

# (Optional) Disable Redis TLS if needed
kubectl delete secret argocd-redis-tls -n argocd --context vcluster-control-plane
kubectl delete secret argocd-redis-tls -n argocd --context vcluster-agent-managed
```

---

### Summary

This testing guide validates:

- ✅ **Certificate Generation**: Proper TLS certificates with correct SANs
- ✅ **Argo CD Components**: Server, repo-server, application-controller connect to Redis with TLS
- ✅ **argocd-agent Principal**: Redis proxy with TLS to upstream Redis
- ✅ **argocd-agent Agent**: Cluster cache Redis with TLS
- ✅ **Certificate Validation**: Proper CA-based validation (no insecure skip!)
- ✅ **End-to-End Flow**: Application sync through agent with all Redis connections encrypted

**All Redis communication is encrypted with proper TLS certificate validation!** 🔒✅

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

**configure-argocd-redis-for-tls.sh:**
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
   ./hack/dev-env/configure-argocd-redis-for-tls.sh vcluster-control-plane
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
     ./hack/dev-env/configure-argocd-redis-for-tls.sh $ctx
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
- Verify `--redis-ca-path` points to the correct CA certificate
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
   - Never use `--redis-tls-insecure` (used by both agent and principal) in production
   - This option disables certificate verification and is insecure

## Additional Resources

- [Redis TLS Support](https://redis.io/docs/latest/operate/oss_and_stack/management/security/encryption/)
- [OpenSSL Certificate Management](https://www.openssl.org/docs/man1.1.1/man1/openssl.html)
