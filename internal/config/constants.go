/*
Package config provides functions and constants around the configuration of the
various argocd-agent components.
*/
package config

// SecretNamePrincipalCA is the name of the secret containing the TLS
// configuration for the principal's Certificate Authority
const SecretNamePrincipalCA = "argocd-agent-ca"

// SecretNameAgentCA is the name of the secret containing the TLS
// configuration for the agent's Certificate Authority
const SecretNameAgentCA = "argocd-agent-ca"

// SecretNamePrincipalTLS is the name of the secret containing the TLS
// configuration for the principal's gRPC service.
const SecretNamePrincipalTLS = "argocd-agent-principal-tls"

// SecretNameProxyTLS is the name of the secret containing the TLS
// configuration for the principal's resource proxy.
const SecretNameProxyTLS = "argocd-agent-resource-proxy-tls"

// SecretNameAgentClientCert is the name of the secret containing the TLS
// client certificate + key for an agent.
const SecretNameAgentClientCert = "argocd-agent-client-tls"

// SecretNameJWT is the name of the secret containing the JWT signing key
// for the principal.
const SecretNameJWT = "argocd-agent-jwt"

// SkipSyncLabel is the label used to skip sync for an application.
const SkipSyncLabel = "argocd-agent.argoproj-labs.io/ignore-sync"

// EnvKubeQPS is the name of the environment variable for setting the Kubernetes API QPS.
const EnvKubeQPS = "ARGOCD_AGENT_KUBE_QPS"

// EnvKubeBurst is the name of the environment variable for setting the Kubernetes API Burst.
const EnvKubeBurst = "ARGOCD_AGENT_KUBE_BURST"

// EnvRecvQueueSize is the name of the environment variable for setting the size of the queue.
const EnvRecvQueueSize = "ARGOCD_AGENT_RECV_QUEUE_SIZE"

// EnvSendQueueSize is the name of the environment variable for setting the size of the queue.
const EnvSendQueueSize = "ARGOCD_AGENT_SEND_QUEUE_SIZE"
