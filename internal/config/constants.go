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
const SecretNameGrpcTls = "argocd-agent-grpc-tls"

// SecretNamePrincipalProxyTls is the name of the secret containing the TLS
// configuration for the principal's resource proxy.
const SecretNameProxyTls = "resource-proxy-tls"
