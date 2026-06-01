# argocd-agent-principal

![Version: 0.3.1](https://img.shields.io/badge/Version-0.3.1-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.8.1](https://img.shields.io/badge/AppVersion-v0.8.1-informational?style=flat-square)

Argo CD Agent Principal component for multi-cluster application management

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
| affinity | object | `{}` | Affinity rules for the principal Pod. |
| configMapData | object | `{}` | Arbitrary extra keys/values merged into the params ConfigMap. Use this to set parameters not exposed by the chart without forking. |
| dnsConfig | object | `{}` | DNS config for the Pod. Only honored when `dnsPolicy` is "None". |
| dnsPolicy | string | `""` | DNS policy for the Pod (e.g. ClusterFirst, None). Empty leaves the default. |
| fullnameOverride | string | `""` | Override the fully-qualified resource name. |
| healthzService | object | `{"annotations":{},"enabled":true,"port":8003,"type":"ClusterIP"}` | Healthz (readiness/liveness) service configuration. |
| healthzService.annotations | object | `{}` | Annotations to add to the healthz Service. |
| healthzService.enabled | bool | `true` | Whether to create a healthz Service. |
| healthzService.port | int | `8003` | Healthz service port. |
| healthzService.type | string | `"ClusterIP"` | Healthz service type. |
| hostAliases | list | `[]` | Host aliases injected into /etc/hosts of the principal Pod. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy for the principal container. |
| image.repository | string | `"quay.io/argoprojlabs/argocd-agent"` | Container image repository for the principal. |
| image.tag | string | `""` | Overrides the image tag whose default is the chart appVersion. |
| imagePullSecrets | list | `[]` | Image pull secrets for private registries. |
| metricsService | object | `{"annotations":{},"enabled":true,"port":8000,"type":"ClusterIP"}` | Metrics service configuration. |
| metricsService.annotations | object | `{}` | Annotations to add to the metrics Service. |
| metricsService.enabled | bool | `true` | Whether to create a metrics Service. |
| metricsService.port | int | `8000` | Metrics service port. |
| metricsService.type | string | `"ClusterIP"` | Metrics service type. |
| nameOverride | string | `""` | Override the chart name used in app.kubernetes.io/name. Changing after initial install requires delete+reinstall. |
| namespaceOverride | string | `""` | Override namespace to deploy the principal into. Leave empty to use the release namespace. |
| networkPolicy | object | `{"enabled":false,"ingress":{"extraRules":[],"grpc":{"enabled":true},"metrics":{"enabled":true,"namespaceSelector":"monitoring"},"redisProxy":{"enabled":true}}}` | NetworkPolicy configuration. Restricts ingress to the principal Pod. Requires the cluster to support NetworkPolicy (CNI plugin such as Calico, Cilium, or Weave). |
| networkPolicy.enabled | bool | `false` | Whether to create a NetworkPolicy for the principal Pod.  NOTE on cross-cluster agents: a NetworkPolicy can only match in-cluster sources via pod/namespace selectors. Remote agents (running on a different cluster) reach the principal as external traffic and cannot be matched by selectors. To allow them, either keep this disabled, or add explicit `ipBlock` rules via `networkPolicy.ingress.extraRules` for the remote agent CIDRs. |
| networkPolicy.ingress.extraRules | list | `[]` | Additional custom ingress rules to append. Use this to add `ipBlock` entries for remote (cross-cluster) agents. |
| networkPolicy.ingress.grpc.enabled | bool | `true` | Allow gRPC port (principal.listen.port) from pods labeled as argocd-agent components in any namespace on the cluster. |
| networkPolicy.ingress.metrics.enabled | bool | `true` | Allow metrics and healthz ports from the specified namespace (e.g. Prometheus). |
| networkPolicy.ingress.metrics.namespaceSelector | string | `"monitoring"` | Namespace label value for kubernetes.io/metadata.name to allow metrics scraping from. |
| networkPolicy.ingress.redisProxy.enabled | bool | `true` | Allow Redis proxy port (6379) from pods labeled as ArgoCD components in any namespace on the cluster. Has no effect when principal.redisProxy.enabled is false. |
| nodeSelector | object | `{}` | Node selector for scheduling the principal Pod. |
| podAnnotations | object | `{}` | Additional annotations to add to the principal Pod. |
| podLabels | object | `{}` | Additional labels to add to the principal Pod. |
| podSecurityContext | object | `{"fsGroup":999,"fsGroupChangePolicy":"OnRootMismatch","runAsGroup":999,"runAsNonRoot":true,"runAsUser":999,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod-level securityContext. Applied to the Pod spec. |
| principal.additionalArgs | list | `[]` | Extra CLI arguments to pass to the principal binary. |
| principal.allowedNamespaces | string | `""` | Comma-separated list of additional namespaces the principal watches. Supports glob patterns. |
| principal.auth | string | `"mtls:CN=([^,]+)"` | Authentication method. Formats: "mtls:CN=([^,]+)", "userpass:/path/to/creds", "header:x-forwarded-client-cert:^.*URI=spiffe://...". |
| principal.destinationBasedMapping | bool | `false` | Enable destination-based mapping (deploys apps into their declared namespace instead of the agent namespace). |
| principal.eventProcessors | string | `"10"` | Number of concurrent event processors. |
| principal.healthz.port | int | `8003` | Health check server port. |
| principal.jwt.allowGenerate | bool | `false` | Allow the principal to generate its own JWT signing key on startup. INSECURE — development only. |
| principal.jwt.keyPath | string | `""` | Path to the JWT private key file inside the container. |
| principal.jwt.secretName | string | `"argocd-agent-jwt"` | Name of the Secret containing the JWT signing key. |
| principal.keepAlive.minInterval | string | `"0"` | Minimum keep-alive interval for gRPC connections. |
| principal.labelSelector | string | `""` | Kubernetes label selector to restrict which resources the principal watches. |
| principal.listen.host | string | `"127.0.0.1"` | Interface address to listen on. Use "127.0.0.1" for header-based auth (prevents bypass); use "" (all interfaces) only with mTLS. |
| principal.listen.port | int | `8443` | gRPC server port. |
| principal.log.format | string | `"text"` | Log format (text or json). |
| principal.log.level | string | `"info"` | Log level (trace, debug, info, warn, error). |
| principal.metrics.port | int | `8000` | Metrics server port. |
| principal.namespace | string | `"argocd"` | Namespace the principal operates in (syncs ArgoCD apps from/to). |
| principal.namespaceCreate.enable | bool | `false` | Allow the principal to create namespaces for agents when they don't exist. |
| principal.namespaceCreate.labels | string | `""` | Labels to apply to auto-created namespaces (e.g. "key=value,key2=value2"). |
| principal.namespaceCreate.pattern | string | `""` | Regexp pattern to restrict namespace names that may be auto-created. |
| principal.pprof.port | string | `"0"` | Port for the pprof profiling server. 0 disables it. |
| principal.redis.compressionType | string | `"gzip"` | Redis compression type for agent event streams (gzip, none). |
| principal.redis.proxy.server.tls.certPath | string | `"/app/config/redis-proxy-server-tls/tls.crt"` | Path to the TLS certificate for the Redis proxy server. |
| principal.redis.proxy.server.tls.keyPath | string | `"/app/config/redis-proxy-server-tls/tls.key"` | Path to the TLS key for the Redis proxy server. |
| principal.redis.proxy.server.tls.secretName | string | `"argocd-redis-proxy-tls"` | Name of the Secret containing the Redis proxy server TLS cert/key. |
| principal.redis.server.address | string | `"argocd-redis:6379"` | Redis server address (host:port). |
| principal.redis.tls | object | `{"caPath":"/app/config/redis-tls/ca.crt","caSecretName":"argocd-redis-tls","enabled":false,"insecure":false}` | Redis TLS configuration. |
| principal.redis.tls.caPath | string | `"/app/config/redis-tls/ca.crt"` | Path to CA certificate for verifying the Redis TLS certificate (must match volume mount). |
| principal.redis.tls.caSecretName | string | `"argocd-redis-tls"` | Name of the Secret containing the Redis TLS CA certificate (key: ca.crt). |
| principal.redis.tls.enabled | bool | `false` | Enable TLS for Redis connections. |
| principal.redis.tls.insecure | bool | `false` | Skip verification of the Redis TLS certificate. INSECURE — development only. |
| principal.redisProxy.enabled | bool | `true` | Enable the Redis proxy sidecar. |
| principal.resourceProxy.ca.path | string | `""` | Path to the resource proxy CA certificate inside the container. |
| principal.resourceProxy.ca.secretName | string | `"argocd-agent-ca"` | Name of the Secret containing the CA certificate for the resource proxy. |
| principal.resourceProxy.enabled | bool | `true` | Enable the resource proxy (required for destination-based mapping). |
| principal.resourceProxy.secretName | string | `"argocd-agent-resource-proxy-tls"` | Name of the Secret containing the resource proxy TLS cert/key. |
| principal.resourceProxy.tls.certPath | string | `""` | Path to the resource proxy TLS certificate inside the container. |
| principal.resourceProxy.tls.keyPath | string | `""` | Path to the resource proxy TLS key inside the container. |
| principal.tls.ciphersuites | string | `""` | Comma-separated list of TLS cipher suites. Empty uses Go defaults. |
| principal.tls.clientCert.matchSubject | bool | `false` | Match the subject field in the client certificate to the agent's registered name. |
| principal.tls.clientCert.require | bool | `false` | Require agents to present a client certificate upon connection. |
| principal.tls.insecurePlaintext | bool | `false` | Disable TLS and accept plaintext connections. Use only with a sidecar (e.g. Istio) that handles TLS. |
| principal.tls.maxVersion | string | `""` | Maximum TLS version (tls1.1, tls1.2, tls1.3). Empty uses highest available. |
| principal.tls.minVersion | string | `"tls1.3"` | Minimum TLS version (tls1.1, tls1.2, tls1.3). Empty uses Go default. |
| principal.tls.secretName | string | `"argocd-agent-principal-tls"` | Name of the Secret containing the principal TLS certificate and key. |
| principal.tls.server.allowGenerate | bool | `false` | Allow the principal to generate its own TLS cert on startup. INSECURE — development only. |
| principal.tls.server.certPath | string | `""` | Path to the TLS certificate file inside the container. |
| principal.tls.server.keyPath | string | `""` | Path to the TLS private key file inside the container. |
| principal.tls.server.rootCaPath | string | `""` | Path to the root CA certificate file inside the container. |
| principal.tls.server.rootCaSecretName | string | `"argocd-agent-ca"` | Name of the Secret containing the root CA certificate for verifying agent client certs. |
| principal.userpass.secretName | string | `"argocd-agent-principal-userpass"` | Name of the Secret containing encrypted agent credentials for userpass authentication. Only required when principal.auth is set to userpass mode. The secret must have a key "passwd" with the encrypted credentials file contents. |
| principal.websocket.enable | bool | `false` | Enable WebSocket for streaming events to agents. |
| priorityClassName | string | `""` | PriorityClassName for the principal Pod. |
| probes | object | `{"liveness":{"enabled":true,"failureThreshold":3,"httpGet":{"path":"/healthz","port":"healthz"},"initialDelaySeconds":10,"periodSeconds":10,"timeoutSeconds":2},"readiness":{"enabled":true,"failureThreshold":3,"httpGet":{"path":"/healthz","port":"healthz"},"initialDelaySeconds":5,"periodSeconds":10,"timeoutSeconds":2}}` | Liveness and readiness probe configuration. |
| probes.liveness | object | `{"enabled":true,"failureThreshold":3,"httpGet":{"path":"/healthz","port":"healthz"},"initialDelaySeconds":10,"periodSeconds":10,"timeoutSeconds":2}` | Liveness probe configuration. |
| probes.liveness.enabled | bool | `true` | Enable the liveness probe. |
| probes.liveness.failureThreshold | int | `3` | Failure threshold for liveness probe. |
| probes.liveness.initialDelaySeconds | int | `10` | Initial delay before the first liveness probe. |
| probes.liveness.periodSeconds | int | `10` | Frequency of liveness probes. |
| probes.liveness.timeoutSeconds | int | `2` | Timeout for liveness probe. |
| probes.readiness | object | `{"enabled":true,"failureThreshold":3,"httpGet":{"path":"/healthz","port":"healthz"},"initialDelaySeconds":5,"periodSeconds":10,"timeoutSeconds":2}` | Readiness probe configuration. |
| probes.readiness.enabled | bool | `true` | Enable the readiness probe. |
| probes.readiness.failureThreshold | int | `3` | Failure threshold for readiness probe. |
| probes.readiness.initialDelaySeconds | int | `5` | Initial delay before the first readiness probe. |
| probes.readiness.periodSeconds | int | `10` | Frequency of readiness probes. |
| probes.readiness.timeoutSeconds | int | `2` | Timeout for readiness probe. |
| progressDeadlineSeconds | int | `600` | Time allowed for the Deployment to make progress before the controller reports failure. |
| rbac | object | `{"create":true,"createClusterRole":true}` | RBAC configuration. |
| rbac.create | bool | `true` | Create namespace-scoped Role and RoleBinding for the principal. |
| rbac.createClusterRole | bool | `true` | Create cluster-scoped ClusterRole and ClusterRoleBinding. Required for destination-based mapping and multi-namespace resource watching. |
| redisPasswordKey | string | `"auth"` | Key within the Redis Secret that holds the password. |
| redisSecretName | string | `"argocd-redis"` | Name of the Secret containing the ArgoCD Redis password. |
| replicaCount | int | `1` | Number of replicas for the principal Deployment. |
| resources | object | `{"limits":{"cpu":"2","memory":"4Gi"},"requests":{"cpu":"500m","memory":"512Mi"}}` | Resource requests and limits for the principal Pod. |
| revisionHistoryLimit | int | `10` | Number of old ReplicaSets to retain for rollback. |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"privileged":false,"readOnlyRootFilesystem":true,"runAsGroup":999,"runAsNonRoot":true,"runAsUser":999,"seccompProfile":{"type":"RuntimeDefault"}}` | Container-level securityContext. Applied to the principal container. |
| service | object | `{"annotations":{},"port":443,"targetPort":8443,"type":"LoadBalancer"}` | Main gRPC service configuration for agent connections. |
| service.annotations | object | `{}` | Annotations to add to the gRPC Service. |
| service.port | int | `443` | External service port. |
| service.targetPort | int | `8443` | Container port the gRPC server listens on. |
| service.type | string | `"LoadBalancer"` | Service type. Use LoadBalancer to expose externally, ClusterIP for in-cluster only. |
| serviceAccount | object | `{"annotations":{},"automountServiceAccountToken":true,"create":true,"name":""}` | ServiceAccount configuration. |
| serviceAccount.annotations | object | `{}` | Annotations to add to the ServiceAccount (e.g. IRSA on AWS, Workload Identity on GCP). |
| serviceAccount.automountServiceAccountToken | bool | `true` | Automount API credentials for the Service Account. |
| serviceAccount.create | bool | `true` | Whether to create the ServiceAccount. |
| serviceAccount.name | string | `""` | Name of the ServiceAccount to use. If empty, a name is generated. |
| serviceMonitor | object | `{"additionalLabels":{},"annotations":{},"enabled":false,"honorLabels":false,"interval":"30s","metricRelabelings":[],"namespace":"","relabelings":[],"scheme":"","scrapeTimeout":"10s","tlsConfig":{}}` | Prometheus ServiceMonitor configuration. |
| serviceMonitor.additionalLabels | object | `{}` | Additional labels for the ServiceMonitor. |
| serviceMonitor.annotations | object | `{}` | Annotations for the ServiceMonitor. |
| serviceMonitor.enabled | bool | `false` | Whether to create a ServiceMonitor resource. |
| serviceMonitor.honorLabels | bool | `false` | When true, preserves metric labels when they collide with the target's labels. |
| serviceMonitor.interval | string | `"30s"` | Prometheus scrape interval. |
| serviceMonitor.metricRelabelings | list | `[]` | Prometheus MetricRelabelConfigs to apply to samples before ingestion. |
| serviceMonitor.namespace | string | `""` | Namespace where the ServiceMonitor should be created. Defaults to release namespace. |
| serviceMonitor.relabelings | list | `[]` | Prometheus RelabelConfigs to apply to samples before scraping. |
| serviceMonitor.scheme | string | `""` | Prometheus ServiceMonitor scheme. |
| serviceMonitor.scrapeTimeout | string | `"10s"` | Prometheus scrape timeout. |
| serviceMonitor.tlsConfig | object | `{}` | Prometheus ServiceMonitor TLS configuration. |
| terminationGracePeriodSeconds | int | `30` | Grace period for Pod termination (seconds). |
| tests | object | `{"enabled":false,"image":"bitnamilegacy/kubectl","tag":"1.33.4"}` | Helm chart test configuration. |
| tests.enabled | bool | `false` | By default, chart tests are disabled. |
| tests.image | string | `"bitnamilegacy/kubectl"` | Test image. |
| tests.tag | string | `"1.33.4"` | Test image tag. |
| tolerations | list | `[]` | Tolerations for the principal Pod. |
| topologySpreadConstraints | list | `[]` | Topology spread constraints for the principal Pod. |
| updateStrategy | object | `{"rollingUpdate":{"maxSurge":"25%","maxUnavailable":"25%"},"type":"RollingUpdate"}` | Deployment update strategy (passed through as `spec.strategy`). |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
