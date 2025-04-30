/*
Package config provides functions and constants around the configuration of the
various argocd-agent components.
*/
package config

// SecretNamePrincipalCA is the name of the secret containing the TLS
// configuration for the principal's Certificate Authority
const SecretNamePrincipalCA = "argocd-agent-ca"

// SecretNamePrincipalGrpcTls is the name of the secret containing the TLS
// configuration for the principal's gRPC service.
const SecretNamePrincipalTls = "argocd-agent-principal-tls"

// SecretNamePrincipalProxyTls is the name of the secret containing the TLS
// configuration for the principal's resource proxy.
const SecretNameProxyTls = "argocd-agent-resource-proxy-tls"

// SecretNameAgentClientCert is the name of the secret containing the TLS
// client certificate + key for an agent.
const SecretNameAgentClientCert = "argocd-agent-client-tls"
