# ArgoCD Agent Hub and Spoke Cluster Setup Documentation Using Operator
This document outlines the process of setting up ArgoCD Agent in a hub and spoke cluster architecture. This configuration allows a central ArgoCD instance (the hub) to manage applications deployed across multiple remote Kubernetes clusters (the spokes) through the argocd-agent.
## Prerequisites
Before proceeding with the setup, ensure you have the following:

A running openshift/kubernetes cluster designated as the hub, with ArgoCD/openshift GitOps Operator installed.
One or more Kubernetes clusters designated as spokes, ArgoCD/openshift GitOps Operator installed.
oc configured to access both hub and spoke clusters.
Operator must be installed in cluster scope mode.
Apps in any Namespace should be enabled.
Argocd-agentctl binary (for non-production scenario)



## Hub Cluster Setup

Hub cluster will be installed in argocd namespace

Before you install the principal's manifest, you will need to create three
Create a TLS secret containing the public CA certificate used by argocd-agent components
Create a TLS secret containing the certificate and private key used by the principal's gRPC service
Create a TLS secret containing the certificate and private key used by the principal's resource proxy
Create a secret containing the private RSA key used to sign JWT issued by the principal

For non-production scenarios, you can use the argocd-agentctl CLI's PKI tooling. Please be advised that this is not a production-grade setup, and that important pieces of the PKI, such as the CA's private key, will be stored unprotected on your control plane cluster.

Create the PKI on the principal:

```argocd-agentctl --principal-context <control plane context> pki init```

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

Create argocd-redis secret, because principal looks for it to fetch redis authentication details.

```
oc create secret generic argocd-redis -n argocd --from-literal=auth="$(oc get secret    argocd-redis-initial-password -n argocd -o jsonpath='{.data.admin\.password}' | base64 -d)"
```

Deploy agent on hub cluster using argocd-operator/gitops-operator using ArgoCD CR given below


```
apiVersion: argoproj.io/v1beta1
kind: ArgoCD
metadata:
  name: argocd
  Namespace: argocd
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
      image: "quay.io/user/argocd-agent:v1"
  sourceNamespaces:
    - "agent-managed"
    - "agent-autonomous"  					
```

The above CR should create all the necessary resource for argocd as well as argocd-agent principal in argocd namespace.


## Setting up agent workload cluster 

### Configure Argo-CD for Agent 
 
argocd instance on Agent cluster

Creating argocd instance for Workload/spoke cluster, this instance will be using the shared repo, redis instance from principal and, have locally running argocd-application-controller instance.
Update the below manifest with loadbalancer ip to create argocd instance. 
```
  apiVersion: argoproj.io/v1beta1
  kind: ArgoCD
  metadata:
    name: argocd
    namespace: argocd
  spec:
    repo:
      remote: <repo LoadBalancer IP addr>:8081
    redis:
      remote: <redisLoadBalancer IP addr>:6379
    server:
      enabled: false
    sourceNamespaces:
    - <workload namespace>
```

### Configure Agent in managed mode

Before installing agent resources create 
Create a TLS secret containing the issued certificate for agent

Create the PKI on the agent:
Run this command while connected to principal
```
argocd-agentctl pki issue agent <agent-name> --agent-context <workload context> --agent-namespace <workload namespace> --upsert
```

Apply the installation manifests for argocd agent
```
oc apply -n $(workload-namespace) -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=main'
```
This should create all the required agent related resources.

Update the configMap with name `argocd-agent-params`  with parameters related to agent.mode,agent.creds, agent.namespace, agent.server.address.	
```
  agent.mode: autonomous
  agent.creds: mtls:any
  agent.tls.client.insecure: "false"
  agent.tls.root-ca-path: "/app/config/tls/ca.crt"
  agent.tls.client.cert-path: "/app/config/tls/tls.crt"
  agent.tls.client.key-path: "/app/config/tls/tls.key"
  agent.log.level: info
  agent.namespace: <workload-namespace>
  agent.server.address: <argocd-principal-server>
  agent.server.port: 443
  agent.metrics.port: 8181
```
Also Update RBAC, rolebinding/clusterrolebinding with `workload-namespace`, if pod is facing rbac issues. 



### Configure Agent in Autonomous mode

Before installing agent resources create 
Create a TLS secret containing the issued certificate for agent

Create the PKI on the agent:
Run this command while connected to principal
```
argocd-agentctl pki issue agent <agent-name> --agent-context <workload context> --agent-namespace argocd --upsert
```

Apply the installation manifests for argocd agent
```
oc apply -n argocd -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=main'
```
This should create all the required agent related resources.

Update the configMap with name `argocd-agent-params`  with parameters related to agent.mode,agent.creds, agent.namespace, agent.server.address.
```
  agent.mode: autonomous
  agent.creds: mtls:any
  agent.tls.client.insecure: "false"
  agent.tls.root-ca-path: "/app/config/tls/ca.crt"
  agent.tls.client.cert-path: "/app/config/tls/tls.crt"
  agent.tls.client.key-path: "/app/config/tls/tls.key"
  agent.log.level: info
  agent.namespace: argocd
  agent.server.address: <argocd-principal-server>
  agent.server.port: 443
  agent.metrics.port: 8181
```
Also Update RBAC, rolebinding/clusterrolebinding with argocd, if pod is facing rbac issues. 

