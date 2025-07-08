# ArgoCD Agent Helm Charts

This directory contains Helm charts for deploying the ArgoCD Agent components:

- `argocd-agent-principal` - The principal component that manages agent connections
- `argocd-agent-agent` - The agent component that runs on remote clusters

## Prerequisites

- Kubernetes cluster
- Helm 3.0+
- ArgoCD running in the cluster (for the principal)

## Installation

### Installing the Principal

The principal should be installed in the same cluster as ArgoCD:

```bash
helm install argocd-agent-principal ./argocd-agent-principal \
  --namespace argocd \
  --create-namespace
```

### Installing the Agent

The agent should be installed in remote clusters that will be managed by ArgoCD:

```bash
helm install argocd-agent-agent ./argocd-agent-agent \
  --namespace argocd \
  --create-namespace \
  --set agent.server.address=<principal-server-address>
```

## Configuration

### Principal Configuration

Key configuration options for the principal:

```yaml
principal:
  # Network configuration
  listenHost: ""
  listenPort: "8443"
  
  # ArgoCD namespace management
  namespace: "argocd"
  allowedNamespaces: ""
  
  # TLS configuration
  tls:
    secretName: "argocd-agent-principal-tls"
    server:
      allowGenerate: "false"  # Set to "true" for development only
    clientCert:
      require: "false"
  
  # Authentication
  auth: "userpass:/app/config/userpass/passwd"
```

### Agent Configuration

Key configuration options for the agent:

```yaml
agent:
  # Operation mode
  mode: "autonomous"
  
  # Server connection
  server:
    address: "argocd-agent-principal.example.com"
    port: "443"
  
  # TLS configuration
  tls:
    clientInsecure: "false"  # Set to "true" for development only
    rootCaSecretName: "argocd-agent-ca"
    secretName: "argocd-agent-client-tls"
  
  # Authentication
  creds: "userpass:/app/config/creds/userpass.creds"
```

## TLS Certificate Management

Both components require TLS certificates for secure communication:

### Principal Certificates

- `argocd-agent-principal-tls` - Server certificate for the principal
- `argocd-agent-ca` - CA certificate for validating agent connections
- `argocd-agent-jwt` - JWT signing key

### Agent Certificates

- `argocd-agent-client-tls` - Client certificate for agent authentication
- `argocd-agent-ca` - CA certificate for validating the principal

### Development Setup

For development and testing, you can enable certificate generation:

```yaml
# Principal values
principal:
  tls:
    server:
      allowGenerate: "true"
  jwt:
    allowGenerate: "true"

# Agent values
agent:
  tls:
    clientInsecure: "true"
```

## Authentication

### Username/Password Authentication

Create secrets for authentication:

```bash
# Principal userpass secret
kubectl create secret generic argocd-agent-principal-userpass \
  --from-literal=passwd=<encrypted-password-file> \
  --namespace argocd

# Agent userpass secret
kubectl create secret generic <release-name>-argocd-agent-agent-userpass \
  --from-literal=credentials=<encrypted-credentials-file> \
  --namespace argocd
```

### Mutual TLS Authentication

For mTLS authentication, configure the principal to require client certificates:

```yaml
principal:
  tls:
    clientCert:
      require: "true"
      matchSubject: "true"
  auth: "mtls:CN=(.+)"
```

## Monitoring

Both charts expose metrics endpoints:

- Principal: Port 8000
- Agent: Port 8181

Metrics services are created automatically and can be scraped by Prometheus.

## Upgrading

To upgrade the charts:

```bash
helm upgrade argocd-agent-principal ./argocd-agent-principal
helm upgrade argocd-agent-agent ./argocd-agent-agent
```

## Uninstallation

To remove the components:

```bash
helm uninstall argocd-agent-principal
helm uninstall argocd-agent-agent
```

## Troubleshooting

### Common Issues

1. **Connection Issues**: Verify network connectivity and TLS certificates
2. **Authentication Failures**: Check secret creation and credential formats
3. **Permission Errors**: Verify RBAC permissions and service account configuration

### Logs

Check component logs:

```bash
kubectl logs -n argocd deployment/argocd-agent-principal
kubectl logs -n argocd deployment/argocd-agent-agent
```

## Values Reference

See the `values.yaml` files in each chart directory for complete configuration options. 