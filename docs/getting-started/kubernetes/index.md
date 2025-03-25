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

### Configuring Argo CD

On the control plane, Argo CD must be configured to use the [apps-in-any-namespace](https://argo-cd.readthedocs.io/en/stable/operator-manual/app-any-namespace/) pattern. The Argo CD API server needs to be able to look for Application resources in all namespaces associated with agents. It is a good idea to find a nice naming pattern for your namespaces, such as prefixing them with `agent-`. This will allow you to use a simple pattern like `agent-*` for granting access to these namespaces.

You can take a look at [how we set up Argo CD on the control plane in our development environment](https://github.com/argoproj-labs/argocd-agent/tree/main/hack/dev-env/control-plane) to get an idea of how this is done.

### Principal prerequisites

The principal server will be installed in the same namespace as Argo CD, which should be `argocd`.

Before you install the principal's manifest, you will need to create three things:

* Create a TLS secret containing the public CA certificate used by argocd-agent components
* Create a TLS secret containing the certificate and private key used by the principal's gRPC service
* Create a secret containing the private RSA key used to sign JWT issued by the principal