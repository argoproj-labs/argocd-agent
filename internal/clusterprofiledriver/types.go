// Copyright 2024 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package clusterprofiledriver implements an OPTIONAL, standalone bridge
// between the Kubernetes SIG-Multicluster ClusterProfile API
// (sigs.k8s.io/cluster-inventory-api) and argocd-agent.
//
// It allows a cluster's argocd-agent registration (i.e. the information
// Argo CD needs in order to address that cluster's agent through the
// principal's resource proxy) to be described declaratively as a
// ClusterProfile object, instead of being created imperatively with
// `argocd-agentctl agent create`.
//
// The actual translation of a ClusterProfile into an Argo CD cluster Secret
// is performed by the separate `clusterprofile-integration-for-argocd`
// controller (https://github.com/argoproj-labs/clusterprofile-integration-for-argocd).
// This package only produces the ClusterProfile status (status.accessProviders)
// and a companion Secret holding a resource-proxy bearer token; it does not
// talk to Argo CD or write any Argo CD Secret directly.
//
// See docs/clusterprofile-integration/ for the full design write-up.
package clusterprofiledriver

const (
	// ProviderName is the AccessProvider name that this driver writes into
	// ClusterProfile.status.accessProviders[].name, and that must match the
	// "name" configured for this provider in clusterprofile-integration-for-argocd's
	// --clusterprofile-provider-file.
	ProviderName = "argocd-agent"

	// ClusterManagerName is the value that must be set in
	// ClusterProfile.spec.clusterManager.name for this driver to consider
	// the ClusterProfile as one it owns/manages. This mirrors the
	// recommended x-k8s.io/cluster-manager label from the ClusterProfile API.
	ClusterManagerName = "argocd-agent"

	// execExtensionKey is the reserved Cluster.Extensions[].name recognized by
	// the Kubernetes client authentication API (and honored end-to-end by
	// clusterprofile-integration-for-argocd) for passing structured,
	// per-cluster data to an exec credential plugin via
	// ExecCredential.Spec.Cluster.Config.
	//
	// NOTE: kept for documentation/compatibility, but argocd-agent-creds does
	// NOT rely on this as its primary source. Empirically (Argo CD v3.4.4),
	// ExecCredential.Spec.Cluster is nil for at least the API-discovery
	// client path used by GetResource/live-manifest/pod-logs, even though
	// the Secret's execProviderConfig.provideClusterInfo is true - some
	// internal Argo CD/gitops-engine rest.Config rebuild along that path
	// loses ProvideClusterInfo. additionalEnvVarsExtensionKey below does not
	// have this problem, since Env is applied unconditionally to the exec
	// plugin's process environment regardless of ProvideClusterInfo.
	execExtensionKey = "client.authentication.k8s.io/exec"

	// additionalEnvVarsExtensionKey is the reserved Cluster.Extensions[].name
	// defined by KEP 5339 and implemented by
	// sigs.k8s.io/cluster-inventory-api's pkg/access package (which
	// clusterprofile-integration-for-argocd uses). Any map[string]string
	// placed here is merged into the exec plugin's Env according to the
	// provider's profileSourcedEnvVarsPolicy (see
	// manifests/clusterprofile-provider-file-cm.yaml, which sets
	// "AppendIfNotExists"). This is the PRIMARY channel this driver uses to
	// tell argocd-agent-creds which agent/companion-Secret to use.
	additionalEnvVarsExtensionKey = "clusterprofiles.multicluster.x-k8s.io/exec/additional-envs"

	// Env var names read by argocd-agent-creds (cmd/argocd-agent-creds/main.go).
	// Exported so both this package (which advertises them via the
	// additionalEnvVarsExtensionKey extension) and cmd/argocd-agent-creds
	// (which reads them from its own process environment) share one
	// definition.
	EnvAgentName            = "ARGOCD_AGENT_CREDS_AGENT_NAME"
	EnvTokenSecretName      = "ARGOCD_AGENT_CREDS_TOKEN_SECRET_NAME"
	EnvTokenSecretNamespace = "ARGOCD_AGENT_CREDS_TOKEN_SECRET_NAMESPACE"
	EnvTokenSecretKey       = "ARGOCD_AGENT_CREDS_TOKEN_SECRET_KEY"

	// SharedClientCertSecretName is the name of the kubernetes.io/tls Secret
	// holding a client certificate signed by the principal's CA (argocd-agent-ca).
	//
	// The resource proxy's TLS listener is hardcoded to
	// tls.RequireAndVerifyClientCert (see argocd-agent's
	// cmd/argocd-agent/principal.go, getResourceProxyTLSConfigFromKube/Files) -
	// this is existing argocd-agent behavior we cannot and do not modify. A
	// bearer token alone cannot complete that TLS handshake, so
	// argocd-agent-creds also needs *some* CA-signed client certificate to
	// present. The cert does not need to be a specific agent's identity: the
	// resource proxy uses the JWT bearer token (Authorization header) to
	// determine which agent to route to (see principal/resource.go, "Method
	// 1: JWT bearer token"), independent of the cert's CN. So one shared
	// cert, provisioned once per driver-managed namespace via
	// `argocd-agentctl pki issue agent <name> --same-context --upsert`
	// (see setup-kind-poc.sh), is sufficient for every agent's ClusterProfile.
	SharedClientCertSecretName = "argocd-agent-client-tls"
)

const (
	// Env var names for the shared mTLS client certificate. Set once by the
	// driver's Reconciler (same value for every agent) via the same
	// additionalEnvVarsExtensionKey extension.
	EnvMTLSSecretName      = "ARGOCD_AGENT_CREDS_MTLS_SECRET_NAME"
	EnvMTLSSecretNamespace = "ARGOCD_AGENT_CREDS_MTLS_SECRET_NAMESPACE"

	// TokenSecretKey is the key inside the companion token Secret that holds
	// the resource-proxy bearer token.
	TokenSecretKey = "token"

	// TokenSecretNameSuffix is appended to the ClusterProfile name to derive
	// the companion token Secret's name.
	TokenSecretNameSuffix = "-argocd-agent-token"

	// clusterAgentMappingLabel MUST mirror
	// internal/argocd/cluster.LabelKeyClusterAgentMapping exactly (that
	// constant lives in argocd-agent's *existing* principal code, which we
	// are not permitted to modify). The argocd-agent principal's own
	// in-memory cluster informer (separate from clusterprofile-integration-for-argocd)
	// uses this label - not anything ClusterProfile-related - to learn
	// which Argo CD cluster Secret corresponds to which connected agent.
	// Without it, the principal logs "agent X is not mapped to any
	// cluster" and never routes resource/app events for that agent.
	//
	// clusterprofile-integration-for-argocd copies every label from the
	// source ClusterProfile onto the Secret it generates (controller.go,
	// mutateSecret), so setting this label on the ClusterProfile is
	// sufficient to get it onto the resulting Argo CD cluster Secret too.
	// This driver self-heals the label onto the ClusterProfile so operators
	// do not have to remember to add it by hand.
	clusterAgentMappingLabel = "argocd-agent.argoproj-labs.io/agent-name"
)

// ExecExtension is the payload embedded in a ClusterProfile's
// status.accessProviders[].cluster.extensions[] entry named
// "client.authentication.k8s.io/exec". It is passed verbatim (as
// ExecCredential.Spec.Cluster.Config) to the argocd-agent-creds exec plugin
// at credential-resolution time, telling it which agent it is fetching a
// token for and where to find that token.
type ExecExtension struct {
	// AgentName is the argocd-agent agent name (matches the ?agentName=
	// query parameter on the cluster's server URL, and the Subject claim
	// inside the resource-proxy JWT).
	AgentName string `json:"agentName"`

	// TokenSecretName is the name of the companion Secret holding the
	// resource-proxy bearer token for this agent.
	TokenSecretName string `json:"tokenSecretName"`

	// TokenSecretNamespace is the namespace of the companion Secret. It is
	// always the same namespace as the source ClusterProfile.
	TokenSecretNamespace string `json:"tokenSecretNamespace"`

	// TokenSecretKey is the data key inside the companion Secret.
	TokenSecretKey string `json:"tokenSecretKey"`
}
