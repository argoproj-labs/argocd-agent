# Getting Started with SPIFFE Authentication via Istio on Kind

This quick start guide walks you through setting up argocd-agent using [SPIFFE](https://spiffe.io/)-based authentication via a service mesh.
Instead of mutual TLS (mTLS) client certificates, the service mesh handles identity and encryption automatically using SPIFFE identities.
This guide uses [Istio](https://istio.io/) as the service mesh and Kind (Kubernetes in Docker) for a local environment.

!!! info "Component Placement Overview"
    Before proceeding, make sure you understand [which Argo CD components run where](../../index.md#argo-cd-component-placement). This guide follows those placement requirements strictly.

## Prerequisites

Before starting, ensure you have:

### Tools
- `kubectl` (v1.20 or later)
- `argocd-agentctl` CLI tool ([download from releases](https://github.com/argoproj-labs/argocd-agent/releases))
- `kind` ([Kind Installation guide](https://kind.sigs.k8s.io/docs/user/quick-start/#installation))
- `kustomize` (optional, for customization)
- `istioctl` ([Istio Installation guide](https://istio.io/latest/docs/setup/getting-started/#download))

### Installing Istio

If you don't have Istio installed, download the distribution. This provides both `istioctl` and the certificate generation tools used later:

```bash
curl -L https://istio.io/downloadIstio | sh -
export ISTIO_DIR=$PWD/$(ls -d istio-* | head -1)
export PATH="$PATH:$ISTIO_DIR/bin"
```

### Agent Planning

- **Unique agent names** following [DNS label standards](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names)
- **Networking plan** for exposing the principal service to agents
- **Decision on agent mode**: autonomous or managed for each agent

## Architecture Overview
```
    kind-argocd-hub               kind-argocd-agent1...
  (Control Plane Cluster)       (Workload Cluster(s))

Pod CIDR: 10.245.0.0/16        Pod CIDR: 10.246.0.0/16...
SVC CIDR: 10.97.0.0/16         SVC CIDR: 10.98.0.0/16...
┌─────────────────────────┐    ┌─────────────────────────┐
│  Istio (shared root CA) │    │  Istio (shared root CA) │
│ ┌─────────────────────┐ │    │ ┌─────────────────────┐ │
│ │   Argo CD           │ │    │ │   Argo CD           │ │
│ │ ┌─────────────────┐ │ │    │ │ ┌─────────────────┐ │ │
│ │ │ API Server      │ │ │◄──┐│ │ │   App           │ │ │
│ │ │ Repository      │ │ │   ││ │ │ Controller      │ │ │
│ │ │ Redis           │ │ │   ││ │ │ Repository      │ │ │
│ │ │ Dex (SSO)       │ │ │   ││ │ │ Redis           │ │ │
│ │ └─────────────────┘ │ │   ││ │ └─────────────────┘ │ │
│ └─────────────────────┘ │   ││ └─────────────────────┘ │
│ ┌─────────────────────┐ │   ││ ┌─────────────────────┐ │
│ │   Principal         │ │◄──┘│ │     Agent           │ │
│ │ ┌─────────────────┐ │ │    │ │ (SPIFFE identity:   │ │
│ │ │ gRPC (plaintext)│ │ │    │ │  spiffe://cluster   │ │
│ │ │ mesh encrypts   │ │ │    │ │  .local/ns/argocd/  │ │
│ │ └─────────────────┘ │ │    │ │  sa/$AGENT_APP_NAME)│ │
│ └─────────────────────┘ │    │ └─────────────────────┘ │
└─────────────────────────┘    └─────────────────────────┘
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
export PRINCIPAL_SVC_CIDR="10.$((96 + $CLUSTER_USER_ID)).0.0/16"
export AGENT_POD_CIDR="10.$((244 + $AGENT_USER_ID)).0.0/16"
export AGENT_SVC_CIDR="10.$((96 + $AGENT_USER_ID)).0.0/16"

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

### Set Release Version Environment Value
You can check available release branches directly from the GitHub repository: [branches](https://github.com/argoproj-labs/argocd-agent/branches)
```bash
export RELEASE_BRANCH="main"

# (optional) Check variables
echo "Release Branch: $RELEASE_BRANCH"
```

## Generate Shared Root CA

For SPIFFE identities to be trusted across clusters, both Istio installations must share the same root Certificate Authority. This must happen **before** installing Istio.

```bash
mkdir -p /tmp/istio-certs && cd /tmp/istio-certs
make -f $ISTIO_DIR/tools/certs/Makefile.selfsigned.mk root-ca
make -f $ISTIO_DIR/tools/certs/Makefile.selfsigned.mk cluster1-cacerts
make -f $ISTIO_DIR/tools/certs/Makefile.selfsigned.mk cluster2-cacerts
```

## Control Plane Setup

### Create cluster
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

### Install Istio with shared CA on Control Plane Cluster

Install the shared CA certificate **before** installing Istio:

```bash
kubectl create namespace istio-system --context kind-$PRINCIPAL_CLUSTER_NAME

kubectl create secret generic cacerts -n istio-system --context kind-$PRINCIPAL_CLUSTER_NAME \
  --from-file=ca-cert.pem=/tmp/istio-certs/cluster1/ca-cert.pem \
  --from-file=ca-key.pem=/tmp/istio-certs/cluster1/ca-key.pem \
  --from-file=root-cert.pem=/tmp/istio-certs/cluster1/root-cert.pem \
  --from-file=cert-chain.pem=/tmp/istio-certs/cluster1/cert-chain.pem

istioctl install --context kind-$PRINCIPAL_CLUSTER_NAME --set profile=demo -y
```

### Create namespace
```bash
kubectl create namespace $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME
kubectl label namespace $NAMESPACE_NAME istio-injection=enabled --context kind-$PRINCIPAL_CLUSTER_NAME
```

### Install Argo CD for Control Plane
Install Principal-specific Argo CD configuration.

```bash
kubectl apply -n $NAMESPACE_NAME \
  -k "https://github.com/argoproj-labs/argocd-agent/install/kubernetes/argo-cd/principal?ref=$RELEASE_BRANCH" \
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
argocd-agentctl pki init \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME
```

## Install Principal

### Deploy Principal components
```bash
kubectl apply -n $NAMESPACE_NAME \
  -k "https://github.com/argoproj-labs/argocd-agent/install/kubernetes/principal?ref=$RELEASE_BRANCH" \
  --context kind-$PRINCIPAL_CLUSTER_NAME
```

### Generate Principal certificates
Issue gRPC server certificate (address for Agent connection) <br />
```bash
# Check NodePort
PRINCIPAL_EXTERNAL_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' argocd-hub-control-plane)
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

# Check DNS Name
PRINCIPAL_DNS_NAME=$(kubectl get svc argocd-agent-principal -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME -o jsonpath='{.metadata.name}.{.metadata.namespace}.svc.cluster.local')
echo "<principal-dns-name>: $PRINCIPAL_DNS_NAME"

# Create pki issue
argocd-agentctl pki issue principal \
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
argocd-agentctl pki issue resource-proxy \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME \
  --ip 127.0.0.1,$RESOURCE_PROXY_INTERNAL_IP \
  --dns localhost,$RESOURCE_PROXY_DNS_NAME \
  --upsert
```

### Generate JWT signing key
```bash
argocd-agentctl jwt create-key \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME \
  --upsert
```

### Update Principal configuration

!!! info "Why not bind the principal to `127.0.0.1`?"
    The argocd-agent documentation recommends localhost binding for header-based auth, but this is incompatible with Istio. Istio's init container sets up iptables rules that redirect all inbound traffic through the sidecar. The sidecar then forwards to the application using the pod IP, not `127.0.0.1`. Binding to localhost makes the principal unreachable. With Istio, `STRICT` mTLS provides equivalent protection: iptables ensures no external traffic can bypass the sidecar, and the sidecar always overwrites the `x-forwarded-client-cert` header with the verified identity from the mTLS handshake. An attacker cannot forge this header without compromising the pod itself.

```bash
kubectl patch configmap argocd-agent-params \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  --patch "{\"data\":{
    \"principal.tls.insecure-plaintext\":\"true\",
    \"principal.auth\":\"header:x-forwarded-client-cert:^.*URI=spiffe://[^/]+/ns/[^/]+/sa/([^,;]+)\",
    \"principal.allowed-namespaces\":\"$AGENT_APP_NAME\"
}}"

kubectl patch svc argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  --type='json' \
  -p='[{"op":"replace","path":"/spec/ports/0/name","value":"grpc-principal"},{"op":"add","path":"/spec/ports/0/appProtocol","value":"grpc"}]'
```

```bash
cat <<EOF | kubectl apply --context kind-$PRINCIPAL_CLUSTER_NAME -f -
apiVersion: security.istio.io/v1beta1
kind: PeerAuthentication
metadata:
  name: argocd-agent-mtls
  namespace: $NAMESPACE_NAME
spec:
  mtls:
    mode: STRICT
---
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: argocd-agent-principal
  namespace: $NAMESPACE_NAME
spec:
  host: argocd-agent-principal.$NAMESPACE_NAME.svc.cluster.local
  trafficPolicy:
    connectionPool:
      http:
        idleTimeout: 300s
    tls:
      mode: ISTIO_MUTUAL
EOF

kubectl rollout restart deployment argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME

kubectl rollout status deployment argocd-agent-principal \
  -n $NAMESPACE_NAME \
  --context kind-$PRINCIPAL_CLUSTER_NAME \
  --timeout=120s
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

### Verify Principal installation
```bash
kubectl get pods -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME | grep principal

kubectl logs -n $NAMESPACE_NAME deployment/argocd-agent-principal --context kind-$PRINCIPAL_CLUSTER_NAME

# Expected logs:
# level=warning msg="TLS disabled - running in plaintext mode for service mesh integration"
# level=info msg="Now listening on [::]:8443"
```
<br />


## Workload Cluster Setup

This document uses **Managed mode** which is easier to get started with. <br />
For Autonomous mode, change the value `managed` to `autonomous` for the `AGENT_MODE` env variable.

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

### Install Istio with shared CA on Workload Cluster

Install the shared CA certificate **before** installing Istio:

```bash
kubectl create namespace istio-system --context kind-$AGENT_CLUSTER_NAME

kubectl create secret generic cacerts -n istio-system --context kind-$AGENT_CLUSTER_NAME \
  --from-file=ca-cert.pem=/tmp/istio-certs/cluster2/ca-cert.pem \
  --from-file=ca-key.pem=/tmp/istio-certs/cluster2/ca-key.pem \
  --from-file=root-cert.pem=/tmp/istio-certs/cluster2/root-cert.pem \
  --from-file=cert-chain.pem=/tmp/istio-certs/cluster2/cert-chain.pem

istioctl install --context kind-$AGENT_CLUSTER_NAME --set profile=demo -y
```

### Create namespace
```bash
kubectl create namespace $NAMESPACE_NAME --context kind-$AGENT_CLUSTER_NAME
kubectl label namespace $NAMESPACE_NAME istio-injection=enabled --context kind-$AGENT_CLUSTER_NAME
```

### Install Argo CD for Workload Cluster

```bash
kubectl apply -n $NAMESPACE_NAME \
  -k "https://github.com/argoproj-labs/argocd-agent/install/kubernetes/argo-cd/agent-$AGENT_MODE?ref=$RELEASE_BRANCH" \
  --context kind-$AGENT_CLUSTER_NAME
```

This configuration includes:

- ✅ **argocd-application-controller** (reconciles applications - **required on workload clusters**)
- ✅ **argocd-repo-server** (Git access for the application controller)
- ✅ **argocd-redis** (local state for the application controller)
- ❌ **argocd-server** (runs on control plane only)
- ❌ **argocd-dex-server** (runs on control plane only)
- ❌ **argocd-applicationset-controller** (not included by default)

!!! info "Why Application Controller Runs Here"
    The **argocd-application-controller** runs on workload clusters because it needs direct access to the Kubernetes API to create, update, and delete resources. The argocd-agent facilitates communication between the control plane and these controllers, enabling centralized management while maintaining local execution.

### Configure cross-cluster Istio mTLS

```bash
PRINCIPAL_EXTERNAL_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' argocd-hub-control-plane)
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

PRINCIPAL_NODE_PORT=$(kubectl get svc argocd-agent-principal -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME -o jsonpath='{.spec.ports[0].nodePort}')
echo "<principal-node-port>: $PRINCIPAL_NODE_PORT"

cat <<EOF | kubectl apply --context kind-$AGENT_CLUSTER_NAME -f -
apiVersion: networking.istio.io/v1beta1
kind: ServiceEntry
metadata:
  name: argocd-agent-principal-external
  namespace: $NAMESPACE_NAME
spec:
  hosts:
  - argocd-agent-principal.external
  addresses:
  - $PRINCIPAL_EXTERNAL_IP
  ports:
  - number: $PRINCIPAL_NODE_PORT
    name: grpc
    protocol: GRPC
  location: MESH_INTERNAL
  resolution: STATIC
  endpoints:
  - address: $PRINCIPAL_EXTERNAL_IP
    ports:
      grpc: $PRINCIPAL_NODE_PORT
---
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: argocd-agent-principal-external
  namespace: $NAMESPACE_NAME
spec:
  host: argocd-agent-principal.external
  trafficPolicy:
    tls:
      mode: ISTIO_MUTUAL
---
apiVersion: security.istio.io/v1beta1
kind: PeerAuthentication
metadata:
  name: argocd-agent-mtls
  namespace: $NAMESPACE_NAME
spec:
  mtls:
    mode: STRICT
EOF
```

### Create Agent configuration
Create Agent configuration on Principal. <br />

```bash
# Check NodePort
PRINCIPAL_EXTERNAL_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' argocd-hub-control-plane)
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

argocd-agentctl agent create $AGENT_APP_NAME \
  --principal-context kind-$PRINCIPAL_CLUSTER_NAME \
  --principal-namespace $NAMESPACE_NAME \
  --resource-proxy-server ${PRINCIPAL_EXTERNAL_IP}:9090
```

### Create Agent namespace on Principal
```bash
kubectl create namespace $AGENT_APP_NAME --context kind-$PRINCIPAL_CLUSTER_NAME
```

### Deploy Agent
```bash
kubectl apply -n $NAMESPACE_NAME \
  -k "https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=$RELEASE_BRANCH" \
  --context kind-$AGENT_CLUSTER_NAME
```

### Configure Agent SPIFFE identity and connection

With SPIFFE authentication, the agent name is derived from the Kubernetes service account name in the SPIFFE URI. Create a service account matching `$AGENT_APP_NAME`, configure the connection, and update the agent deployment to use the new identity:

```bash
# Create service account and RBAC bindings
kubectl create serviceaccount $AGENT_APP_NAME \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME

kubectl create rolebinding $AGENT_APP_NAME \
  --role=argocd-agent-agent \
  --serviceaccount=$NAMESPACE_NAME:$AGENT_APP_NAME \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME

kubectl create clusterrolebinding $AGENT_APP_NAME \
  --clusterrole=argocd-agent-agent \
  --serviceaccount=$NAMESPACE_NAME:$AGENT_APP_NAME \
  --context kind-$AGENT_CLUSTER_NAME

# Configure connection to Principal
PRINCIPAL_EXTERNAL_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' argocd-hub-control-plane)
echo "<principal-external-ip>: $PRINCIPAL_EXTERNAL_IP"

PRINCIPAL_NODE_PORT=$(kubectl get svc argocd-agent-principal -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME -o jsonpath='{.spec.ports[0].nodePort}')
echo "<principal-node-port>: $PRINCIPAL_NODE_PORT"

kubectl patch configmap argocd-agent-params \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME \
  --patch "{\"data\":{
    \"agent.server.address\":\"$PRINCIPAL_EXTERNAL_IP\",
    \"agent.server.port\":\"$PRINCIPAL_NODE_PORT\",
    \"agent.mode\":\"$AGENT_MODE\",
    \"agent.creds\":\"header:\",
    \"agent.tls.insecure-plaintext\":\"true\"
  }}"

# Update deployment with new service account (triggers rollout with all config changes)
kubectl patch deployment argocd-agent-agent \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME \
  --patch '{"spec":{"template":{"spec":{"serviceAccountName":"'$AGENT_APP_NAME'"}}}}'

kubectl rollout status deployment argocd-agent-agent \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME \
  --timeout=120s
```

<br />

## Verification

### Verify Agent connection
```bash
kubectl logs -n $NAMESPACE_NAME deployment/argocd-agent-agent --context kind-$AGENT_CLUSTER_NAME

# Expected output on success:
# time="..." level=info msg="Authentication successful" module=Connector
# time="..." level=info msg="Connected to argocd-agent-..." module=Connector
# time="..." level=info msg="Starting to send events to event stream" direction=send module=StreamEvent
```

### Verify Agent recognition on Principal
- Wait until connect `$AGENT_APP_NAME` appears.
```bash
kubectl logs -n $NAMESPACE_NAME deployment/argocd-agent-principal --context kind-$PRINCIPAL_CLUSTER_NAME

# Expected output on success:
# time="..." level=info msg="Successfully authenticated agent: $AGENT_APP_NAME" component=header-auth
# time="..." level=info msg="An agent connected to the subscription stream" client=$AGENT_APP_NAME method=Subscribe
# time="..." level=info msg="Updated connection status to 'Successful' in Cluster: '$AGENT_APP_NAME'" component=ClusterManager
```

### List connected Agents
```bash
argocd-agentctl agent list \
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

PRINCIPAL_NODE_PORT=$(kubectl get svc argocd-agent-principal -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME -o jsonpath='{.spec.ports[0].nodePort}')
echo "<principal-node-port>: $PRINCIPAL_NODE_PORT"

cat <<EOF | kubectl apply -f - --context kind-$PRINCIPAL_CLUSTER_NAME
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: test-app
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

Verify that the application is synchronized to the Agent:

```bash
# Check that the application exists on the principal
kubectl get applications -n $AGENT_APP_NAME --context kind-$PRINCIPAL_CLUSTER_NAME

# Check that the application appears on the workload cluster
kubectl get applications -n $NAMESPACE_NAME --context kind-$AGENT_CLUSTER_NAME
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
- You can verify that the Agent cluster (`$AGENT_APP_NAME`) is connected in the UI.

<br />

## Adding Another Agent Cluster
To add another agent, simply **increment** the `AGENT_USER_ID` by `+1` and proceed to the **Workload Cluster Setup** section below.
Each new agent automatically receives a **unique CIDR range** derived from its `AGENT_USER_ID` value.

```bash
export AGENT_USER_ID=$((AGENT_USER_ID + 1))
export AGENT_APP_NAME="agent-b"
export AGENT_CLUSTER_NAME="argocd-agent2"
export AGENT_POD_CIDR="10.$((244 + $AGENT_USER_ID)).0.0/16"
export AGENT_SVC_CIDR="10.$((96 + $AGENT_USER_ID)).0.0/16"

# Generate Istio intermediate CA for the new cluster
cd /tmp/istio-certs
make -f $ISTIO_DIR/tools/certs/Makefile.selfsigned.mk cluster${AGENT_USER_ID}-cacerts

# (optional) Check variables
echo "Agent App Name: $AGENT_APP_NAME"
echo "Agent Pod CIDR: $AGENT_POD_CIDR"
echo "Agent Service CIDR: $AGENT_SVC_CIDR"
```

!!! info "Next step"
    Once the new agent variables are set, continue to the [Workload Cluster Setup](#workload-cluster-setup) section to create and configure the new agent cluster.


## Troubleshooting

### Common Issues

**Authentication failures with SPIFFE**

If the `x-forwarded-client-cert` header is missing, check:

1. The principal service has `appProtocol: grpc` or a port name starting with `grpc-`
2. Both clusters use the same root CA (`cacerts` secret in `istio-system`)
3. The agent cluster has a `ServiceEntry` with `location: MESH_INTERNAL` for the principal's address

**Access Forbidden**
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

**Missing Server Secret Key**:
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

**Auth Failure (connection timeout)**:
```bash
# Re-check and restart agent deployment
PRINCIPAL_EXTERNAL_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' argocd-hub-control-plane)
PRINCIPAL_NODE_PORT=$(kubectl get svc argocd-agent-principal -n $NAMESPACE_NAME --context kind-$PRINCIPAL_CLUSTER_NAME -o jsonpath='{.spec.ports[0].nodePort}')

kubectl patch configmap argocd-agent-params \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME \
  --patch "{\"data\":{
    \"agent.server.address\":\"$PRINCIPAL_EXTERNAL_IP\",
    \"agent.server.port\":\"$PRINCIPAL_NODE_PORT\",
    \"agent.mode\":\"$AGENT_MODE\",
    \"agent.creds\":\"header:\",
    \"agent.tls.insecure-plaintext\":\"true\"
  }}"

kubectl rollout restart deployment argocd-agent-agent \
  -n $NAMESPACE_NAME \
  --context kind-$AGENT_CLUSTER_NAME
```
