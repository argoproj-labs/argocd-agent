# Getting Started

## Table of Contents
- [Getting Started](#getting-started)
  - [Table of Contents](#table-of-contents)
  - [Local Environment Architecture](#local-environment-architecture)
  - [Architecture Overview](#architecture-overview)
  - [Prerequisites](#prerequisites)
  - [Control Plane Setup](#control-plane-setup)
  - [PKI Setup](#pki-setup)
  - [Principal Installation](#principal-installation)
  - [Workload Cluster Setup](#workload-cluster-setup)
  - [Agent Creation and Connection](#agent-creation-and-connection)
  - [Verification](#verification)
  - [Cluster Restart Commands](#cluster-restart-commands)

## Local Environment Architecture
Following the [ArgoCD-Agent Official Guide](https://argocd-agent.readthedocs.io/latest/getting-started/kubernetes/), we'll set up a local environment using kind.

Install argocd-agent on local Kubernetes clusters using kind.

Configure Control Plane and Workload Cluster as separate kind clusters to test a multi-cluster environment similar to actual production environments locally.

```
[Local Machine]
 |
 |-- [Kind Cluster: argocd-hub]
 |     |-- [Argo CD Server, Principal]
 |
 |-- [Kind Cluster: argocd-agent1]
       |-- [Argo CD App Controller, Agent]

[Developer's Code Editor/IDE]
  |
  |-- [Argo CD Source Code]
```

## Architecture Overview
```
Control Plane Cluster           Workload Cluster(s)
┌─────────────────────┐        ┌─────────────────────┐
│ ┌─────────────────┐ │        │ ┌─────────────────┐ │
│ │   Argo CD       │ │        │ │   Argo CD       │ │
│ │ ┌─────────────┐ │ │        │ │ ┌─────────────┐ │ │
│ │ │ API Server  │ │ │◄──────┐│ │ │   App       │ │ │
│ │ │ Repository  │ │ │       ││ │ │ Controller  │ │ │
│ │ │ Redis       │ │ │       ││ │ │ Repository  │ │ │
│ │ │ Dex (SSO)   │ │ │       ││ │ │ Redis       │ │ │
│ │ └─────────────┘ │ │       ││ │ └─────────────┘ │
│ └─────────────────┘ │       ││ └─────────────────┘ │
│ ┌─────────────────┐ │       ││ ┌─────────────────┐ │
│ │   Principal     │ │◄──────┘│ │     Agent       │ │
│ │ ┌─────────────┐ │ │        │ │                 │ │
│ │ │ gRPC Server │ │ │        │ │                 │ │
│ │ │ Resource    │ │ │        │ │                 │ │
│ │ │ Proxy       │ │ │        │ │                 │ │
│ │ └─────────────┘ │ │        │ └─────────────────┘ │
│ └─────────────────┘ │        └─────────────────────┘
└─────────────────────┘
```

<br />

## Prerequisites

### Required Tools
- go
- kubectl (v1.20 or higher)
- argocd-agentctl CLI tool
- cloud-provider-kind

### Install kind
```bash
brew install kind
```

### Clone argocd-agent repository and build CLI
```bash
git clone https://github.com/argoproj-labs/argocd-agent.git
cd argocd-agent

# Build argocd-agentctl CLI
make build

# Add to PATH (Optional)
export PATH=$PATH:$(pwd)/dist
```

### Install Cloud Provider Kind
```bash
# (Option1) install brew
brew install cloud-provider-kind

# you can verify that it’s installed by running with this command `which cloud-provider-kind`

# (Option2) install go
go install sigs.k8s.io/cloud-provider-kind@latest

sudo install ~/go/bin/cloud-provider-kind /usr/local/bin
```

### Start Cloud Provider Kind
```bash
# start background
sudo cloud-provider-kind
```

<br />

## Define resource names
This document uses **Managed mode** which is easier to get started with. <br />
For Autonomous mode, change `agent-managed` commands to `agent-autonomous` in the commands below.

### Cluster Name
```bash
# === Define resource names ===
export PRINCIPAL_CLUSTER_NAME="argocd-hub"
export AGENT_CLUSTER_NAME="argocd-agent1"
export AGENT_APP_NAME="agent-a"
export NAMESPACE_NAME="argocd"
export AGENT_MODE="agent-managed"
export CLUSTER_USER_ID=1
export AGENT_USER_ID=2

export PRINCIPAL_POD_CIDR="10.$((244 + $CLUSTER_USER_ID)).0.0/16"
export PRINCIPAL_SVC_CIDR="10.$((96 + $CLUSTER_USER_ID)).0.0/12"
export AGENT_POD_CIDR="10.$((244 + $AGENT_USER_ID)).0.0/16"
export AGENT_SVC_CIDR="10.$((96 + $AGENT_USER_ID)).0.0/12"
# export AGENT2_CIDR="10.246.0.0/16"

# (optional) Check variables
echo "Principal Cluster: $PRINCIPAL_CLUSTER_NAME"
echo "Agent Cluster: $AGENT_CLUSTER_NAME"
echo "Namespace: $NAMESPACE_NAME"
echo "Agent App Name: $AGENT_APP_NAME"
echo "Agent Mode: $AGENT_MODE"
echo "Principal Pod CIDR: $PRINCIPAL_POD_CIDR"
echo "Principal Service CIDR: $PRINCIPAL_SVC_CIDR"
echo "Agent Pod CIDR: $AGENT_POD_CIDR"
echo "Agent Service CIDR: $AGENT_SVC_CIDR"
```

### Agent Name

## Control Plane Setup

### Create cluster
- Add extraPortMappings for NodePort Binding
```bash
cat <<EOF | kind create cluster --name $PRINCIPAL_CLUSTER_NAME --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: $PRINCIPAL_CLUSTER_NAME
networking:
  podSubnet: "$PRINCIPAL_POD_CIDR"
  serviceSubnet: "$PRINCIPAL_SVC_CIDR"
nodes:
  - role: control-plane
EOF

```

### Create namespace
```bash
kubectl create namespace $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME
```

### Install Argo CD for Control Plane
Install Principal-specific Argo CD configuration.

```bash
kubectl apply -n $NAMESPACE_NAME \
  -k install/kubernetes/argo-cd/principal \
  --context kind-$PRINCIPAL_CLUSTER_NAME
```

This configuration includes:
- ✅ argocd-server (API and UI)
- ✅ argocd-dex-server (SSO)
- ✅ argocd-redis (state storage)
- ✅ argocd-repo-server (Git repository access)
- ❌ argocd-application-controller (runs only on workload clusters)

### Configure Apps-in-Any-Namespace
```bash
kubectl patch configmap argocd-cmd-params-cm \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  --patch '{"data":{"application.namespaces":"*"}}'

kubectl rollout restart deployment argocd-server -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME

kubectl get configmap argocd-cmd-params-cm \
  -n "$NAMESPACE_NAME" \
  --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  -o yaml | grep application.namespaces
```

### Expose ArgoCD UI
```bash
kubectl patch svc argocd-server \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  --patch '{"spec":{"type":"NodePort"}}'

kubectl get svc argocd-server \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME
```

<br />

### Initialize Certificate Authority
```bash
./dist/argocd-agentctl pki init \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME
```

## Install Principal

### Deploy Principal components
```bash
kubectl apply -n $NAMESPACE_NAME \
  -k install/kubernetes/principal \
  --context kind-$PRINCIPAL_CLUSTER_NAME
```

### Check Principal configuration
```bash
# Check the principal authentication configuration
kubectl get configmap argocd-agent-params \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  -o jsonpath='{.data.principal\.auth}'
# Should output: mtls:CN=([^,]+)
```

### Update Principal configuration
```bash
kubectl patch configmap argocd-agent-params \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  --patch "{\"data\":{
    \"principal.allowed-namespaces\":\"$AGENT_APP_NAME\"
  }}"

kubectl rollout restart deployment argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME

kubectl get configmap argocd-agent-params \
  -n "$NAMESPACE_NAME" \
  --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  -o yaml | grep principal.allowed-namespaces
```

### Verify Principal service exposure
Ensure the 8443 listener is properly configured on LoadBalancer for Agent access to Principal:
you have to check `cloud-provider-kind` is running
```bash
# Option 1: LoadBalancer
kubectl patch svc argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  --patch '{"spec":{"type":"NodePort"}}'

kubectl get svc argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME

# Expected output:
# NAME                     TYPE           CLUSTER-IP      EXTERNAL-IP       PORT(S)          AGE
# argocd-agent-principal   LoadBalancer   10.96.215.149   172.19.0.2      8443:30673/TCP   5s

```

<br />

## PKI Setup

### Generate Principal certificates
Issue gRPC server certificate (address for Agent connection) <br />
```bash
# Check LoadBalancer IP
PRINCIPAL_EXTERNAL_IP=$(kubectl get svc argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

# Check NodePort
PRINCIPAL_EXTERNAL_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' argocd-hub-control-plane)
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

PRINCIPAL_NODE_PORT=$(kubectl get svc argocd-agent-principal -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME -o jsonpath='{.spec.ports[0].nodePort}'
)
echo "<principal-node-port>: $PRINCIPAL_NODE_PORT"

# Check DNS Name
PRINCIPAL_DNS_NAME=$(kubectl get svc argocd-agent-principal -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME -o jsonpath='{.metadata.name}.{.metadata.namespace}.svc.cluster.local')
echo "<principal-dns-name>: $PRINCIPAL_DNS_NAME"

# Create pki issue
./dist/argocd-agentctl pki issue principal \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME \
  --ip 127.0.0.1,$PRINCIPAL_EXTERNAL_IP \
  --dns localhost,$PRINCIPAL_DNS_NAME \
  --upsert
```

Issue resource proxy certificate (address for Argo CD connection) <br />
```bash
# Check NodePort
RESOURCE_PROXY_INTERNAL_IP=$(kubectl get svc argocd-agent-resource-proxy \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  -o jsonpath='{.spec.clusterIP}')
echo "<resource-proxy-ip>: $RESOURCE_PROXY_INTERNAL_IP"

RESOURCE_PROXY_DNS_NAME=$(kubectl get svc argocd-agent-resource-proxy \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  -o jsonpath='{.metadata.name}.{.metadata.namespace}.svc.cluster.local')
echo "<resource-proxy-dns>: $RESOURCE_PROXY_DNS_NAME"

# Create pki issue
./dist/argocd-agentctl pki issue resource-proxy \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME \
  --ip 127.0.0.1,$RESOURCE_PROXY_INTERNAL_IP \
  --dns localhost,$RESOURCE_PROXY_DNS_NAME \
  --upsert
```

### Generate JWT signing key
```bash
./dist/argocd-agentctl jwt create-key \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME \
  --upsert
```

### Verify Principal installation
```bash
kubectl get pods -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME | grep principal

kubectl logs -n $NAMESPACE_NAME deployment/argocd-agent-principal --context kind-$PRINCIPAL_CLUSTER_NAME

# Expected logs:
# argocd-agent-principal-785cd96ddc-sm44r            1/1     Running   0          7s
# {"level":"info","msg":"Setting loglevel to info","time":"2025-09-13T07:25:38Z"}
# time="2025-09-13T07:25:38Z" level=info msg="Loading gRPC TLS certificate from secret argocd/argocd-agent-principal-tls" - Loading TLS certificate for gRPC communication from Kubernetes Secret
# ...
# time="2025-09-13T07:25:38Z" level=info msg="This server will require TLS client certs as part of authentication" module=server - TLS client certificates required for client authentication
# ...
# time="2025-09-13T07:25:38Z" level=info msg="Starting argocd-agent (server) v0.0.1-alpha (ns=$NAMESPACE_NAME, allowed_namespaces=[])" module=server - Starting argocd-agent server (version, namespace, allowed namespaces list)
# ...
# time="2025-09-13T07:25:38Z" level=info msg="Now listening on [::]:8443" module=server
# time="2025-09-13T07:25:38Z" level=info msg="Application informer synced and ready" module=server
# time="2025-09-13T07:25:38Z" level=info msg="AppProject informer synced and ready" module=server
# time="2025-09-13T07:25:38Z" level=info msg="Repository informer synced and ready" module=server
```
<br />


## Workload Cluster Setup

This document uses **Managed mode** which is easier to get started with. <br />
For Autonomous mode, change `agent-managed` commands to `agent-autonomous` in the commands below.

### Cluster Name
```bash
# === Define resource names ===
export CLUSTER_USER_ID=1
export AGENT_USER_ID=2

export AGENT_POD_CIDR="10.$((244 + $AGENT_USER_ID)).0.0/16"
export AGENT_SVC_CIDR="10.$((96 + $AGENT_USER_ID)).0.0/12"
# export AGENT2_CIDR="10.246.0.0/16"

# (optional) Check variables
echo "Agent Pod CIDR: $AGENT_POD_CIDR"
echo "Agent Service CIDR: $AGENT_SVC_CIDR"
```


### Create cluster
```bash
cat <<EOF | kind create cluster --name $AGENT_CLUSTER_NAME --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: $AGENT_CLUSTER_NAME
networking:
  podSubnet: "$AGENT_POD_CIDR"
  serviceSubnet: "$AGENT_SVC_CIDR"
nodes:
  - role: control-plane
EOF

```

### Create namespace
```bash
kubectl create namespace $NAMESPACE_NAME --context kind-$AGENT_CLUSTER_NAME
```

### Install Argo CD for Workload Cluster

```bash
kubectl apply -n $NAMESPACE_NAME \
  -k install/kubernetes/argo-cd/$AGENT_MODE \
  --context kind-$AGENT_CLUSTER_NAME
```

This configuration includes:
- ✅ argocd-application-controller (application reconciliation)
- ✅ argocd-repo-server (Git access)
- ✅ argocd-redis (local state)
- ❌ argocd-server (runs only on Control Plane)
- ❌ argocd-dex-server (runs only on Control Plane)

## Agent Creation and Connection

### Create Agent configuration
Create Agent configuration on Principal. <br />

```bash
PRINCIPAL_EXTERNAL_IP=$(kubectl get svc argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

# Check NodePort
PRINCIPAL_EXTERNAL_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' argocd-hub-control-plane)
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

./dist/argocd-agentctl agent create $AGENT_APP_NAME \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME \
  --resource-proxy-server ${PRINCIPAL_EXTERNAL_IP}:9090 \
  --resource-proxy-username $AGENT_APP_NAME \
  --resource-proxy-password "$(openssl rand -base64 32)"
```

### Issue Agent client certificate

```bash
# Issue Agent client certificate
./dist/argocd-agentctl pki issue agent $AGENT_APP_NAME \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --agent-context kind-$AGENT_CLUSTER_NAME \
  --agent-namespace $NAMESPACE_NAME \
  --upsert
```

### Propagate Certificate Authority to Agent

```bash
argocd-agentctl pki propagate \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME \
  --agent-context kind-$AGENT_CLUSTER_NAME \
  --agent-namespace $NAMESPACE_NAME
```

### Verify certificate installation
Verify that Agent client certificates are properly installed:

```bash
kubectl get secret argocd-agent-client-tls -n $NAMESPACE_NAME --context kind-$AGENT_CLUSTER_NAME

# Expected result
# NAME                      TYPE                DATA   AGE
# argocd-agent-client-tls   kubernetes.io/tls   2      7s

kubectl get secret argocd-agent-ca -n $NAMESPACE_NAME --context kind-$AGENT_CLUSTER_NAME

# Expected result
# NAME              TYPE     DATA   AGE
# argocd-agent-ca   Opaque   1      15s
```

### Create Agent namespace on Principal (Optional)

**⚠️ Warning**: This step is unnecessary if creating Applications in the `$NAMESPACE_NAME` namespace.

Create only if you want to use Agent-specific namespaces:

```bash
kubectl create namespace $AGENT_APP_NAME --context kind-$PRINCIPAL_CLUSTER_NAME

# Also need to create AppProject in $AGENT_APP_NAME namespace
cat <<EOF | kubectl apply -f - --context kind-$PRINCIPAL_CLUSTER_NAME
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: default
  namespace: $AGENT_APP_NAME
spec:
  clusterResourceWhitelist:
  - group: '*'
    kind: '*'
  destinations:
  - namespace: '*'
    server: '*'
  sourceRepos:
  - '*'
EOF
```

**Recommended**: Use `$NAMESPACE_NAME` namespace for simple testing.

### Deploy Agent
```bash
kubectl apply -n $NAMESPACE_NAME \
  -k install/kubernetes/agent \
  --context kind-$AGENT_CLUSTER_NAME
```

### Configure Agent connection
Configure Agent to connect to Principal using mTLS authentication. 

```bash
# LoadBalancer IP
PRINCIPAL_EXTERNAL_IP=$(kubectl get svc argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

# Check NodePort
PRINCIPAL_EXTERNAL_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' argocd-hub-control-plane)
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

PRINCIPAL_NODE_PORT=$(kubectl get svc argocd-agent-principal -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME -o jsonpath='{.spec.ports[0].nodePort}'
)
echo "<principal-node-port>: $PRINCIPAL_NODE_PORT"

kubectl patch configmap argocd-agent-params \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME \
  --patch "{\"data\":{
    \"agent.server.address\":\"$PRINCIPAL_EXTERNAL_IP\",
    \"agent.server.port\":\"$PRINCIPAL_NODE_PORT\",
    \"agent.mode\":\"managed\",
    \"agent.creds\":\"mtls:any\"
  }}"

kubectl rollout restart deployment argocd-agent-agent \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME
```

<br />

## Verification

### Wait
```bash
sleep 40
```

### Verify Agent connection

- Wait until `redis.go:464: auto mode fallback:` output appears.

```bash
kubectl logs -n $NAMESPACE_NAME deployment/argocd-agent-agent --context kind-$AGENT_CLUSTER_NAME

# Expected output on success:
# time="2025-10-09T09:11:22Z" level=info msg="Authentication successful" module=Connector
# time="2025-10-09T09:11:22Z" level=info msg="Connected to argocd-agent-0.0.1-alpha" module=Connector
# time="2025-10-09T09:11:23Z" level=info msg="Starting to send events to event stream" direction=send module=StreamEvent
```

### Verify Agent recognition on Principal
- Wait until connect `$AGENT_APP_NAME` appears.
```bash
kubectl logs -n $NAMESPACE_NAME deployment/argocd-agent-principal --context kind-$PRINCIPAL_CLUSTER_NAME

# Expected output on success:
# time="2025-10-09T09:11:23Z" level=info msg="An agent connected to the subscription stream" client=$AGENT_APP_NAME method=Subscribe
# time="2025-10-09T09:11:23Z" level=info msg="Updated connection status to 'Successful' in Cluster: '$AGENT_APP_NAME'" component=ClusterManager
```

### List connected Agents
```bash
./dist/argocd-agentctl agent list \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME
```

### Test Application Synchronization (Managed Mode)

Propagate a default AppProject from principal to the agent:

```bash
kubectl patch appproject default -n $AGENT_APP_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME --type='merge' \
  --patch='{"spec":{"sourceNamespaces":["*"],"destinations":[{"name":"*","namespace":"*","server":"*"}]}}'
```

Verify the AppProject is synchronized to the agent:

```bash
# Check that the AppProject appears on the workload cluster
kubectl get appprojs -n $NAMESPACE_NAME --context kind-$AGENT_CLUSTER_NAME
```

```bash
PRINCIPAL_EXTERNAL_IP=$(kubectl get svc argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

# Check NodePort
PRINCIPAL_EXTERNAL_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' argocd-hub-control-plane)
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

PRINCIPAL_NODE_PORT=$(kubectl get svc argocd-agent-principal -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME -o jsonpath='{.spec.ports[0].nodePort}'
)
echo "<principal-node-port>: $PRINCIPAL_NODE_PORT"

cat <<EOF | kubectl apply -f - --context kind-$PRINCIPAL_CLUSTER_NAME
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: test-app-2
  namespace: $NAMESPACE_NAME
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: guestbook
  destination:
    server: https://$PRINCIPAL_EXTERNAL_IP:$PRINCIPAL_NODE_PORT?agentName=$AGENT_APP_NAME
    namespace: $NAMESPACE_NAME
  syncPolicy:
    syncOptions:
    - CreateNamespace=true
EOF
```

Verify the application is synchronized to the agent:

```bash
# Check that the application exists on the principal
kubectl get applications -n $AGENT_APP_NAME --context kind-$PRINCIPAL_CLUSTER_NAME

# Check that the application appears on the workload cluster
kubectl get applications -n $NAMESPACE_NAME --context kind-$AGENT_CLUSTER_NAME
```


Verify that the application is synchronized to the Agent.

```bash
# Delete Application in wrong namespace
kubectl delete application test-app -n $AGENT_APP_NAME --context kind-$PRINCIPAL_CLUSTER_NAME

# Recreate in $NAMESPACE_NAME namespace (use the Application creation code above)
```

### Access ArgoCD UI

```bash
kubectl port-forward svc/argocd-server -n $NAMESPACE_NAME 8080:443 --context kind-$PRINCIPAL_CLUSTER_NAME &

# Check initial admin password
kubectl -n $NAMESPACE_NAME get secret argocd-initial-admin-secret --context kind-$PRINCIPAL_CLUSTER_NAME \
  -o jsonpath="{.data.password}" | base64 -d && echo
```

- URL: https://localhost:8080
- Username: `admin`
- Password: Value confirmed by the command above
- If SSL certificate warning appears in browser, click "Advanced" → "Proceed to unsafe"
- You can verify that the Agent cluster (`agent-a`) is connected in the UI.

![alt text](./images/image-1.png)

![alt text](./images/image.png)

<br />