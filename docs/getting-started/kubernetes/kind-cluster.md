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

## ⚠️ Important Notes

- **<25.10.09>** **tcp: lookup argocd-redis**: DNS lookup issue for redis occurs. <br />
Please modify the following files before starting:
1. Allow DNS lookup queries in cluster's networkpolicy
- `argocd-agent/install/kubernetes/agent/agent-networkpolicy-redis.yaml`, `argocd-agent/install/kubernetes/principal/principal-networkpolicy-redis.yaml` <br />
```yaml
    ports:
    - port: 6379
      protocol: TCP # This is the existing yaml
  # Add DNS query rules below
  - to:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: kube-system
    ports:
    - port: 53
      protocol: UDP
    - port: 53
      protocol: TCP
```
- **<25.10.09>** **Redis connection pool failed**: Principal and agent pods cannot access redis in their respective clusters. <br />
Please modify the following files before starting:

```bash
$ kubectl logs -n argocd deployment/argocd-agent-agent --tail=5
time="2025-10-09T06:53:26Z" level=error msg="Failed to get cluster info from cache" error="dial tcp 10.96.124.14:6379: i/o timeout"
redis: 2025/10/09 06:53:36 pool.go:367: redis: connection pool: failed to dial after 5 attempts: dial tcp 10.96.124.14:6379: i/o timeout
```

<br />

**Cause**: The argocd-redis pod from the original argo-cd resources imported by the argocd-agent project does not allow access from the newly created argocd-agent and argocd-principal.

**Solution**:

1. Add kustomize to allow `argocd-agent-agent` access in agent cluster's redis networkpolicy
- `argocd-agent/install/kubernetes/argo-cd/agent-managed/kustomization.yaml` <br />
```yaml
# At the top
namespace: argocd # Use your namespace

resources:
# ...
- patch: |-
    $patch: delete
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: argocd-applicationset-controller
# This is the existing yaml
# Add ingress for argocd-agent-agent pod below
- target:
    group: networking.k8s.io
    version: v1
    kind: NetworkPolicy
    name: argocd-redis-network-policy
  patch: |-
    - op: add
      path: /spec/ingress/0/from/-
      value:
        podSelector:
          matchLabels:
            app.kubernetes.io/name: argocd-agent-agent
```
2. `argocd-agent/install/kubernetes/argo-cd/principal/kustomization.yaml`  <br />
```yaml
# At the top
namespace: argocd # Use your namespace

resources:
# ...
- patch: |-
    $patch: delete
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: argocd-applicationset-controller
# This is the existing yaml
# Add ingress for argocd-agent-agent pod below
- target:
    group: networking.k8s.io
    version: v1
    kind: NetworkPolicy
    name: argocd-redis-network-policy
  patch: |-
    - op: add
      path: /spec/ingress/0/from/-
      value:
        podSelector:
          matchLabels:
            app.kubernetes.io/name: argocd-agent-agent
    - op: add
      path: /spec/ingress/0/from/-
      value:
        podSelector:
          matchLabels:
            app.kubernetes.io/name: argocd-agent-principal
```

- **<25.10.09>** **Service port mismatch issue**: Principal Service uses port 443 but Principal actually listens on port 8443. Connection timeout may occur when using NodePort in Kind environment.
Change `spec.ports.port` from 443 to 8443 in `argocd-agent/install/kubernetes/principal/principal-grpc-service.yaml` file.
- **<25.09.08>** Agent code tries to read all fields in CA secret as certificates, but the CA certificate generated by `argocd-agentctl pki issue agent` command is not automatically copied. See the Agent Client Certificate Issuance section in [Agent Creation and Connection](#agent-creation-and-connection).
- **<25.09.08>** Please change the namespace to "argocd" in the following files before starting. The default namespace is set to default, causing **`(CrashLoopBackOff)`** issues with role binding. <br />
`argocd-agent/install/kubernetes/agent/agent-clusterrolebinding.yaml` <br />
`argocd-agent/install/kubernetes/agent/agent-rolebinding.yaml` <br />
`argocd-agent/install/kubernetes/principal/principal-clusterrolebinding.yaml` <br />
`argocd-agent/install/kubernetes/principal/principal-rolebinding.yaml`
[See #403](https://github.com/argoproj-labs/argocd-agent/issues/403)

<br />

## Prerequisites

### Required Tools
- kubectl (v1.20 or higher)
- argocd-agentctl CLI tool

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

<br />

## Control Plane Setup

### Create cluster
- Add extraPortMappings for NodePort Binding
```bash
kind create cluster --name argocd-hub
```

### Create namespace
```bash
kubectl create namespace argocd --context kind-argocd-hub
```

### Install Argo CD for Control Plane
Install Principal-specific Argo CD configuration.

```bash
kubectl apply -n argocd \
  -k install/kubernetes/argo-cd/principal \
  --context kind-argocd-hub
```

This configuration includes:
- ✅ argocd-server (API and UI)
- ✅ argocd-dex-server (SSO)
- ✅ argocd-redis (state storage)
- ✅ argocd-repo-server (Git repository access)
- ❌ argocd-application-controller (runs only on workload clusters)

### Configure Apps-in-Any-Namespace
```bash
kubectl patch configmap argocd-cmd-params-cm -n argocd --context kind-argocd-hub \
  --patch '{"data":{"application.namespaces":"*"}}'

kubectl rollout restart deployment argocd-server -n argocd --context kind-argocd-hub
```

<br />

## PKI Setup

### Initialize Certificate Authority
```bash
./dist/argocd-agentctl pki init \
  --principal-context kind-argocd-hub \
  --principal-namespace argocd
```

### Generate JWT signing key
```bash
./dist/argocd-agentctl jwt create-key \
  --principal-context kind-argocd-hub \
  --principal-namespace argocd \
  --upsert
```

<br />

## Principal Installation

- **⚠️ Warning!!!** Please change the namespace to "argocd" in the following files before starting. The default namespace is set to default, causing **`(CrashLoopBackOff)`** issues with role binding. <br />
`argocd-agent/install/kubernetes/agent/agent-clusterrolebinding.yaml` <br />
`argocd-agent/install/kubernetes/agent/agent-rolebinding.yaml` <br />
`argocd-agent/install/kubernetes/principal/principal-clusterrolebinding.yaml` <br />
`argocd-agent/install/kubernetes/principal/principal-rolebinding.yaml`
[See #403](https://github.com/argoproj-labs/argocd-agent/issues/403)

### Deploy Principal components
```bash
kubectl apply -n argocd \
  -k install/kubernetes/principal \
  --context kind-argocd-hub
```

### Update Principal configuration
```bash
kubectl patch configmap argocd-agent-params -n argocd --context kind-argocd-hub \
  --patch "{\"data\":{
    \"principal.allowed-namespaces\":\"agent-a-ns\"
  }}"

kubectl rollout restart deployment argocd-agent-principal -n argocd --context kind-argocd-hub
```

### Verify Principal service exposure
Ensure the 8443 listener is properly configured on NodePort for Agent access to Principal:

```bash
# Option 1: LoadBalancer
kubectl patch svc argocd-agent-principal -n argocd --context kind-argocd-hub \
  --patch '{"spec":{"type":"LoadBalancer"}}'

kubectl get svc argocd-agent-principal -n argocd --context kind-argocd-hub

# Expected output:
# NAME                     TYPE       CLUSTER-IP      EXTERNAL-IP   PORT(S)          AGE
# argocd-agent-principal   NodePort   10.96.215.149   <none>        8443:30694/TCP   5s
```

### Generate Principal certificates

Issue gRPC server certificate (address for Agent connection) <br />
```bash
#./dist/argocd-agentctl pki issue principal \
#  --principal-context kind-argocd-hub \
#  --principal-namespace argocd \
#  --ip 127.0.0.1,<principal-external-ip> \
#  --dns localhost,<principal-dns-name> \
#  --upsert

# Check LoadBalancer IP
EXTERNAL_IP=$(kubectl get svc argocd-agent-principal -n argocd --context kind-argocd-hub -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "LoadBalancer IP: $EXTERNAL_IP"

./dist/argocd-agentctl pki issue principal \
  --principal-context kind-argocd-hub \
  --principal-namespace argocd \
  --ip 127.0.0.1,$EXTERNAL_IP \
  --dns localhost,$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="Hostname")].address}' --context kind-argocd-hub) \
  --upsert
```

Issue resource proxy certificate (address for Argo CD connection) <br />
```bash
#./dist/argocd-agentctl pki issue resource-proxy \
#  --principal-context kind-argocd-hub \
#  --principal-namespace argocd \
#  --ip 127.0.0.1,<resource-proxy-ip> \
#  --dns localhost,<resource-proxy-dns-name> \
#  --upsert

./dist/argocd-agentctl pki issue resource-proxy \
  --principal-context kind-argocd-hub \
  --principal-namespace argocd \
  --ip 127.0.0.1,$(kubectl get svc argocd-server -n argocd --context kind-argocd-hub -o jsonpath='{.spec.clusterIP}') \
  --dns localhost,$(kubectl get svc argocd-server -n argocd --context kind-argocd-hub -o jsonpath='{.metadata.name}.{.metadata.namespace}.svc.cluster.local') \
  --upsert
```

### Verify Principal installation
```bash
kubectl get pods -n argocd --context kind-argocd-hub | grep principal

kubectl logs -n argocd deployment/argocd-agent-principal --context kind-argocd-hub

# Expected logs:
# argocd-agent-principal-785cd96ddc-sm44r            1/1     Running   0          7s
# {"level":"info","msg":"Setting loglevel to info","time":"2025-09-13T07:25:38Z"}
# time="2025-09-13T07:25:38Z" level=info msg="Loading gRPC TLS certificate from secret argocd/argocd-agent-principal-tls" - Loading TLS certificate for gRPC communication from Kubernetes Secret
# ...
# time="2025-09-13T07:25:38Z" level=info msg="This server will require TLS client certs as part of authentication" module=server - TLS client certificates required for client authentication
# ...
# time="2025-09-13T07:25:38Z" level=info msg="Starting argocd-agent (server) v0.0.1-alpha (ns=argocd, allowed_namespaces=[])" module=server - Starting argocd-agent server (version, namespace, allowed namespaces list)
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
kind create cluster --name argocd-agent1
```

### Create namespace
```bash
kubectl create namespace argocd --context kind-argocd-agent1
```

### Install Argo CD for Workload Cluster

```bash
kubectl apply -n argocd \
  -k install/kubernetes/argo-cd/agent-managed \
  --context kind-argocd-agent1
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
#./dist/argocd-agentctl agent create agent-a \
#  --principal-context kind-argocd-hub \
#  --principal-namespace argocd \
#  --resource-proxy-server <principal-external-ip>:9090 \
#  --resource-proxy-username agent-a \
#  --resource-proxy-password "$(openssl rand -base64 32)"

./dist/argocd-agentctl agent create agent-a \
  --principal-context kind-argocd-hub \
  --principal-namespace argocd \
  --resource-proxy-server $(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' --context kind-argocd-hub):9090 \
  --resource-proxy-username agent-a \
  --resource-proxy-password "$(openssl rand -base64 32)"
```

### Issue Agent client certificate

⚠️ As of 25.09.08, the `argocd-agentctl pki issue agent` command does not automatically copy the CA secret to the Agent cluster. Also, Agent code tries to read all fields in the CA secret as certificates, but the `tls.key` field in `kubernetes.io/tls` type secret is a private key, causing errors.

Create CA secret in ConfigMap style (certificates only) with the following commands.

```bash
# Manually copy CA secret to Agent cluster (ConfigMap style)
kubectl get secret argocd-agent-ca -n argocd --context kind-argocd-hub -o jsonpath='{.data.tls\.crt}' | base64 -d > /tmp/ca.crt
kubectl create secret generic argocd-agent-ca --from-file=ca.crt=/tmp/ca.crt -n argocd --context kind-argocd-agent1

# Issue Agent client certificate
./dist/argocd-agentctl pki issue agent agent-a \
  --principal-context kind-argocd-hub \
  --agent-context kind-argocd-agent1 \
  --agent-namespace argocd \
  --upsert

# Clean up temporary file
rm /tmp/ca.crt
```

### Create Agent namespace on Principal (Optional)

**⚠️ Warning**: This step is unnecessary if creating Applications in the `argocd` namespace.

Create only if you want to use Agent-specific namespaces:

```bash
kubectl create namespace agent-a-ns --context kind-argocd-hub

# Also need to create AppProject in agent-a namespace
cat <<EOF | kubectl apply -f - --context kind-argocd-hub
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: default
  namespace: agent-a
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

**Recommended**: Use `argocd` namespace for simple testing.

### Verify certificate installation
Verify that Agent client certificates are properly installed:

```bash
kubectl get secret argocd-agent-client-tls -n argocd --context kind-argocd-agent1

# Expected result
# NAME                      TYPE                DATA   AGE
# argocd-agent-client-tls   kubernetes.io/tls   2      7s

kubectl get secret argocd-agent-ca -n argocd --context kind-argocd-agent1

# Expected result
# NAME              TYPE     DATA   AGE
# argocd-agent-ca   Opaque   1      15s
```

### Deploy Agent
```bash
kubectl apply -n argocd \
  -k install/kubernetes/agent \
  --context kind-argocd-agent1
```

### Configure Agent connection
Configure Agent to connect to Principal using mTLS authentication. 

```bash
EXTERNAL_IP=$(kubectl get svc argocd-agent-principal -n argocd --context kind-argocd-hub -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "Principal External IP: $EXTERNAL_IP"

kubectl patch configmap argocd-agent-params -n argocd --context kind-argocd-agent1 \
  --patch "{\"data\":{
    \"agent.server.address\":\"$EXTERNAL_IP\",
    \"agent.server.port\":\"8443\",
    \"agent.mode\":\"managed\",
    \"agent.creds\":\"mtls:any\"
  }}"

kubectl rollout restart deployment argocd-agent-agent -n argocd --context kind-argocd-agent1
```

<br />

## Verification

### Wait
```bash
sleep 40
```

### Debugging commands

```bash
# Check secrets
kubectl get secrets -n argocd --context kind-argocd-hub | grep agent
kubectl get secrets -n argocd --context kind-argocd-agent1 | grep agent

# Check Pod status
kubectl get pods -n argocd --context kind-argocd-hub
kubectl get pods -n argocd --context kind-argocd-agent1

# Check services
kubectl get svc -n argocd --context kind-argocd-hub
kubectl get svc -n argocd --context kind-argocd-agent1
```

### Verify Agent connection

- Wait until `redis.go:464: auto mode fallback:` output appears.

```bash
kubectl logs -n argocd deployment/argocd-agent-agent --context kind-argocd-agent1

# Expected output on success:
# time="2025-10-09T09:11:22Z" level=info msg="Authentication successful" module=Connector
# time="2025-10-09T09:11:22Z" level=info msg="Connected to argocd-agent-0.0.1-alpha" module=Connector
# time="2025-10-09T09:11:23Z" level=info msg="Starting to send events to event stream" direction=send module=StreamEvent
```

### Verify Agent recognition on Principal
- Wait until connect `agent-a` appears.
```bash
kubectl logs -n argocd deployment/argocd-agent-principal --context kind-argocd-hub

# Expected output on success:
# time="2025-10-09T09:11:23Z" level=info msg="An agent connected to the subscription stream" client=agent-a method=Subscribe
# time="2025-10-09T09:11:23Z" level=info msg="Updated connection status to 'Successful' in Cluster: 'agent-a'" component=ClusterManager
```

⚠️ **This log means Redis doesn't support maint_notifications feature and can be ignored.**
```bash
redis: 2025/10/11 12:03:06 redis.go:464: auto mode fallback: maintnotifications disabled due to handshake error: ERR unknown subcommand 'maint_notifications'. Try CLIENT HELP.
```

### List connected Agents
```bash
./dist/argocd-agentctl agent list \
  --principal-context kind-argocd-hub \
  --principal-namespace argocd
```

### Create test application

#### ⚠️ Important: Application-AppProject namespace matching

Application and AppProject must be in the same namespace. 
Cross-namespace references will cause "app is not allowed in project default" errors.

**Recommended approach**: Create Application in `argocd` namespace

```bash
cat <<EOF | kubectl apply -f - --context kind-argocd-hub
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: test-app
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: guestbook
  destination:
    server: https://$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' --context kind-argocd-hub):$(kubectl get svc argocd-agent-principal -n argocd --context kind-argocd-hub -o jsonpath='{.spec.ports[0].nodePort}')?agentName=agent-a
    namespace: guestbook
  syncPolicy:
    syncOptions:
    - CreateNamespace=true
EOF
```

Verify that the application is synchronized to the Agent.

```bash
# Delete Application in wrong namespace
kubectl delete application test-app -n agent-a --context kind-argocd-hub

# Recreate in argocd namespace (use the Application creation code above)
```

### Access ArgoCD UI

```bash
kubectl port-forward svc/argocd-server -n argocd 8080:443 --context kind-argocd-hub &

# Check initial admin password
kubectl -n argocd get secret argocd-initial-admin-secret --context kind-argocd-hub \
  -o jsonpath="{.data.password}" | base64 -d && echo
```

### If UI CORS occurs
```bash
# Enable insecure mode
kubectl patch configmap argocd-cmd-params-cm -n argocd --context kind-argocd-hub --patch '{"data":{"server.insecure":"true"}}'

# Restart principal
kubectl rollout restart deployment argocd-server -n argocd --context kind-argocd-hub
```

- URL: https://localhost:8080
- Username: `admin`
- Password: Value confirmed by the command above
- If SSL certificate warning appears in browser, click "Advanced" → "Proceed to unsafe"
- You can verify that the Agent cluster (`agent-a`) is connected in the UI.

![alt text](./images/image-1.png)

![alt text](./images/image.png)

<br />

## Cluster Restart Commands
```bash
# Restart Principal
kubectl rollout restart deployment argocd-agent-principal -n argocd --context kind-argocd-hub

# Restart Agent
kubectl rollout restart deployment argocd-agent-agent -n argocd --context kind-argocd-agent1
```

## Debugging) log: trace configuration
```bash
# Agent
kubectl patch cm argocd-agent-params -n argocd --context kind-argocd-agent1 --type merge -p '{"data":{"log.level":"trace"}}'

kubectl rollout restart deployment argocd-agent-agent -n argocd

# Principal
kubectl patch cm argocd-agent-params -n argocd --context kind-argocd-hub --type merge -p '{"data":{"principal.log.level":"trace"}}'

kubectl rollout restart deployment argocd-agent-principal -n argocd --context kind-argocd-hub
```


Name: "argocd-secret", Namespace: "argocd"
for: "STDIN": error when patching "STDIN": Operation cannot be fulfilled on secrets "argocd-secret": the object has been modified; please apply your changes to the latest version and try again

# Copy Argo CD Secret (sync principal info to agent cluster)
```bash
kubectl delete secret argocd-secret -n argocd --context kind-argocd-agent1
kubectl get secret argocd-secret -n argocd --context kind-argocd-hub -o yaml | sed 's/kind-argocd-hub/kind-argocd-agent1/' | kubectl apply -f - --context kind-argocd-agent1

kubectl rollout restart statefulset argocd-application-controller -n argocd --context kind-argocd-agent1
```
