# ArgoCD Agent Helm Chart: Installation and Configuration Guide
This guide provides step-by-step instructions on how to install the argocd-agent-agent-helm Helm chart from GitHub Container Registry (GHCR) and how to configure its various parameters using the values.yaml file.

## Prerequisites
Before you begin, ensure you have the following:

Helm CLI (v3.8.0 or newer): Installed and configured on your local machine.

Kubernetes Cluster: Access to a Kubernetes cluster where you want to deploy the agent.

## Helm Chart Installation
Use the following command to install the argocd-agent-agent-helm chart.

`helm install argocd-agent ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent-helm --version 0.1.0`

Understanding the Command:

helm install argocd-agent: argocd-agent is the release name you assign to this specific installation of the chart. You can choose any unique name.

ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent-helm: This is the OCI (Open Container Initiative) URI pointing to your Helm chart in GHCR.

ghcr.io: The GitHub Container Registry host.

argoproj-labs: The GitHub organization or username that owns the package.

argocd-agent: An optional sub-path for organizing charts within the registry.

argocd-agent-agent-helm: The name of the Helm chart artifact.

--version 0.1.0: Specifies the exact version of the chart to install. It's highly recommended to always pin to a specific version.

## Namespace Handling
The chart can be deployed into a specific Kubernetes namespace.

Default Namespace:
Your values.yaml includes a default namespace:

namespace: "argocd"

If you run the helm install command without specifying a namespace flag, Helm will attempt to deploy resources into the argocd namespace.

Creating the Namespace:
If the target namespace (e.g., argocd) does not exist, the installation will fail. You can tell Helm to create it automatically:

`helm install argocd-agent ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent-helm --version 0.1.0 --create-namespace`

Deploying to a Custom Namespace:
To deploy the chart into a different namespace, use the --namespace flag. This will override the default specified in values.yaml for all namespace-scoped resources:

`helm install argocd-agent ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent-helm --version 0.1.0 --namespace my-custom-agent-ns --create-namespace`

## Overriding Configuration Values
Configuration (values.yaml)
The values.yaml file allows you to customize the behavior of the ArgoCD Agent. Here's a breakdown of the available parameters:

#### Default values for argocd-agent-agent.
#### This is a YAML-formatted file.
#### Declare variables to be passed into your templates.

#### Namespace to deploy your agent in
namespace: "argocd"

#### Secret names for argo-agent deployment
tlsSecretName: "argocd-agent-client-tls"
userPasswordSecretName: "argocd-agent-agent-userpass"
image: "ghcr.io/argoproj-labs/argocd-agent/argocd-agent"
imageTag: "latest"

#### config-map to config parameters for argocd-agent

agentMode: "autonomous"
auth: "mtls:any"
logLevel: "info"
agentNamespace: "argocd"
server: "http://principal.server.address.com"
serverPort: "443"
metricsPort: "8181"
tlsClientInSecure: "false"

Parameter Descriptions:

namespace:

Default: "argocd"

The Kubernetes namespace where the agent's resources (Deployment, Service, ConfigMap, etc.) will be deployed. This can be overridden by the helm install --namespace flag.

tlsSecretName:

Default: "argocd-agent-client-tls"

The name of the Kubernetes Secret containing TLS client certificates for the agent.

userPasswordSecretName:

Default: "argocd-agent-agent-userpass"

The name of the Kubernetes Secret containing user credentials if auth method is userpass.

image:

Default: "ghcr.io/argoproj-labs/argocd-agent/argocd-agent"

The Docker image repository for the ArgoCD Agent.

imageTag:

Default: "latest"

The tag of the Docker image to use. It's recommended to use a specific version tag in production.

agentMode:

Default: "autonomous"

The operating mode for the agent. Valid values are "autonomous" or "managed".

auth:

Default: "mtls:any"

The credential identifier for the agent's authentication method. Examples: "userpass:_path_to_encrypted_creds_" or "mtls:_agent_id_regex_".

logLevel:

Default: "info"

The logging level for the agent. Valid values: "trace", "debug", "info", "warn", "error".

agentNamespace:

Default: "argocd"

The Kubernetes namespace where the agent should operate and manage Argo CD resources.

server:

Default: "http://principal.server.address.com"

The remote address of the principal (Argo CD server) to connect to. Can be a DNS name or IP address.

serverPort:

Default: "443"

The remote port of the principal to connect to. Note: This value must be treated as a string in the ConfigMap.

metricsPort:

Default: "8181"

The port on which the agent's metrics server should listen. Note: This value must be treated as a string in the ConfigMap.

tlsClientInSecure:

Default: "false"

Whether to skip validation of the remote TLS credentials. Insecure; use only for development purposes. Note: This value must be treated as a string in the ConfigMap.

#### Overriding Configuration Values
You can override any of the default values in values.yaml during installation:

Using --set for individual values:
```
helm install argocd-agent ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent-helm --version 0.1.0 \
  --set logLevel="debug" \
  --set agentMode="managed" \
  --set server="https://my-argocd-server.com"
```
Using a custom values.yaml file:
Create a new YAML file (e.g., my-custom-values.yaml) with only the values you want to change:

### my-custom-values.yaml

namespace: "production-agents"
logLevel: "error"
server: "https://argocd.production.com"

Then, install with:

```
helm install argocd-agent ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent-helm --version 0.1.0 \
  -f my-custom-values.yaml --create-namespace
```
Values provided via -f take precedence over the chart's default values.yaml. You can use multiple -f flags, with the rightmost file taking highest precedence.

By following these steps, you should be able to successfully install and configure your ArgoCD Agent Helm chart.