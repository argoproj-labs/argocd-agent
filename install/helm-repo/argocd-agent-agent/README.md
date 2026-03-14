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
| allowedNamespaces | string | `""` | Comma-separated list of additional namespaces the agent is allowed to manage applications in (used with applications in any namespace feature). Supports glob patterns (e.g., "team-*,prod-*"). |
| appLabelSelector | string | `""` | Kubernetes label selector to restrict which Applications the agent watches. Only matching Applications will be listed, watched, and processed. |
| argoCdRedisPasswordKey | string | `"auth"` | ArgoCD Redis password key. |
| argoCdRedisSecretName | string | `"argocd-redis"` | ArgoCD Redis password secret name. |
| auth | string | `"mtls:any"` | Authentication mode for connecting to the principal. |
| cacheRefreshInterval | string | `"10s"` | Cache refresh interval. |
| createNamespace | bool | `false` | Whether to create target namespaces automatically when they don't exist. Used with destination-based mapping. |
| destinationBasedMapping | bool | `false` | Whether to enable destination-based mapping. When enabled, the agent creates applications in their original namespace (preserving the namespace from the principal) instead of the agent's own namespace. |
| enableCompression | bool | `false` | Whether to enable gRPC compression. |
| enableResourceProxy | bool | `true` | Whether to enable resource proxy. |
| enableWebSocket | bool | `false` | Whether to enable WebSocket connections. |
| healthzPort | string | `"8002"` | Healthz server port exposed by the agent. |
| image.pullPolicy | string | `"Always"` | Image pull policy for the agent container. |
| image.repository | string | `"ghcr.io/argoproj-labs/argocd-agent/argocd-agent"` | Container image repository for the agent. |
| image.tag | string | `"latest"` | Container image tag for the agent. |
| keepAliveInterval | string | `"50s"` | Keep-alive interval for connections. |
| logFormat | string | `"text"` | Log format for the agent (text or json). |
| logLevel | string | `"info"` | Log level for the agent. |
| metricsPort | string | `"8181"` | Metrics server port exposed by the agent. |
| namespaceOverride | string | `""` | Override namespace to deploy the agent into. Leave empty to use the release namespace. |
| nodeSelector | object | `{}` | Node selector for scheduling the agent Pod. |
| podAnnotations | object | `{}` | Additional annotations to add to the agent Pod. |
| podLabels | object | `{}` | Additional labels to add to the agent Pod. |
| pprofPort | string | `"0"` | Port for pprof server (0 disables pprof). |
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
| redisTLS | object | `{"caPath":"/app/config/redis-tls/ca.crt","enabled":false,"insecure":false,"secretName":"argocd-redis-tls"}` | Redis TLS configuration. |
| redisTLS.caPath | string | `"/app/config/redis-tls/ca.crt"` | Path to CA certificate for verifying Redis TLS certificate. This path is where the CA certificate will be mounted inside the container. |
| redisTLS.enabled | bool | `false` | Enable TLS for Redis connections. |
| redisTLS.insecure | bool | `false` | Skip verification of Redis TLS certificate (INSECURE - for development only). |
| redisTLS.secretName | string | `"argocd-redis-tls"` | Name of the Kubernetes Secret containing the Redis TLS CA certificate. The secret should have a key 'ca.crt' containing the CA certificate in PEM format. Set to empty string to disable mounting (requires system CAs or insecure mode). |
| redisUsername | string | `""` | Redis username for authentication. |
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
| serviceAccount | object | `{"annotations":{},"automountServiceAccountToken":true,"create":true,"name":""}` | ServiceAccount configuration. |
| serviceAccount.annotations | object | `{}` | Annotations to add to the ServiceAccount. |
| serviceAccount.automountServiceAccountToken | bool | `true` | Automount API credentials for the Service Account |
| serviceAccount.create | bool | `true` | Whether to create the ServiceAccount. |
| serviceAccount.name | string | `""` | Name of the ServiceAccount to use. If empty, a name is generated. |
| serviceMonitor | object | `{"additionalLabels":{},"annotations":{},"enabled":false,"honorLabels":false,"interval":"30s","metricRelabelings":[],"namespace":"","relabelings":[],"scheme":"","scrapeTimeout":"10s","tlsConfig":{}}` | Prometheus ServiceMonitor configuration. |
| serviceMonitor.additionalLabels | object | `{}` | Prometheus ServiceMonitor labels |
| serviceMonitor.annotations | object | `{}` | Prometheus ServiceMonitor annotations |
| serviceMonitor.enabled | bool | `false` | Whether to create a ServiceMonitor resource. |
| serviceMonitor.honorLabels | bool | `false` | When true, honorLabels preserves the metric’s labels when they collide with the target’s labels. |
| serviceMonitor.interval | string | `"30s"` | Prometheus scrape interval. Must be a valid duration string (e.g. "30s"). |
| serviceMonitor.metricRelabelings | list | `[]` | Prometheus [MetricRelabelConfigs] to apply to samples before ingestion |
| serviceMonitor.namespace | string | `""` | Namespace where the ServiceMonitor should be created. Defaults to release namespace. |
| serviceMonitor.relabelings | list | `[]` | Prometheus [RelabelConfigs] to apply to samples before scraping |
| serviceMonitor.scheme | string | `""` | Prometheus ServiceMonitor scheme |
| serviceMonitor.scrapeTimeout | string | `"10s"` | Prometheus scrape timeout. Must be a valid duration string (e.g. "10s"). |
| serviceMonitor.tlsConfig | object | `{}` | Prometheus ServiceMonitor tlsConfig |
| tests | object | `{"enabled":false,"image":"bitnamilegacy/kubectl","tag":"1.33.4"}` | Configuration for helm-chart tests. |
| tests.enabled | bool | `false` | By default, chart tests are disabled. |
| tests.image | string | `"bitnamilegacy/kubectl"` | Test image. |
| tests.tag | string | `"1.33.4"` | Test image tag. |
| tlsCipherSuites | string | `""` | Comma-separated list of TLS cipher suites. Empty uses Go defaults. |
| tlsClientCertPath | string | `""` | Path to the TLS client certificate. |
| tlsClientInSecure | string | `"false"` | Whether to skip TLS verification for client connections. |
| tlsClientKeyPath | string | `""` | Path to the TLS client key. |
| tlsMaxVersion | string | `""` | Maximum TLS version to use (tls1.1, tls1.2, tls1.3). Empty uses highest available. |
| tlsMinVersion | string | `""` | Minimum TLS version to use (tls1.1, tls1.2, tls1.3). Empty uses Go default. |
| tlsRootCAPath | string | `""` | Path to the TLS root CA certificate. |
| tlsRootCASecretName | string | `"argocd-agent-ca"` | Name of the Secret containing root CA certificate. |
| tlsSecretName | string | `"argocd-agent-client-tls"` | Name of the TLS Secret containing client cert/key for mTLS. |
| tolerations | list | `[]` | Tolerations for the agent Pod. |
| userPasswordSecretName | string | `"argocd-agent-agent-userpass"` | Name of the Secret containing agent username/password (if used). |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.13.1](https://github.com/norwoodj/helm-docs/releases/v1.13.1)
