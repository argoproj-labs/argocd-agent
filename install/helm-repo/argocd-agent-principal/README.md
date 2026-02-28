# ArgoCD Agent Principal

This Helm chart installs the ArgoCD Agent Principal component, which is part of the ArgoCD Agent system that enables multi-cluster application deployment and management.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.2+
- ArgoCD installed in the cluster
- Redis instance for agent communication

## Installing the Chart

To install the chart with the release name `argocd-agent-principal`:

```bash
helm install argocd-agent-principal . -n argocd
```

To install with custom values:

```bash
helm install argocd-agent-principal . -n argocd -f values.yaml
```

## Uninstalling the Chart

To uninstall/delete the `argocd-agent-principal` deployment:

```bash
helm uninstall argocd-agent-principal -n argocd
```

## Configuration

The following table lists the configurable parameters of the ArgoCD Agent Principal chart and their default values.

### Basic Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `namespace` | Target namespace for deployment | `argocd` |
| `replicaCount` | Number of replicas | `1` |

### Image Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Image repository | `ghcr.io/argoproj-labs/argocd-agent/argocd-agent` |
| `image.tag` | Image tag | `"d7ee8580"` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |

### Resource Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `resources.limits.cpu` | CPU limit | `2` |
| `resources.limits.memory` | Memory limit | `4Gi` |
| `resources.requests.cpu` | CPU request | `2` |
| `resources.requests.memory` | Memory request | `4Gi` |

### Service Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `service.type` | Service type | `LoadBalancer` |
| `service.port` | Service port | `443` |
| `service.targetPort` | Target port | `8443` |
| `service.annotations` | Service annotations | `networking.gke.io/load-balancer-type: "Internal"` |

### Metrics Service

| Parameter | Description | Default |
|-----------|-------------|---------|
| `metricsService.enabled` | Enable metrics service | `true` |
| `metricsService.type` | Metrics service type | `ClusterIP` |
| `metricsService.port` | Metrics service port | `8000` |

### Health Check Service

| Parameter | Description | Default |
|-----------|-------------|---------|
| `healthzService.enabled` | Enable health check service | `true` |
| `healthzService.type` | Health check service type | `ClusterIP` |
| `healthzService.port` | Health check service port | `8003` |

### Principal Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `principal.listen.port` | gRPC server listen port | `8443` |
| `principal.listen.host` | gRPC server listen host | `""` (all interfaces) |
| `principal.log.level` | Log level (trace, debug, info, warn, error) | `info` |
| `principal.log.format` | Log format (text, json) | `text` |
| `principal.metrics.port` | Metrics server port | `8000` |
| `principal.healthz.port` | Health check server port | `8003` |
| `principal.namespace` | Principal operation namespace | `"argocd"` |
| `principal.allowedNamespaces` | Allowed namespaces for agents | `"argocd,argocd-apps,default"` |

### Namespace Management

| Parameter | Description | Default |
|-----------|-------------|---------|
| `principal.namespaceCreate.enable` | Allow namespace creation | `true` |
| `principal.namespaceCreate.pattern` | Namespace creation pattern | `"-agent"` |
| `principal.namespaceCreate.labels` | Labels for created namespaces | `"managed-by=argocd-agent,environment=production"` |

### TLS Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `principal.tls.secretName` | TLS secret name | `"argocd-agent-principal-tls"` |
| `principal.tls.server.allowGenerate` | Allow TLS cert generation | `false` |
| `principal.tls.server.rootCaSecretName` | Root CA secret name | `"argocd-agent-ca"` |
| `principal.tls.clientCert.require` | Require client certificates | `true` |
| `principal.tls.clientCert.matchSubject` | Match subject to agent name | `true` |

### Redis Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `principal.redis.compressionType` | Redis compression type | `"gzip"` |
| `principal.redis.server.address` | Redis server address | `"argocd-redis:6379"` |

### Resource Proxy

| Parameter | Description | Default |
|-----------|-------------|---------|
| `principal.redisProxy.enabled` | Enable Redis proxy | `true` |
| `principal.resourceProxy.enabled` | Enable resource proxy | `true` |
| `principal.resourceProxy.secretName` | Resource proxy TLS secret | `"argocd-agent-resource-proxy-tls"` |
| `principal.resourceProxy.ca.secretName` | Resource proxy CA secret | `"argocd-agent-ca"` |

### JWT Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `principal.jwt.allowGenerate` | Allow JWT key generation | `false` |
| `principal.jwt.secretName` | JWT secret name | `"argocd-agent-jwt"` |

### Advanced Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `principal.websocket.enable` | Enable WebSocket streaming | `false` |
| `principal.keepAlive.minInterval` | Keep-alive minimum interval | `"0"` |
| `principal.pprof.port` | pprof server port | `"0"` (disabled) |

### Secrets Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `secrets.ca.tls.create` | Create CA TLS secret | `true` |
| `secrets.ca.tls.key` | CA private key (base64) | `<provided>` |
| `secrets.ca.tls.crt` | CA certificate (base64) | `<provided>` |

## Usage

The principal component should be installed in the management cluster where ArgoCD is running. It will coordinate with the agent components installed in remote clusters.