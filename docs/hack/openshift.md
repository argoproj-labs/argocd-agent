# Deploying Argocd-Agent on Openshift


This guide can be used to set up argocd-agent on openshift environment. It is a guide to help you get going with deployment of argocd-agent on Openshift. This guide does not intend to provide pure copy & paste instructions. Instead, it will assume a little bit of willingness to hackery on your side, as well as some core knowledge about the underlying technologies (JWT, TLS, etc.).

## Pre-requisite
- Create tls certs, Jwt 
- Access to two Openshift clusters
- Argocd CRD should be present on the cluster



## Deploying argocd-agent 

Create argocd-agent password secret using [gen-creds.sh](./hack/demo-env/gen-creds.sh) script. This script will generate three files, `creds.agent-autonomous`, `creds.agent-managed`, `users.control-plane` in `hack/demo-env/creds` dir, which we will use to create principal and agent secrets.

### Step to deploy on argocd-agent principal component
- login to control-plane openshift cluster
- create namespace for principal component deployment. e.g. `argocd`
- create password secret for argo-agent principal

```shell
oc create secret -n argocd generic argocd-agent-principal-userpass --from-file=passwd=./path/to/users.control-plane
```

- create JWT secret and tls certs, follow [#Generating-the-JWT-signing-key](./docs/hack/quickstart.md#Generating-the-JWT-signing-key)
- create tls certs and secret, follow [easyrsa](docs/hack/easyrsa.md) and [TLS certificates and CA](./docs/hack/quickstart.md#TLS-certificates-and-CA)


#### Apply the installation manifests

Now that the required secrets exist, it's time to apply the installation manifests and install the principal into the cluster:

```shell
kubectl apply -n argocd -k install/kubernetes/principal
```

This should create all required manifests in the cluster and spin up a deployment with a single pod.

To check its status:

```shell
kubectl get -n argocd deployments argocd-agent-principal
kubectl get -n argocd pods
```

#### Configuring the principal

You can configure the principal by editing the `argocd-agent-params` ConfigMap in the principal's installation namespace. For an up-to-date example with comments, have a look at the
[example](https://github.com/jannfis/argocd-agent/blob/main/install/kubernetes/principal/principal-params-cm.yaml)

After a change to the `argocd-agent-params` ConfigMap, the principal needs to be restarted to pick up the changes:

```shell
kubectl rollout -n argocd restart deployment argocd-agent-principal
```

Once the deployment are up and running, expose the service `argocd-agent-principal` using routes, set TLS termination to be set to `passthrough`.



### Step to deploy on argocd-agent Agent component

- create tls-secret that were used for principal
- create password secret for argo-agent principal

```shell
oc create secret -n argocd generic argocd-agent-agent-userpass --from-file=credentials=./path/to/users.agent-managed
```

or

```shell
oc create secret -n argocd generic argocd-agent-agent-userpass --from-file=credentials=./path/to/users.agent-autonomus
````

#### Apply the installation manifests

Now that the required secrets exist, it's time to apply the installation manifests and install the agent into the cluster:

```shell 
kubectl apply -n argocd -k install/kubernetes/principal
```

#### Configuring the agent

You can configure the agent by editing the `argocd-agent-params` ConfigMap in the agent's installation namespace. For an up-to-date example with comments, have a look at the
[example](https://github.com/argoproj-labs/argocd-agent/blob/492fbb482744ffad052515a1ad5ad8e376ef927d/install/kubernetes/agent/agent-params-cm.yaml)

__Note:__
Set `agent.tls.client.insecure` to `insecure` to if needed.

After a change to the `argocd-agent-params` ConfigMap, the agent needs to be restarted to pick up the changes:

```shell
kubectl rollout -n argocd restart deployment argocd-agent-agent
```