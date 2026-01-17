# Redis TLS Configuration

> **Note:** Redis TLS is **enabled by default** in Kubernetes and Helm installations. This guide explains the configuration options and how to customize the setup.

This guide explains how to configure TLS encryption for Redis connections in argocd-agent to secure sensitive data in transit.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Quick Start](#quick-start)
- [Setup with argocd-agentctl](#setup-with-argocd-agentctl)
- [Local Development Environments](#local-development-environments)
- [Certificate Management](#certificate-management)
- [Kubernetes Installation](#kubernetes-installation)
- [Testing Guide](#testing-guide)

## Overview

The argocd-agent Redis proxy handles communication between Argo CD components and Redis. Without TLS, this traffic is transmitted in plain text, potentially exposing sensitive information such as application configurations, secrets references, and cluster state.

**Redis TLS is enabled by default** in Kubernetes and Helm installations to ensure secure communication.

Redis TLS provides encryption for:
- **Principal's Redis Proxy**: Connections from Argo CD components (server, repo-server) to the principal's Redis proxy
  - Uses secret: `argocd-redis-proxy-tls` (server cert + key)
- **Principal's Redis**: Connections from the principal's Redis proxy to its local argocd-redis instance
  - Uses secret: `argocd-redis-tls` (CA cert)
- **Agent's Redis**: Connections from agents to their local argocd-redis instances
  - Uses secret: `argocd-redis-tls` (CA cert)

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
  [Principal argocd-redis] ─────────────────  Principal Redis TLS
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

2. **Principal Redis TLS** (Principal only)
   - Secures connections from the principal's Redis proxy to its local argocd-redis
   - Requires: CA certificate to validate the Redis server's certificate
   - Configured via: `--redis-ca-path` or `--redis-ca-secret-name`

3. **Agent Redis TLS** (Agent only)
   - Secures connections from agents to their local argocd-redis
   - Requires: CA certificate to validate the Redis server's certificate
   - Configured via: `--redis-tls-ca-path` (for local development) or `--redis-tls-ca-secret-name` (recommended for production)

## Quick Start

### For Development/Testing

**Redis TLS is REQUIRED for all E2E tests** 
Tests will fail if Redis TLS is not properly configured. The `make setup-e2e` command automatically configures Redis TLS.

**Quick E2E Setup:**
```bash
make setup-e2e    # Automatically includes Redis TLS setup
make test-e2e     # Run tests
```

**Note:** `make test-e2e` runs tests directly against vclusters via LoadBalancer IPs. If you need to run principal/agents locally for debugging, use `make start-e2e` first (see [Local Development Environments](#local-development-environments)).

## Setup with argocd-agentctl

Redis TLS certificates and configuration are managed by `argocd-agentctl`:

```bash
# Generate Redis TLS certificates
argocd-agentctl redis generate-certs \
  --principal-context <context> \
  --principal-namespace argocd

# Configure Redis for TLS
argocd-agentctl redis configure-tls \
  --principal-context <context> \
  --principal-namespace argocd
```

**Note:** `make setup-e2e` uses these commands automatically. For production deployments with our manifests, Redis TLS is already configured—just create the secrets.

## Local Development Environments

### Standard Setup (Recommended)
E2E tests connect directly to Redis via LoadBalancer IPs. Requires:
- LoadBalancer support (MetalLB for local, cloud provider LB for remote)
- No port-forwards or reverse tunnel needed

### Local Principal with Remote vclusters
If running principal/agent locally while vclusters are remote, you need a reverse tunnel:

```bash
make setup-e2e
./hack/dev-env/reverse-tunnel/setup.sh  # Run in background
make start-e2e
make test-e2e
```

**Why?** vcluster networking isolation prevents pods from reaching the host machine directly. The reverse tunnel (`rathole`) bridges this gap.

See `hack/dev-env/reverse-tunnel/README.md` for details.

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
     - LoadBalancer IP addresses (for direct external access)
     - `rathole-container-internal` (for reverse tunnel when running principal locally)

**Note:** Redis TLS certificates are generated using `argocd-agentctl redis generate-certs`. The same certificate is used for both the Redis server and the principal's Redis proxy for simplicity.

## Kubernetes Installation

> **Important:** The default Kubernetes and Helm installations have Redis TLS **pre-configured** with `principal.redis.tls.enabled: "true"` and `agent.redis.tls.enabled: "true"`. You only need to create the TLS secrets with your certificates.

### Create TLS Secrets

First, generate certificates (see [Certificate Management](#certificate-management)), then create Kubernetes secrets containing the TLS materials:

```bash
# For Redis servers (in each cluster: control-plane, agent-managed, agent-autonomous)
kubectl create secret generic argocd-redis-tls \
  --from-file=tls.crt=redis.crt \
  --from-file=tls.key=redis.key \
  --from-file=ca.crt=ca.crt \
  -n argocd

# For Redis Proxy (only in principal/control-plane cluster)
kubectl create secret generic argocd-redis-proxy-tls \
  --from-file=tls.crt=redis-proxy.crt \
  --from-file=tls.key=redis-proxy.key \
  --from-file=ca.crt=ca.crt \
  -n argocd
```

**Secret Usage:**
- **`argocd-redis-tls`** (all clusters):
  - Contains: Redis server certificate (tls.crt, tls.key) and CA certificate (ca.crt)
  - Used by: Redis Deployment (server cert), Principal/Agents (CA to verify Redis)
  
- **`argocd-redis-proxy-tls`** (principal only):
  - Contains: Redis Proxy server certificate (tls.crt, tls.key) and CA certificate (ca.crt)
  - Used by: Principal's Redis Proxy (server cert to accept connections from Argo CD)

## Testing Guide

### Prerequisites

- LoadBalancer support in your Kubernetes cluster (MetalLB, cloud provider, etc.)
- Run `make setup-e2e` to configure vclusters with Redis TLS

### Run Tests

```bash
make test-e2e
```

Tests connect directly to Redis via LoadBalancer IPs with full TLS encryption.

**Note:** For debugging with local principal/agent processes, run `make start-e2e` before tests (see [Local Development Environments](#local-development-environments)).

### Verify Redis TLS

Test Redis TLS connection:

```bash
REDIS_PASSWORD=$(kubectl get secret argocd-redis -n argocd --context vcluster-control-plane \
  -o jsonpath='{.data.auth}' | base64 -d)

kubectl exec -n argocd --context vcluster-control-plane deployment/argocd-redis -- \
  redis-cli --tls --cacert /app/config/redis-tls/ca.crt -a "$REDIS_PASSWORD" ping
```

Expected: `PONG`

Check for TLS errors:

```bash
kubectl logs -n argocd -l app.kubernetes.io/name=argocd-redis --context vcluster-control-plane --since=5m | \
  grep -i "error\|wrong version\|bad certificate"
```

Expected: No output

## Additional Resources

- [Redis TLS Support](https://redis.io/docs/latest/operate/oss_and_stack/management/security/encryption/)

