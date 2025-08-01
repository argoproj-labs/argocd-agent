# ArgoCD Agent Helm Chart: Installation and Configuration Guide
This guide provides step-by-step instructions on how to install the argocd-agent-agent-helm Helm chart from GitHub Container Registry (GHCR) and how to configure its various parameters using the values.yaml file.

## Prerequisites
Before you begin, ensure you have the following:

- Helm CLI (v3.8.0 or newer): Installed and configured on your local machine.
- Kubernetes Cluster: Access to a Kubernetes cluster where you want to deploy the agent.

## Helm Chart Installation
Use the following command to install the argocd-agent-agent-helm chart.

`helm install argocd-agent ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent-helm --version 0.1.0`

### Namespace Handling

If you run the helm install command without specifying a namespace flag, Helm will attempt to deploy resources into the `default` namespace.

If the target namespace set using flag `--set namespaceOverride=argocd`, does not exist, the installation will fail. 

Deploying to a Custom Namespace:
The chart can be deployed into a specific Kubernetes namespace using `--namespace` flag, and `--create-namespace` to create a namespace if not present. Or, it can also be set using `--set namespaceOverride=agent-namespce`.

```
helm install argocd-agent ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent-helm --version 0.1.0 --namespace=argocd --create-namespace
```

OR,

```
helm install argocd-agent ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent-helm --version 0.1.0 --set namespaceOverride=argocd
```


## Overriding Configuration Values
Configuration (values.yaml)
The values.yaml file allows you to customize the behavior of the ArgoCD Agent. Here's a breakdown of the available parameters:

Note:
__Default values for argocd-agent-agent.__
__This is a YAML-formatted file.__
__Declare variables to be passed into your templates.__

#### Namespace to deploy your agent in
```
namespaceOverride: ""
```

#### Secret names for argo-agent deployment

```
tlsSecretName: "argocd-agent-client-tls"
userPasswordSecretName: "argocd-agent-agent-userpass"
image: "ghcr.io/argoproj-labs/argocd-agent/argocd-agent"
imageTag: "latest"
```

#### config-map to config parameters for argocd-agent

```
agentMode: "autonomous"
auth: "mtls:any"
logLevel: "info"
server: "http://principal.server.address.com" 
serverPort: "443"
metricsPort: "8181"
tlsClientInSecure: "false"
healthzPort: "8002"
tlsClientKeyPath: "/app/config/tls/tls.key"
tlsClientCertPath: "/app/config/tls/tls.crt"
tlsRootCAPath: "/app/config/tls/ca.crt"
```

Parameter Descriptions:

namespaceOverride:

Default: ""

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
Valid credential identifier for this agent. Must be in the
format `<method>:<configuration>`. 
Valid values are:
- "userpass:_path_to_encrypted_creds_" where _path_to_encrypted_creds_ is
  the path to the file containing encrypted credential for authenticatiion.
- "mtls:_agent_id_regex_" where _agent_id_regex_ is the regex pattern for
  extracting the agent ID from client cert subject.

logLevel:

Default: "info"

The logging level for the agent. Valid values: "trace", "debug", "info", "warn", "error".

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

healthzPort:

Default: "8002"

The port the health check server should listen on.

tlsClientKeyPath: 

Default: "/app/config/tls/tls.key"

Path to a file containing the agent's TLS client certificate.

tlsClientCertPath: 

Default: "/app/config/tls/tls.crt"

Path to a file containing the agent's TLS client private key.

tlsRootCAPath: 

Default: "/app/config/tls/ca.crt"

The path to a file containing the certificates for the TLS root certificate authority used to validate the remote principal. 

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
  -f my-custom-values.yaml
```
Values provided via -f take precedence over the chart's default values.yaml. You can use multiple -f flags, with the rightmost file taking highest precedence.

By following these steps, you should be able to successfully install and configure your ArgoCD Agent Helm chart.