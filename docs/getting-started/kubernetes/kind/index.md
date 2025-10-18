# Getting Started with Kind

This quick start guide walks you through setting up argocd-agent using Kind (Kubernetes in Docker).
It provides a simple local environment to connect a principal (control plane) and agent (workload) cluster with mutual TLS (mTLS) authentication.

!!! info "Component Placement Overview"
    Before proceeding, make sure you understand [which Argo CD components run where](../../index.md#argo-cd-component-placement). This guide follows those placement requirements strictly.

## Prerequisites

Before starting, ensure you have:

### Tools
- `kubectl` (v1.20 or later)
- `argocd-agentctl` CLI tool ([download from releases](https://github.com/argoproj-labs/argocd-agent/releases))
- `kustomize` (optional, for customization)
- `kind` ([Kind Installation guide](https://kind.sigs.k8s.io/docs/user/quick-start/#installation))

### Agent Planning

- **Unique agent names** following [DNS label standards](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names)
- **Networking plan** for exposing the principal service to agents
- **Decision on agent mode**: autonomous or managed for each agent

## Architecture Overview
```
    kind-argocd-hub               kind-argocd-agent1...
  (Control Plane Cluster)       (Workload Cluster(s))

Pod CIDR: 10.245.0.0/16        Pod CIDR: 10.246.0.0/16...
SVC CIDR: 10.97.0.0/12         SVC CIDR: 10.98.0.0/12...
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

## Define resource names
This document uses **Managed mode** which is easier to get started with. <br />
For Autonomous mode, change the value `managed` to `autonomous` for the `AGENT_MODE` env variable.

### Set Environment Values
```bash
# === Define resource names ===
export PRINCIPAL_CLUSTER_NAME="argocd-hub"
export AGENT_CLUSTER_NAME="argocd-agent1"
export AGENT_APP_NAME="agent-a"
export NAMESPACE_NAME="argocd"
export AGENT_MODE="managed" # or autonomous
export CLUSTER_USER_ID=1
export AGENT_USER_ID=2

export PRINCIPAL_POD_CIDR="10.$((244 + $CLUSTER_USER_ID)).0.0/16"
export PRINCIPAL_SVC_CIDR="10.$((96 + $CLUSTER_USER_ID)).0.0/12"
export AGENT_POD_CIDR="10.$((244 + $AGENT_USER_ID)).0.0/16"
export AGENT_SVC_CIDR="10.$((96 + $AGENT_USER_ID)).0.0/12"

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

## Control Plane Setup

### Create cluster
Add extraPortMappings for NodePort Binding
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

- ✅ **argocd-server** (API and UI)
- ✅ **argocd-dex-server** (SSO, if needed)
- ✅ **argocd-redis** (state storage)
- ✅ **argocd-repo-server** (Git repository access)
- ❌ **argocd-application-controller** (runs on workload clusters only)
- ❌ **argocd-applicationset-controller** (not yet supported)

!!! warning "Critical Component Placement"
    The **argocd-application-controller** must **never** be deployed on the control plane cluster. It can only run on workload clusters where it has direct access to manage Kubernetes resources. Running it on the control plane is not supported and is out of scope for this project.

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
```bash
# Option 1: NodePort
kubectl patch svc argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  --patch '{"spec":{"type":"NodePort"}}'

kubectl get svc argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME

# Expected output:
# NAME                     TYPE       CLUSTER-IP      EXTERNAL-IP   PORT(S)         AGE
# argocd-agent-principal   NodePort   10.106.231.64   <none>        443:32560/TCP   26s

```

<br />

## PKI Setup

### Generate Principal certificates
Issue gRPC server certificate (address for Agent connection) <br />
```bash
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
  -k install/kubernetes/argo-cd/agent-$AGENT_MODE \
  --context kind-$AGENT_CLUSTER_NAME
```

This configuration includes:

- ✅ **argocd-application-controller** (reconciles applications - **required on workload clusters**)
- ✅ **argocd-repo-server** (Git access for the application controller)
- ✅ **argocd-redis** (local state for the application controller)
- ❌ **argocd-server** (runs on control plane only)
- ❌ **argocd-dex-server** (runs on control plane only)
- ❌ **argocd-applicationset-controller** (managed agents don't create their own ApplicationSets)

!!! info "Why Application Controller Runs Here"
    The **argocd-application-controller** runs on workload clusters because it needs direct access to the Kubernetes API to create, update, and delete resources. The argocd-agent facilitates communication between the control plane and these controllers, enabling centralized management while maintaining local execution.

### Create Agent configuration
Create Agent configuration on Principal. <br />

```bash
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

### Create Agent namespace on Principal
```bash
kubectl create namespace $AGENT_APP_NAME --context kind-$PRINCIPAL_CLUSTER_NAME
```

### Deploy Agent
```bash
kubectl apply -n $NAMESPACE_NAME \
  -k install/kubernetes/agent \
  --context kind-$AGENT_CLUSTER_NAME
```

### Configure Agent connection
Configure Agent to connect to Principal using mTLS authentication. 

```bash
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
    \"agent.mode\":\"$AGENT_MODE\",
    \"agent.creds\":\"mtls:any\"
  }}"

kubectl rollout restart deployment argocd-agent-agent \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME
```

<br />

## Verification

### Verify Agent connection
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
kubectl patch appproject default -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME --type='merge' \
  --patch='{"spec":{"sourceNamespaces":["*"],"destinations":[{"name":"*","namespace":"*","server":"*"}]}}'
```

Verify the AppProject is synchronized to the agent:

```bash
# Check that the AppProject appears on the workload cluster
kubectl get appprojs -n $NAMESPACE_NAME --context kind-$AGENT_CLUSTER_NAME
```

```bash
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
  namespace: $AGENT_APP_NAME
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: guestbook
  destination:
    server: https://$PRINCIPAL_EXTERNAL_IP:$PRINCIPAL_NODE_PORT?agentName=$AGENT_APP_NAME
    namespace: guestbook
  syncPolicy:
    syncOptions:
    - CreateNamespace=true
EOF
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

<br />

## Adding Another Agent Cluster
To add another agent, simply **increment** the `AGENT_USER_ID` by `+1` and proceed to the **Workload Cluster Setup** section below.  
Each new agent automatically receives a **unique CIDR range** derived from its `AGENT_USER_ID` value.

```bash
export AGENT_USER_ID=$((AGENT_USER_ID + 1))
export AGENT_POD_CIDR="10.$((244 + $AGENT_USER_ID)).0.0/16"
export AGENT_SVC_CIDR="10.$((96 + $AGENT_USER_ID)).0.0/12"

# (optional) Check variables
echo "Agent Pod CIDR: $AGENT_POD_CIDR"
echo "Agent Service CIDR: $AGENT_SVC_CIDR"
```

!!! info "Next step"
    Once the new agent variables are set, continue to the [Workload Cluster Setup](#workload-cluster-setup) section to create and configure the new agent cluster.


## Troubleshooting

### Common Issues

**Access Fobidden**
```bash
# The application status contains:
error="applications.argoproj.io \"<agent-app-name>\" is forbidden: 
User \"system:serviceaccount:argocd:argocd-agent-agent\" cannot get resource 
\"applications\" in API group \"argoproj.io\" in the namespace \"<agent-app-name>\""
```

```bash
# Create ClusterRoleBinding to agent sa
kubectl patch clusterrole argocd-agent-agent \
  --context kind-$AGENT_CLUSTER_NAME \
  --type='json' \
  -p='[{"op":"add","path":"/rules/-","value":{"apiGroups":["argoproj.io"],"resources":["applications","appprojects"],"verbs":["get","list","watch","create","update","patch","delete"]}}]'
```

Verify the application is synchronized to the agent:

```bash
# Check that the application exists on the principal
kubectl get applications -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME

# Check that the application appears on the workload cluster
kubectl get applications -n $NAMESPACE_NAME --context kind-$AGENT_CLUSTER_NAME

# NAME         SYNC STATUS   HEALTH STATUS
# test-app-2   Synced        Progressing
```


**Missing Server Secretkey**:
```bash
# The application status contains:
status:
  conditions:
  - lastTransitionTime: "2025-09-22T16:20:56Z"
    message: 'error getting cluster by name "in-cluster": server.secretkey is missing'
    type: InvalidSpecError
```

```bash
# Create a random server.secretkey
kubectl patch secret argocd-secret -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME \
  --patch='{"data":{"server.secretkey":"'$(openssl rand -base64 32 | base64 -w 0)'"}}'
```

**Auth Failure**:
```bash
# The application status contains:
"Auth failure: rpc error: code = Unavailable desc = connection error: desc = \"transport: Error while dialing: dial tcp $PRINCIPAL_EXTERNAL_IP:$PRINCIPAL_NODE_PORT: connect: connection timed out\" (retrying in 1.728s)"
```

```bash
# Restart agent deployment
kubectl patch configmap argocd-agent-params \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME \
  --patch "{\"data\":{
    \"agent.server.address\":\"$PRINCIPAL_EXTERNAL_IP\",
    \"agent.server.port\":\"$PRINCIPAL_NODE_PORT\",
    \"agent.mode\":\"$AGENT_MODE\",
    \"agent.creds\":\"mtls:any\"
  }}"

kubectl rollout restart deployment argocd-agent-agent \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME
```