# Argo CD Agent Principal Installation

## Prerequisites

### Redis TLS Certificates (Required)

**The principal requires Redis TLS certificates to be created before deployment.**

Redis TLS is **enabled by default** for security. The following secrets must exist before applying the principal manifests:

1. `argocd-redis-tls` - CA certificate for principal → Redis connections
2. `argocd-redis-proxy-tls` - TLS certificate for Argo CD → Redis proxy connections

### Quick Setup

```bash
# 1. Install the agent CLI
go install github.com/argoproj-labs/argocd-agent/cmd/argocd-agentctl@latest

# 2. Initialize the PKI (creates argocd-agent-ca secret)
argocd-agentctl pki init --principal-namespace argocd

# 3. Generate Redis TLS certificates
argocd-agentctl redis generate-certs \
  --principal-namespace argocd \
  --secret-name argocd-redis-tls \
  --upsert

# 4. Generate Redis proxy server certificate
argocd-agentctl redis generate-certs \
  --principal-namespace argocd \
  --secret-name argocd-redis-proxy-tls \
  --upsert

# 5. Apply the principal manifests
kubectl apply -k .
```

## Security Configuration

### Listen Host

**Default: `127.0.0.1` (localhost only)**

The principal binds to `127.0.0.1` by default to prevent external access. This is **critical for header-based authentication** where the service mesh or ingress controller adds authentication headers.

To change (not recommended):
```yaml
# principal-params-cm.yaml
principal.listen.host: ""  # Binds to all interfaces - USE ONLY WITH mTLS
```

### Network Policy

A NetworkPolicy is included to restrict access:
- **Port 6379 (Redis proxy)**: Only Argo CD components
- **Port 8443 (gRPC)**: Only agents
- **Ports 8000, 8003 (metrics, health)**: Only monitoring namespace

To disable the NetworkPolicy:
```bash
# Remove from kustomization.yaml
# - principal-redis-proxy-networkpolicy.yaml
```

## Disabling Redis TLS (Not Recommended)

For development/testing only:

```yaml
# principal-params-cm.yaml
principal.redis.tls.enabled: "false"
```

Then update the deployment:
```yaml
# principal-deployment.yaml
volumes:
- name: redis-proxy-server-tls
  secret:
    optional: true  # Change from false
- name: redis-tls-ca
  secret:
    optional: true  # Change from false
```

## Troubleshooting

### Principal fails with "secret not found"

```bash
# Check if secrets exist
kubectl get secrets -n argocd argocd-redis-tls argocd-redis-proxy-tls

# If missing, run the setup commands above
```

### Cannot connect to principal from agents

Check the listen host:
```bash
kubectl get cm argocd-agent-params -n argocd -o yaml | grep listen.host
```

If it's `127.0.0.1`, you need port-forwarding or a service mesh.

### Argo CD cannot connect to Redis proxy

```bash
# Check NetworkPolicy
kubectl get networkpolicy -n argocd

# Check if Argo CD pods have correct labels
kubectl get pods -n argocd -l app.kubernetes.io/part-of=argocd
```

