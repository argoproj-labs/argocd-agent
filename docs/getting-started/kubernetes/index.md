# Installation on Kubernetes

These instructions should apply to most Kubernetes distributions, including if you partitioned a cluster using vcluster. Please make sure to read and understand the [Overview](../index.md) page, because the requirements set out there apply for these instructions.

## Assumptions

* You have at least two clusters. One for hosting the control plane, and one or more additional clusters to host your workloads. It's perfectly fine to use [vcluster](https://github.com/loft-sh/vcluster) for this purpose.
* You have administrative access to all of these clusters, and your Kubernetes client is set up to access these environments
* You are familiar with both, Argo CD and its concepts as well as managing configuration of Kubernetes clusters

## Installation and configuration of the control plane components

### Argo CD installation

First, Argo CD must be installed on the control plane cluster. Right now, argocd-agent will expect the same, stable version of Argo CD on all involved clusters, the control plane and workload clusters.

We recommend Kustomize or Helm to install Argo CD. It is also recommended to install Argo CD in its default namespace, which is `argocd`.

The following Argo CD components must be installed and running on the control plane:

* `argocd-server`
* `argocd-dex` (if you plan to use SSO)
* `argocd-redis` (or potentially, the HA variant)
* `argocd-repository-server`

The following Argo CD components **must not** run on the control plane cluster. If you installed them, either delete the StatefulSet or Deployment resource (recommended) or scale them *permanently* down to 0.

* `argocd-application-controller`
* `argocd-applicationset-controller` (not yet supported)

We provide a Kustomize base to install Argo CD on the principal:

```shell
# Instead of using --context, you can also make sure that your current
# context points to the principal's cluster (i.e. using kubectx)
kubectl --context <principal context> create namespace argocd
kubectl --context <principal context> apply -n argocd -k install/argo-cd/principal
```

### Configuring Argo CD

On the control plane, Argo CD must be configured to use the [apps-in-any-namespace](https://argo-cd.readthedocs.io/en/stable/operator-manual/app-any-namespace/) pattern. The Argo CD API server needs to be able to look for Application resources in all namespaces associated with agents. It is a good idea to find a nice naming pattern for your namespaces, such as prefixing them with `agent-`. This will allow you to use a simple pattern like `agent-*` for granting access to these namespaces.

You can take a look at [how we set up Argo CD on the control plane in our development environment](https://github.com/argoproj-labs/argocd-agent/tree/main/hack/dev-env/control-plane) to get an idea of how this is done.

You can then proceed to configure other aspects of Argo CD to meet your needs, such as SSO, RBAC etc.

!!!note
    Since there is no application controller running on the control plane, there is no point in making configuration that would affect reconciliation.

### Principal prerequisites

The principal server will be installed in the same namespace as Argo CD, which should be `argocd`.

Before you install the principal's manifest, you will need to create three things:

* Create a TLS secret containing the public CA certificate used by argocd-agent components
* Create a TLS secret containing the certificate and private key used by the principal's gRPC service
* Create a TLS secret containing the certificate and private key used by the principal's resource proxy
* Create a secret containing the private RSA key used to sign JWT issued by the principal

For non-production scenarios, you can use the `argocd-agentctl` CLI's PKI tooling. Please be advised that this is not a production-grade setup, and that important pieces of the PKI, such as the CA's private key, will be stored unprotected on your control plane cluster.

#### Create the PKI on the principal:

``` { .bash .copy}
argocd-agentctl --principal-context <control plane context> pki init
```

#### Issue the certificate for the principal's gRPC service:

The gRPC service is the service the agents will connect to. This service usually needs to be exposed to the outside world, or at least from your other clusters where the agents are running on. Typically, this means the service is exposed by a load balancer or a node port.

```
argocd-agentctl pki issue principal \
    --principal-context <control plane context> \
    --ip <ip addresses of principal> \
    --dns <dns names of principal>
```

For the `--ip` and `--dns` values, you want to specify all addresses and DNS names that the principal will be reachable at for the agents, for example `--ip 5.5.5.5 --dns my-principal.example.com`. If there is a load balancer in front of the principal, make sure it will pass-through traffic to the principal - otherwise, client certificates might not be used for authentication.

#### Issue the certificate for the principal's resource proxy:

The principal's resource proxy needs to be reachable by your Argo CD API server (_argocd-server_), which usually runs in the same cluster as the principal. Typically, this service should not be reachable outside of the cluster and is exposed using a Kubernetes Service. 

```
argocd-agentctl pki issue resource-proxy \
    --principal-context <control plane context> \
    --ip <ip addresses of resource proxy> \
    --dns <dns names of principal>
```

### Installing the principal

To install the principal component on the control plane cluster, you can use the provided Kustomize base in the repository:

```
kubectl --context <control plane context> -n argocd apply -k install/kubernetes/principal
```
## Installation and configuration of the workload components


### Argo CD installation

Argo CD must be installed on the workload cluster. Right now, argocd-agent will expect the same, stable version of Argo CD on all involved clusters, the control plane and workload clusters.

We recommend Kustomize or Helm to install Argo CD. It is also recommended to install Argo CD in its default namespace, which is `argocd`.

The following Argo CD components must be installed and running on the control plane:

* `argocd-application-controller`
* `argocd-redis` (or potentially, the HA variant)
* `argocd-repository-server`

The following Argo CD components **must not** run on the workload cluster. If you installed them, either delete the StatefulSet or Deployment resource (recommended) or scale them *permanently* down to 0.

* `argocd-server` 

### Agent Pre-requisite

___Note: soon we will be using helm to install agent___

To install the agent, we will need the following things:

- Argo-cd CRDs installed
- Create namespace to deploy agent resources
- Create a TLS secret containing the issued certificate for agent
- 
#### Create the PKI on the agent:

```bash
argocd-agentctl pki issue agent <agent-name> --agent-context <workload context> --agent-namespace <workload namespace> --upsert
```

#### Apply the installation manifests

```shell 
kubectl apply -n $(workload-namespace) -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=main'
```
This should create all the required agent related resources.

NOTE: update RBAC to refer to the right namespaces, rbac namespaces are set to `default` namespace

#### Configuring the agent

You can configure the agent by editing the `argocd-agent-params` ConfigMap in the agent's installation namespace. For an up-to-date example with comments, have a look at the
[example](https://github.com/argoproj-labs/argocd-agent/blob/main/install/kubernetes/agent/agent-params-cm.yaml)

Update the config-map with details with principal server address URL, creds, agent type and more.

__Note:__
Set `agent.tls.client.insecure` to `insecure` to if needed.

After a change to the `argocd-agent-params` ConfigMap, the agent needs to be restarted to pick up the changes:

```shell
kubectl rollout -n argocd restart deployment argocd-agent-agent
```