# Getting started

This is **not** a guide to set-up argocd-agent for a production environment. It is rather a guide to help you get going with contributing to and hacking on argocd-agent. This guide does not intend to provide pure copy & paste instructions. Instead, it will assume a little bit of willingness to hackery on your side, as well as some core knowledge about the underlying technologies (JWT, TLS, etc)

!!! warning "Important - Deprecation Notice"
    This guide uses the userpass authentication method for simplicity in development environments. **The userpass authentication method is deprecated and not suited for use outside development environments.** For production deployments, use mTLS authentication instead.

Please note that some resource names might be out-of-date (e.g. have changed names, or were removed and replaced by something else). If you notice something, please figure it out and submit a PR to this guide. Thanks!

If you want to use [Open Cluster Management (OCM)](https://open-cluster-management.io/)
to bootstrap the Argo CD Agent environment,
follow the guide provided [here](https://github.com/open-cluster-management-io/ocm/tree/main/solutions/argocd-agent).

## Terminology and concepts

### Terminology used throughout this doc

* **control plane** refers to the cluster with the management (or, controlling) components installed on. This typically will have the Argo CD UI on it, is connected to SSO, etc.
* **hub** is synonymous with control plane
* **workload cluster** refers to any cluster on which applications are reconciled to by Argo CD
* **spoke** is synonymous with workload cluster
* **principal** is the component of `argocd-agent` that runs on the control plane cluster. It provides gRPC services to the outside world. It will connect to the Kubernetes API on the control plane cluster only.
* **agent** is the component of `argocd-agent` that runs on each workload cluster. It connects to the principal on the control plane via gRPC and will send and receive events over that connection. It will also connect to the Kubernetes API on the workload cluster.

### Agent modes

Each agent can operate in one of the following modes:

* **autonomous** - In autonomous mode, configuration is maintained outside of the agents scope on the workload cluster. For example, through local ApplicationSets, App-of-Apps pattern or similar. Applications created on the workload cluster will be visible on the control plane. It is possible to trigger sync or refresh from the control plane, however, any changes made to the Applications on the control plane will not be propagated to the agents.
* **managed** - In managed mode, configuration is maintained on the control plane and transmitted to the workload cluster. Configuration can be performed using the Argo CD UI or CLI on the control plane, but could also come from other sources such as ApplicationSet. In managed mode, the Argo CD on the workload clusters may use the Redis on the control plane for enhanced observability. Also, it is possible to trigger sync and refresh from the control plane. However, any changes made to Applications on the workload clusters will be reverted to the state on the control plane.

### Interactions between the agent and the principal

The principal does not need to know details about topology details of the workload clusters or agents. However, agents need to authenticate to the principal using a pre-assigned, unique name. Agent names must comply to the [RFC 1123 DNS label](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names) naming scheme. You need to create the credentials for the agent on the control plane cluster. You may also want to add an extra layer of authentication by requiring the agents to use TLS client certificates in addition to their credentials.

Each agent is assigned a dedicated, unique namespace on the control plane. This namespace will hold all of the workload cluster's configuration. The namespace must be created manually for now, before the agent is connecting the first time (this may change in the future).

On the workload cluster, the agent must be configured at least with:

* the address or FQDN of the principal's gRPC service,
* the agent's credentials,
* optionally, the agent's TLS client certificate and key,
* the agent's mode of operation (autonomous or managed)

### Current limitations

A loose and incomplete collection of current limitations that you should be aware of.

* ~~The connection from the agent to the principal needs to be end-to-end HTTP/2. If you have a network component that is not able to speak HTTP/2 in either direction, argocd-agent will not work for you at the moment.~~  https://github.com/argoproj-labs/argocd-agent/pull/190 introduced preliminary support for HTTP/1 via websockets.
* On the workload clusters, the apps-in-any-namespace feature cannot be used. This may or may not change in the future.
* Argo CD UI/API features that do not work in either mode, managed or autonomous:
  * Pod logs
  * Resource actions
  * Resource manipulation
* Other Argo CD UI/API features such as resource diffing, live manifests, etc only work within specific topologies.

## Prerequisites

- openssl v3.3.1
- kubectl v1.29.1+
- curl 8.7.1+
- kustomize v5.0.0+

## Installing the principal

To install the principal, we will need the following things:

* An RSA key used to sign JWT tokens and
* A set of TLS certificates and private keys,
* A credentials file for authenticating agents
* Some Argo CD components installed, depending on the desired mode of agents

For the sake of simplicity, we will use the `argocd` namespace to install all required resources into the cluster.

The principal installation will come with a ClusterRole and ClusterRoleBinding, so make sure the account you'll be using for installation has sufficient permissions to do that.

### Generating the JWT signing key

The JWT signing key will be stored in a secret in the principal's namespace. We will first need to create the key:

```shell
openssl genrsa -out /tmp/jwt.key 2048
```

If you want a keysize different than 2048, just specify it.

Then, create the secret named `argocd-agent-principal-jwt` with the RSA key as field `jwt.key`:

```shell
kubectl create -n argocd secret generic --from-file=jwt.key=/tmp/jwt.key argocd-agent-jwt
```

### TLS certificates and CA

The principal itself will need a valid TLS server certificate and key pair in order to provide its service. This may be a self-signed certificate if you turn off TLS verification on the agents but that is insecure and shouldn't be used anywhere, really.

A much better idea is to use certificates issued by an existing CA or to set up your own CA. For development purposes, it is fine (and even encouraged) that such CA is of transient, non-production nature.

Once you have the server's certificate and key (in this example, stored in files named `server.crt` and `server.key` respectively) as well as the certificate of the CA (stored in a file named `ca.crt`), create a new secret with the contents of these files:

```shell
kubectl create -n argocd secret generic --from-file=tls.crt=server.crt --from-file=tls.key=server.key --from-file=ca.crt=ca.crt argocd-agent-tls
```

Keep the `ca.crt` file, as you will need it for the installation of agents, too.

### Apply the installation manifests

Create the principal user password secret; replace `<PASSWORD>` with the password of your choice:

```shell
kubectl create -n argocd secret generic argocd-agent-principal-userpass --from-literal=passwd='<PASSWORD>'  # DEPRECATED: userpass auth not suited for production
```

Now that the required secrets exist, it's time to apply the installation manifests and install the principal into the cluster:

```shell
kubectl apply -n argocd -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/principal?ref=main'
```

This should create all required manifests in the cluster and spin up a deployment with a single pod.

To check its status:

```shell
kubectl get -n argocd deployments argocd-agent-principal
kubectl get -n argocd pods
```

### Configuring the principal

You can configure the principal by editing the `argocd-agent-params` ConfigMap in the principal's installation namespace. For an up-to-date example with comments, have a look at the
[example](https://github.com/argoproj-labs/argocd-agent/blob/main/install/kubernetes/principal/principal-params-cm.yaml)

After a change to the `argocd-agent-params` ConfigMap, the principal needs to be restarted to pick up the changes:

```shell
kubectl rollout -n argocd restart deployment argocd-agent-principal
```

## Installing the agent

To install the agent, we will need the following things:

- A set of TLS certificates and private keys, 
- A credentials file for authenticating agents

we will use the `argocd` namespace to install all required resources into the cluster.

#### Apply the installation manifests

Create the agent user password secret; replace `<CREDENTIALS>` with the credentials of your choice:

```shell
kubectl create -n argocd secret generic argocd-agent-agent-userpass --from-literal=credentials='<CREDENTIALS>'  # DEPRECATED: userpass auth not suited for production
```

Now that the required secrets exist, it's time to apply the installation manifests and install the agent into the cluster:

```shell 
kubectl apply -n argocd -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=main'
```
This should create all the required agent related resources.

#### Configuring the agent

You can configure the agent by editing the `argocd-agent-params` ConfigMap in the agent's installation namespace. For an up-to-date example with comments, have a look at the
[example](https://github.com/argoproj-labs/argocd-agent/blob/main/install/kubernetes/agent/agent-params-cm.yaml)

__Note:__
Set `agent.tls.client.insecure` to `insecure` to if needed.

After a change to the `argocd-agent-params` ConfigMap, the agent needs to be restarted to pick up the changes:

```shell
kubectl rollout -n argocd restart deployment argocd-agent-agent
```
