# Argo CD Agent Hub and Spoke Cluster Setup Documentation Using Operator
This document outlines the process of setting up Argo CD Agent in a hub and spoke cluster architecture. This configuration allows a central Argo CD instance (the hub) to manage applications deployed across multiple remote Kubernetes clusters (the spokes) through the Argo CD-agent.
## Prerequisites
Before proceeding with the setup, ensure you have the following:

- A running OpenShift/Kubernetes cluster designated as the hub, with Argo CD/OpenShift GitOps Operator installed.
- One or more Kubernetes clusters designated as spokes, Argo CD/OpenShift GitOps Operator installed.
- `oc` binary configured to access both hub and spoke clusters.
- Operator must be installed in [cluster scope](https://argocd-operator.readthedocs.io/en/stable/usage/basics/#cluster-scoped-instance) mode.
- [Apps in any Namespace](https://argocd-operator.readthedocs.io/en/latest/usage/apps-in-any-namespace/) must be enabled for Hub Cluster.
- `argocd-agentctl` binary (for non-production scenario)


## Hub Cluster Setup

Hub cluster will be installed in argocd namespace

Before you install the principal's manifest, you will need to
- Create a TLS secret containing the public CA certificate used by Argo CD-agent components
- Create a TLS secret containing the certificate and private key used by the principal's gRPC service
- Create a TLS secret containing the certificate and private key used by the principal's resource proxy
- Create a secret containing the private RSA key used to sign JWT issued by the principal

For non-production scenarios, you can use the argocd-agentctl CLI's PKI tooling. Please be advised that this is not a production-grade setup, and that important pieces of the PKI, such as the CA's private key, will be stored unprotected on your control plane cluster.

Create the PKI on the principal:

```
argocd-agentctl --principal-context <control plane context> pki init
```

Issue the certificate for the principal's gRPC service:
The gRPC service is the service the agents will connect to. This service usually needs to be exposed to the outside world, or at least from your other clusters where the agents are running on. Typically, this means the service is exposed by a load balancer or a node port.

```
argocd-agentctl pki issue principal \
    --principal-context <control plane context> \
    --ip <ip addresses of principal> \
    --dns <dns names of principal>
```

For the --ip and --dns values, you want to specify all addresses and DNS names that the principal will be reachable at for the agents, for example --ip 5.5.5.5 --dns my-principal.example.com. If there is a load balancer in front of the principal, make sure it will pass through traffic to the principal - otherwise, client certificates might not be used for authentication.

Issue the certificate for the principal's resource proxy:
The principal's resource proxy needs to be reachable by your Argo CD API server (argocd-server), which usually runs in the same cluster as the principal. Typically, this service should not be reachable outside the cluster and is exposed using a Kubernetes Service.

```
argocd-agentctl pki issue resource-proxy \
    --principal-context <control plane context> \
    --ip <ip addresses of resource proxy> \
    --dns <dns names of principal>
```

Deploy principal on hub cluster using argocd-operator/gitops-operator using Argo CD CR given below

```
apiVersion: argoproj.io/v1beta1
kind: ArgoCD
metadata:
  name: argocd
spec:
  controller:
    enabled: false
  argoCDAgent:
    principal:
      enabled: true
      allowedNamespaces: 
        - "*"
      jwtAllowGenerate: true
      auth: "mtls:CN=([^,]+)"
      logLevel: "trace"
      image: "ghcr.io/argoproj-labs/argocd-agent/argocd-agent:latest"
  sourceNamespaces:
    - "agent-managed"
    - "agent-autonomous"  					
```

The above CR should create all the necessary resource for Argo CD as well as argocd-agent principal in argocd namespace.

Create argocd-redis secret, because principal looks for it to fetch redis authentication details.

```
oc create secret generic argocd-redis -n argocd --from-literal=auth="$(oc get secret argocd-redis-initial-password -n argocd -o jsonpath='{.data.admin\.password}' | base64 -d)"
```

## Setting up agent workload cluster 

### Configure Argo CD for Agent 
 
Argo CD instance on Agent cluster

Creating Argo CD instance for Workload/spoke cluster.
```
  apiVersion: argoproj.io/v1beta1
  kind: ArgoCD
  metadata:
    name: argocd
  spec:
    server:
      enabled: false
```

Create redis secret using below command for agent deployment 
```
kubectl create secret generic argocd-redis -n <workload namespace> --from-literal=auth="$(kubectl get secret argocd-redis-initial-password -n <argocd-namespace> -o jsonpath='{.data.admin\.password}' | base64 -d)"
```

### Configure Agent in managed mode

Before installing agent resources create 
- a TLS secret containing the issued certificate for agent

Create the PKI on the agent:
Run this command while connected to principal
```
argocd-agentctl pki issue agent <agent-name>  --principal-context <principal context> --agent-context <workload context> --agent-namespace <workload namespace> --upsert
```

Apply the installation manifests for Argo CD-agent agent
```
oc apply -n $(workload-namespace) -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=main'
```
This should create all the required agent related resources.

Note: If installation is done on other than `default` namespace, run the following command to update cluster-role with correct namespace

```
kubectl patch clusterrolebinding argocd-agent-agent --type='json' -p='[{"op": "replace", "path": "/subjects/0/namespace", "value": "<workload-namespace>"}]'
```


Update the configMap with name `argocd-agent-params`  with parameters related to agent.mode,agent.creds, agent.namespace, agent.server.address.	
```
  agent.keep-alive-ping-interval: 50s
  agent.mode: managed
  agent.creds: mtls:any
  agent.tls.client.insecure: "false"
  agent.tls.root-ca-path: ""
  agent.tls.client.cert-path: ""
  agent.tls.client.key-path: ""
  agent.log.level: info
  agent.namespace: <workload-namespace>
  agent.server.address: <argocd-principal-server>
  agent.server.port: 443
  agent.metrics.port: 8181
  agent.healthz.port: "8002"
  agent.tls.root-ca-secret-name: argocd-agent-ca
  agent.tls.secret-name: argocd-agent-client-tls
```
Also Update RBAC, rolebinding/clusterrolebinding with `workload-namespace`, if pod is facing rbac issues. 



### Configure Agent in Autonomous mode

Before installing agent resources create 
Create a TLS secret containing the issued certificate for agent

Create the PKI on the agent:
Run this command while connected to principal
```
argocd-agentctl pki issue agent <agent-name> --principal-context <principal context> --agent-context <workload context> --agent-namespace argocd --upsert
```

Apply the installation manifests for argocd agent
```
oc apply -n argocd -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=main'
```
This should create all the required agent related resources.

Note: If installation is done on other than `default` namespace, run the following command to update cluster-role with correct namespace

```
kubectl patch clusterrolebinding argocd-agent-agent --type='json' -p='[{"op": "replace", "path": "/subjects/0/namespace", "value": "<workload-namespace>"}]'
```


Update the configMap with name `argocd-agent-params`  with parameters related to agent.mode,agent.creds, agent.namespace, agent.server.address.
```
data:
  agent.keep-alive-ping-interval: 50s
  agent.tls.client.insecure: 'false' 
  agent.server.port: '443'
  agent.tls.root-ca-path: ''
  agent.tls.client.cert-path: ''
  agent.tls.root-ca-secret-name: argocd-agent-ca
  agent.tls.secret-name: argocd-agent-client-tls
  agent.mode: autonomous
  agent.log.level: info
  agent.namespace: argocd
  agent.server.address: <principal server address>
  agent.metrics.port: '8181'
  agent.tls.client.key-path: ''
  agent.healthz.port: '8002'
  agent.creds: 'mtls:any'
```


#### Troubleshooting 
___

1. If pod fails to come up with error
```
time="2025-07-30T14:58:33Z" level=warning msg="INSECURE: Not verifying remote TLS certificate"
time="2025-07-30T14:58:33Z" level=info msg="Loading client TLS certificate from secret argocd/argocd-agent-client-tls"
[FATAL]: Error creating remote: unable to read TLS client from secret: could not read TLS secret argocd/argocd-agent-client-tls: secrets "argocd-agent-client-tls" is forbidden: User "system:serviceaccount:argocd:argocd-agent-agent" cannot get resource "secrets" in API group "" in the namespace "argocd"
```

update the ClusterRoleBinding to update the subject namespace to workload-namespace.

```
kubectl patch clusterrolebinding argocd-agent-agent --type='json' -p='[{"op": "replace", "path": "/subjects/0/namespace", "value": "<workload-namespace>"}]'
```

2. If facing error with appProject

```
Unable to create application: app is not allowed in project "default", or the project does not exist
```
refer to doc for [AppProject Synchronization](https://argocd-agent.readthedocs.io/latest/user-guide/appprojects/#managed-agent-mode).