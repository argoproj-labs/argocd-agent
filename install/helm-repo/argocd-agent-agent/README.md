# argocd-agent-agent

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.4.1](https://img.shields.io/badge/AppVersion-0.4.1-informational?style=flat-square)

Argo CD Agent for connecting managed clusters to a Principal

**Homepage:** <https://github.com/argoproj-labs/argocd-agent>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Argo Project Maintainers |  | <https://github.com/argoproj-labs/argocd-agent> |

## Source Code

* <https://github.com/argoproj-labs/argocd-agent>

## Requirements

Kubernetes: `>=1.24.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for the agent Pod. |
| agentMode | string | `"autonomous"` | Agent mode of operation. |
| auth | string | `"mtls:any"` | Authentication mode for connecting to the principal. |
| healthzPort | string | `"8002"` | Healthz server port exposed by the agent. |
| image.pullPolicy | string | `"Always"` | Image pull policy for the agent container. |
| image.repository | string | `"ghcr.io/argoproj-labs/argocd-agent/argocd-agent"` | Container image repository for the agent. |
| image.tag | string | `"latest"` | Container image tag for the agent. |
| logLevel | string | `"info"` | Log level for the agent. |
| metricsPort | string | `"8181"` | Metrics server port exposed by the agent. |
| namespaceOverride | string | `""` | Override namespace to deploy the agent into. Leave empty to use the release namespace. |
| networkPolicy.enabled | bool | `true` |  |
| networkPolicy.redis.agentSelector."app.kubernetes.io/name" | string | `"argocd-agent-agent"` |  |
| networkPolicy.redis.enabled | bool | `true` |  |
| networkPolicy.redis.name | string | `"allow-agent-to-redis"` |  |
| networkPolicy.redis.namespace | string | `""` |  |
| networkPolicy.redis.redisSelector."app.kubernetes.io/name" | string | `"argocd-redis"` |  |
| nodeSelector | object | `{}` | Node selector for scheduling the agent Pod. |
| podAnnotations | object | `{}` | Additional annotations to add to the agent Pod. |
| podLabels | object | `{}` | Additional labels to add to the agent Pod. |
| priorityClassName | string | `""` | PriorityClassName for the agent Pod. |
| probes | object | `{"liveness":{"enabled":true,"failureThreshold":3,"httpGet":{"path":"/healthz","port":"healthz"},"initialDelaySeconds":10,"periodSeconds":10,"timeoutSeconds":2},"readiness":{"enabled":true,"failureThreshold":3,"httpGet":{"path":"/healthz","port":"healthz"},"initialDelaySeconds":5,"periodSeconds":10,"timeoutSeconds":2}}` | Liveness and readiness probe configuration. |
| probes.liveness.enabled | bool | `true` | Enable the liveness probe. |
| probes.liveness.failureThreshold | int | `3` | Failure threshold for liveness probe. |
| probes.liveness.initialDelaySeconds | int | `10` | Initial delay before the first liveness probe. |
| probes.liveness.periodSeconds | int | `10` | Frequency of liveness probes. |
| probes.liveness.timeoutSeconds | int | `2` | Timeout for liveness probe. |
| probes.readiness.enabled | bool | `true` | Enable the readiness probe. |
| probes.readiness.failureThreshold | int | `3` | Failure threshold for readiness probe. |
| probes.readiness.initialDelaySeconds | int | `5` | Initial delay before the first readiness probe. |
| probes.readiness.periodSeconds | int | `10` | Frequency of readiness probes. |
| probes.readiness.timeoutSeconds | int | `2` | Timeout for readiness probe. |
| redisAddress | string | `"argocd-redis:6379"` | Redis address used by the agent. |
| replicaCount | int | `1` | Number of replicas for the agent Deployment. |
| resources | object | `{"limits":{"cpu":"500m","memory":"512Mi"},"requests":{"cpu":"100m","memory":"128Mi"}}` | Resource requests and limits for the agent Pod. |
| server | string | `"principal.server.address.com"` | Principal server address (hostname or host:port). |
| serverPort | string | `"443"` | Principal server port. |
| service | object | `{"healthz":{"annotations":{},"port":8002,"targetPort":8002},"metrics":{"annotations":{},"port":8181,"targetPort":8181}}` | Service configuration for metrics and healthz endpoints. |
| service.healthz.annotations | object | `{}` | Annotations to add to the healthz Service. |
| service.healthz.port | int | `8002` | Service port for healthz. |
| service.healthz.targetPort | int | `8002` | Target port for healthz. |
| service.metrics.annotations | object | `{}` | Annotations to add to the metrics Service. |
| service.metrics.port | int | `8181` | Service port for metrics. |
| service.metrics.targetPort | int | `8181` | Target port for metrics. |
| serviceAccount | object | `{"annotations":{},"create":true,"name":""}` | ServiceAccount configuration. |
| serviceAccount.annotations | object | `{}` | Annotations to add to the ServiceAccount. |
| serviceAccount.create | bool | `true` | Whether to create the ServiceAccount. |
| serviceAccount.name | string | `""` | Name of the ServiceAccount to use. If empty, a name is generated. |
| tests | object | `{"enabled":"true","image":"bitnamilegacy/kubectl","tag":"1.33.4"}` | Configuration for chart tests. |
| tests.enabled | string | `"true"` | Enable chart tests. |
| tests.image | string | `"bitnamilegacy/kubectl"` | Test image. |
| tests.tag | string | `"1.33.4"` | Test image tag. |
| tlsClientCertPath | string | `"/app/config/tls/tls.crt"` |  |
| tlsClientInSecure | string | `"false"` | Whether to skip TLS verification for client connections. |
| tlsClientKeyPath | string | `"/app/config/tls/tls.key"` |  |
| tlsRootCAPath | string | `"/app/config/tls/ca.crt"` |  |
| tlsSecretName | string | `"argocd-agent-client-tls"` | Name of the TLS Secret containing client cert/key for mTLS. |
| tolerations | list | `[]` | Tolerations for the agent Pod. |
| userPasswordSecretName | string | `"argocd-agent-agent-userpass"` | Name of the Secret containing agent username/password (if used). |

