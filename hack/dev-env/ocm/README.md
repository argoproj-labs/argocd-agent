## Running the agent components in clusters using the Open Cluster Management (OCM) setup

[Open Cluster Management (OCM)](https://open-cluster-management.io/) is a robust, modular,
and extensible platform for orchestrating multiple Kubernetes clusters.
It features an addon framework that allows other projects to develop extensions for managing clusters in custom scenarios.

The following instructions will setup the `vcluster-control-plane` as the OCM hub cluster,
with the `vcluster-agent-managed` and `vcluster-agent-autonomous` clusters joining as managed clusters.

The Argo CD agent principal component will be installed on the hub cluster directly,
while the Argo CD agent agents will be deployed to the managed clusters as OCM addons.

## Set up OCM

### Prerequisites

- vcluster v0.21.0
- kubectl v1.29.1+
- curl 8.7.1+
- kustomize v5.0.0+
- clusteradm v0.15.1+


### Install clusteradm CLI tool

Run the following command to download and install the latest OCM `clusteradm` tool:

```shell
curl -L https://raw.githubusercontent.com/open-cluster-management-io/clusteradm/main/install.sh | bash
```
### Setup the hub cluster

Setup the `vcluster-control-plane` as the OCM hub cluster:

```shell
kubectl config use-context vcluster-control-plane
joincmd=$(clusteradm init --wait | grep clusteradm)
```

### Request to join as managed clusters

Request `vcluster-agent-managed` and `vcluster-agent-autonomous` to join the hub as managed clusters:

```shell
kubectl config use-context vcluster-agent-managed
$(echo ${joincmd} --wait | sed "s/<cluster_name>/agent-managed/g")

kubectl config use-context vcluster-agent-autonomous
$(echo ${joincmd} --wait | sed "s/<cluster_name>/agent-autonomous/g")
```

### Accept the managed clusters join requests

Accept the join requests on the hub cluster:

```shell
kubectl config use-context vcluster-control-plane
clusteradm accept --clusters agent-managed
clusteradm accept --clusters agent-autonomous
```

### Verify

Verify that the managed clusters have successfully joined the hub cluster:

```shell
kubectl get managedclusters
NAME               HUB ACCEPTED   MANAGED CLUSTER URLS   JOINED   AVAILABLE   AGE
agent-autonomous   true                                  True     True        2m57s
agent-managed      true                                  True     True        2m57s
```

## Deploy the Argo CD agent components

### Deploy and verify the Argo CD agent principal component

Deploy the principal component:

```shell
kubectl config use-context vcluster-control-plane
kubectl create -n argocd secret generic argocd-agent-principal-userpass --from-literal=passwd="$(cat hack/demo-env/creds/users.control-plane)"
kubectl apply -n argocd -k hack/demo-env/ocm/principal
```

Verify the principal deployment:

```shell
kubectl -n argocd get deploy argocd-agent-principal
NAME                     READY   UP-TO-DATE   AVAILABLE   AGE
argocd-agent-principal   1/1     1            1           46s
```

### Deploy and verify the Argo CD agent agent component

Deploy the agent component:

```shell
kubectl config use-context vcluster-agent-managed
kubectl create -n agent-managed  secret generic argocd-agent-managed-userpass --from-literal=credentials="$(cat hack/demo-env/creds/creds.agent-managed)"
kubectl config use-context vcluster-control-plane
kubectl apply -k 'https://github.com/argoproj-labs/argocd-agent/hack/demo-env/ocm/agent-managed?ref=main'

kubectl config use-context vcluster-agent-autonomous
kubectl create ns agent-autonomous
kubectl create -n agent-autonomous  secret generic argocd-agent-auto-userpass --from-literal=credentials="$(cat hack/demo-env/creds/creds.agent-autonomous)"
kubectl config use-context vcluster-control-plane
kubectl apply -k 'https://github.com/argoproj-labs/argocd-agent/hack/demo-env/ocm/agent-autonomous?ref=main'
```

Verify the agents deployment:

```shell
kubectl config use-context vcluster-control-plane
kubectl -n agent-managed get managedclusteraddon
NAME                   AVAILABLE   DEGRADED   PROGRESSING
argocd-agent-managed   True                   False

kubectl -n agent-autonomous get managedclusteraddon
NAME                AVAILABLE   DEGRADED   PROGRESSING
argocd-agent-auto   True                   False
```
