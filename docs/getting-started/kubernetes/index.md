# Getting Started with Kubernetes

This comprehensive guide will walk you through setting up argocd-agent on Kubernetes, including installing Argo CD on both the control plane and workload clusters, deploying the principal and agent components, and connecting your first agent using mutual TLS (mTLS) authentication.

!!! info "Component Placement Overview"
    Before proceeding, make sure you understand [which Argo CD components run where](../index.md#argo-cd-component-placement). This guide follows those placement requirements strictly.

## Prerequisites

Before starting, ensure you have:

### Clusters

- **At least two Kubernetes clusters**: One for the control plane and one or more for workloads
- **Administrative access** to all clusters with `kubectl` configured
- **Network connectivity** from workload clusters to the control plane cluster

### Tools

- `kubectl` (v1.20 or later)
- `argocd-agentctl` CLI tool ([download from releases](https://github.com/argoproj-labs/argocd-agent/releases))
- `kustomize` (optional, for customization)

### Agent Planning

- **Unique agent names** following [DNS label standards](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names)
- **Networking plan** for exposing the principal service to agents
- **Decision on agent mode**: autonomous or managed for each agent

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

## Step 1: Control Plane Setup

### 1.1 Create Namespace

```bash
kubectl create namespace argocd --context <control-plane-context>
```

### 1.2 Install Argo CD on Control Plane

Install a customized Argo CD instance that excludes components that will run on workload clusters replacing <release-branch> with the release you wish to use:

```bash
# Apply the principal-specific Argo CD configuration
kubectl apply -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/argo-cd/principal?ref=<release-branch>' \
  --context <control-plane-context>
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

### 1.3 Configure Apps-in-Any-Namespace

Update the Argo CD server configuration to enable apps-in-any-namespace:

```bash
kubectl patch configmap argocd-cmd-params-cm -n argocd --context <control-plane-context> \
  --patch '{"data":{"application.namespaces":"*"}}'

# Restart the server to apply changes
kubectl rollout restart deployment argocd-server -n argocd --context <control-plane-context>
```

### 1.4 Expose Argo CD UI (Optional)

```bash
# Option 1: Port forwarding (development)
kubectl port-forward svc/argocd-server -n argocd 8080:443 --context <control-plane-context>

# Option 2: LoadBalancer (production)
kubectl patch svc argocd-server -n argocd --context <control-plane-context> \
  --patch '{"spec":{"type":"LoadBalancer"}}'

# Option 3: Ingress (production with custom domain)
# Create your own ingress resource based on your ingress controller
```

## Step 2: PKI Setup

### 2.1 Initialize Certificate Authority

```bash
# Initialize the PKI
argocd-agentctl pki init \
  --principal-context <control-plane-context> \
  --principal-namespace argocd
```

### 2.2 Generate Principal Certificates

```bash
# Issue gRPC server certificate (agents connect to this)
argocd-agentctl pki issue principal \
  --principal-context <control-plane-context> \
  --principal-namespace argocd \
  --ip 127.0.0.1,<principal-external-ip> \
  --dns localhost,<principal-dns-name> \
  --upsert

# Issue resource proxy certificate (Argo CD connects to this)
argocd-agentctl pki issue resource-proxy \
  --principal-context <control-plane-context> \
  --principal-namespace argocd \
  --ip 127.0.0.1,<resource-proxy-ip> \
  --dns localhost,<resource-proxy-dns> \
  --upsert
```

Replace placeholders:

- `<principal-external-ip>`: External IP where agents will reach the principal
- `<principal-dns-name>`: DNS name for the principal service
- `<resource-proxy-ip>`: IP for the resource proxy (usually cluster-internal)
- `<resource-proxy-dns>`: DNS name for resource proxy

### 2.3 Create JWT Signing Key

```bash
argocd-agentctl jwt create-key \
  --principal-context <control-plane-context> \
  --principal-namespace argocd \
  --upsert
```

### 2.4 Setup Redis TLS (Required)

!!! warning "Redis TLS is Required"
    Redis TLS is **enabled by default** in argocd-agent. All Redis connections must use TLS to protect sensitive data.

!!! tip "Order of Operations"
    Follow this exact order to avoid connection errors:
    
    1. Generate TLS certificates
    2. Create secrets
    3. **Configure Redis for TLS** (patches Redis deployment)
    4. Wait for Redis pods to restart
    5. Verify Redis TLS is working
    
    The Argo CD components are already pre-configured to use Redis TLS in the installation manifests.

#### Generate Redis TLS Certificates

```bash
# Generate CA certificate
openssl genrsa -out redis-ca.key 4096
openssl req -new -x509 -days 3650 -key redis-ca.key -out redis-ca.crt \
  -subj "/CN=Redis CA"

# Generate Redis server certificate
openssl genrsa -out redis-server.key 4096
openssl req -new -key redis-server.key -out redis-server.csr \
  -subj "/CN=argocd-redis"

# Create SAN extension file
cat > redis-server.ext <<EOF
subjectAltName = @alt_names
[alt_names]
DNS.1 = argocd-redis
DNS.2 = argocd-redis.argocd.svc.cluster.local
DNS.3 = localhost
IP.1 = 127.0.0.1
EOF

# Sign the certificate
openssl x509 -req -in redis-server.csr -CA redis-ca.crt -CAkey redis-ca.key \
  -CAcreateserial -out redis-server.crt -days 365 -extfile redis-server.ext

```

**Note:** The same certificate is used for both Redis server and the principal's Redis proxy.

#### Create Redis TLS Secret

```bash
# Create single secret with all TLS materials
kubectl create secret generic argocd-redis-tls \
  --from-file=tls.crt=redis-server.crt \
  --from-file=tls.key=redis-server.key \
  --from-file=ca.crt=redis-ca.crt \
  -n argocd \
  --context <control-plane-context>
```

#### Configure Redis for TLS

```bash
# Add volume for TLS certificates (creates array if not exists, appends if exists)
kubectl patch deployment argocd-redis -n argocd --context <control-plane-context> --type='json' -p='[
  {"op": "add", "path": "/spec/template/spec/volumes/-", "value":
    {"name": "redis-tls", "secret": {"secretName": "argocd-redis-tls"}}
  }
]'

# Add volume mount (creates array if not exists, appends if exists)
kubectl patch deployment argocd-redis -n argocd --context <control-plane-context> --type='json' -p='[
  {"op": "add", "path": "/spec/template/spec/containers/0/volumeMounts/-", "value":
    {"name": "redis-tls", "mountPath": "/app/tls"}
  }
]'

# Update Redis args to enable TLS
kubectl patch deployment argocd-redis -n argocd --context <control-plane-context> --type='json' -p='[
  {"op": "replace", "path": "/spec/template/spec/containers/0/args", "value": [
    "--save", "",
    "--appendonly", "no",
    "--requirepass", "$(REDIS_PASSWORD)",
    "--tls-port", "6379",
    "--port", "0",
    "--tls-cert-file", "/app/tls/tls.crt",
    "--tls-key-file", "/app/tls/tls.key",
    "--tls-ca-cert-file", "/app/tls/ca.crt",
    "--tls-auth-clients", "no"
  ]}
]'

# Wait for Redis to restart
kubectl rollout status deployment/argocd-redis -n argocd --context <control-plane-context>
```

#### Verify Redis TLS

```bash
# Get the Redis pod name
REDIS_POD=$(kubectl get pods -n argocd --context <control-plane-context> -l app.kubernetes.io/name=argocd-redis -o jsonpath='{.items[0].metadata.name}')

# Test TLS connection
kubectl exec -it $REDIS_POD -n argocd --context <control-plane-context> -- \
  redis-cli --tls \
  --cert /app/tls/tls.crt \
  --key /app/tls/tls.key \
  --cacert /app/tls/ca.crt \
  ping
# Should output: PONG
```

!!! info "Automatic TLS Configuration"
    When using the argocd-agent Kubernetes installation manifests from Step 1.2 and Step 4.3:
    
    - **Argo CD components** (server, repo-server, application-controller) are **pre-configured** to use Redis TLS with proper CA certificate validation via `--redis-use-tls` and `--redis-ca-certificate` flags
    - **Principal's Redis proxy** automatically uses TLS when the `argocd-redis-tls` secret is present
    - **Agent** connects to its local Redis with TLS enabled by default
    
    You only need to:
    1. Create the TLS secret (single secret for all components)
    2. Patch the Redis deployment (as shown above)
    
    No manual ConfigMap or Argo CD deployment patches are required!

## Step 3: Install Principal

### 3.1 Deploy Principal Component

Change <release-branch> to the release you wish to use:

```bash
kubectl apply -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/principal?ref=<release-branch>' \
  --context <control-plane-context>
```

The principal is pre-configured to use mTLS authentication by default. You can verify this configuration:

```bash
# Check the principal authentication configuration
kubectl get configmap argocd-agent-params -n argocd --context <control-plane-context> \
  -o jsonpath='{.data.principal\.auth}'
# Should output: mtls:CN=([^,]+)
```

Update principal configuration:

```bash
# Allow principal to operate in agent's namespace
kubectl patch configmap argocd-agent-params -n argocd --context <control-plane-context> \
  --patch "{\"data\":{
    \"principal.allowed-namespaces\":\"my-first-agent\"
  }}"

# Restart the principal to apply changes
kubectl rollout restart deployment argocd-agent-principal -n argocd --context <control-plane-context>
```

### 3.2 Expose Principal Service

The principal's gRPC service needs to be accessible from workload clusters:

```bash
# Option 1: LoadBalancer
kubectl patch svc argocd-agent-principal -n argocd --context <control-plane-context> \
  --patch '{"spec":{"type":"LoadBalancer"}}'

# Option 2: NodePort
kubectl patch svc argocd-agent-principal -n argocd --context <control-plane-context> \
  --patch '{"spec":{"type":"NodePort","ports":[{"port":8443,"nodePort":30443}]}}'
```

### 3.3 Verify Principal Installation

```bash
# Check pod status
kubectl get pods -n argocd --context <control-plane-context> | grep principal

# Check logs
kubectl logs -n argocd deployment/argocd-agent-principal --context <control-plane-context>

# Expected log output:
# INFO[0001] Starting argocd-agent-principal v0.1.0
# INFO[0002] gRPC server listening on :8443
# INFO[0003] Resource proxy started on :9090
```

## Step 4: Workload Cluster Setup

### 4.1 Choose Agent Mode

- **Managed Mode**: Principal manages Applications, agent executes them
-  **Autonomous Mode**: Agent manages its own Applications, principal provides observability

For this guide, we'll use **managed mode** as it's simpler for getting started. For autonomous mode, replace `agent-managed` with `agent-autonomous` in the following commands.

### 4.2 Create Namespace

```bash
kubectl create namespace argocd --context <workload-cluster-context>
```

### 4.3 Install Argo CD on Workload Cluster

Replace <release-branch> with the release you wish to use:

```bash
# For managed agents
kubectl apply -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/argo-cd/agent-managed?ref=<release-branch>' \
  --context <workload-cluster-context>

# For autonomous agents (alternative)
# kubectl apply -n argocd \
#   -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/argo-cd/agent-autonomous?ref=<release-branch>' \
#   --context <workload-cluster-context>
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

### 4.4 Setup Redis TLS on Workload Cluster

!!! warning "Required"
    Workload cluster Redis must also use TLS. The agent and Argo CD components connect with TLS enabled by default when using the installation manifests from Step 4.3.

#### Generate Redis Certificate for Workload Cluster

```bash
# Generate Redis server certificate for workload cluster
openssl genrsa -out redis-workload.key 4096
openssl req -new -key redis-workload.key -out redis-workload.csr \
  -subj "/CN=argocd-redis"

# Create SAN extension file
cat > redis-workload.ext <<EOF
subjectAltName = @alt_names
[alt_names]
DNS.1 = argocd-redis
DNS.2 = argocd-redis.argocd.svc.cluster.local
DNS.3 = localhost
IP.1 = 127.0.0.1
EOF

# Sign with the same CA (reuse redis-ca.crt and redis-ca.key from Step 2.4)
openssl x509 -req -in redis-workload.csr -CA redis-ca.crt -CAkey redis-ca.key \
  -CAcreateserial -out redis-workload.crt -days 365 -extfile redis-workload.ext
```

#### Create Redis TLS Secret

```bash
kubectl create secret generic argocd-redis-tls \
  --from-file=tls.crt=redis-workload.crt \
  --from-file=tls.key=redis-workload.key \
  --from-file=ca.crt=redis-ca.crt \
  -n argocd \
  --context <workload-cluster-context>
```

#### Configure Redis for TLS

```bash
# Add volume for TLS certificates (creates array if not exists, appends if exists)
kubectl patch deployment argocd-redis -n argocd --context <workload-cluster-context> --type='json' -p='[
  {"op": "add", "path": "/spec/template/spec/volumes/-", "value":
    {"name": "redis-tls", "secret": {"secretName": "argocd-redis-tls"}}
  }
]'

# Add volume mount (creates array if not exists, appends if exists)
kubectl patch deployment argocd-redis -n argocd --context <workload-cluster-context> --type='json' -p='[
  {"op": "add", "path": "/spec/template/spec/containers/0/volumeMounts/-", "value":
    {"name": "redis-tls", "mountPath": "/app/tls"}
  }
]'

# Update Redis args to enable TLS
kubectl patch deployment argocd-redis -n argocd --context <workload-cluster-context> --type='json' -p='[
  {"op": "replace", "path": "/spec/template/spec/containers/0/args", "value": [
    "--save", "",
    "--appendonly", "no",
    "--requirepass", "$(REDIS_PASSWORD)",
    "--tls-port", "6379",
    "--port", "0",
    "--tls-cert-file", "/app/tls/tls.crt",
    "--tls-key-file", "/app/tls/tls.key",
    "--tls-ca-cert-file", "/app/tls/ca.crt",
    "--tls-auth-clients", "no"
  ]}
]'

# Wait for Redis to restart
kubectl rollout status deployment/argocd-redis -n argocd --context <workload-cluster-context>
```

#### Verify Redis TLS

```bash
# Get the Redis pod name
REDIS_POD=$(kubectl get pods -n argocd --context <workload-cluster-context> -l app.kubernetes.io/name=argocd-redis -o jsonpath='{.items[0].metadata.name}')

# Test TLS connection
kubectl exec -it $REDIS_POD -n argocd --context <workload-cluster-context> -- \
  redis-cli --tls \
  --cert /app/tls/tls.crt \
  --key /app/tls/tls.key \
  --cacert /app/tls/ca.crt \
  ping
# Should output: PONG
```

## Step 5: Create and Connect Your First Agent

### 5.1 Create Agent Configuration

```bash
# Create agent configuration on the principal
argocd-agentctl agent create my-first-agent \
  --principal-context <control-plane-context> \
  --principal-namespace argocd \
  --resource-proxy-server <principal-external-ip>:9090 \
  --resource-proxy-username my-first-agent \
  --resource-proxy-password "$(openssl rand -base64 32)"
```

### 5.2 Issue Agent Client Certificate

```bash
argocd-agentctl pki issue agent my-first-agent \
  --principal-context <control-plane-context> \
  --agent-context <workload-cluster-context> \
  --agent-namespace argocd \
  --upsert
```

### 5.3 Propagate Certificate Authority to Agent

```bash
argocd-agentctl pki propagate \
  --principal-context <control-plane-context> \
  --principal-namespace argocd \
  --agent-context <workload-cluster-context> \
  --agent-namespace argocd
```

### 5.4 Verify Certificate Installation

The agent client certificate should already be installed from step 5.2. Verify it exists:

```bash
# Verify the client certificate secret exists
kubectl get secret argocd-agent-client-tls -n argocd --context <workload-cluster-context>

# Verify the CA certificate secret exists
kubectl get secret argocd-agent-ca -n argocd --context <workload-cluster-context>
```

### 5.5 Create Agent Namespace on Principal

For managed agents, create a namespace on the principal where the agent's Applications will be created and managed:

```bash
kubectl create namespace my-first-agent --context <control-plane-context>
```

### 5.6 Deploy Agent

Replace <release-branch> with the version of the release you wish to use:

```bash
kubectl apply -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=<release-branch>' \
  --context <workload-cluster-context>
```

### 5.7 Configure Agent Connection

Update the agent configuration to connect to your principal using mTLS authentication:

```bash
kubectl patch configmap argocd-agent-params -n argocd --context <workload-cluster-context> \
  --patch "{\"data\":{
    \"agent.server.address\":\"<principal-external-ip>\",
    \"agent.server.port\":\"8443\",
    \"agent.mode\":\"managed\",
    \"agent.creds\":\"mtls:any\"
  }}"

# Restart the agent to apply changes
kubectl rollout restart deployment argocd-agent-agent -n argocd --context <workload-cluster-context>
```

## Step 6: Verification

### 6.1 Check Agent Connection

```bash
# Check agent logs
kubectl logs -n argocd deployment/argocd-agent-agent --context <workload-cluster-context>

# Expected output:
# INFO[0001] Starting argocd-agent (agent) v0.1.0 (ns=argocd, mode=managed, auth=mtls)
# INFO[0002] Authentication successful
# INFO[0003] Connected to argocd-agent-principal v0.1.0
```

### 6.2 Verify Principal Recognizes Agent

```bash
# Check principal logs
kubectl logs -n argocd deployment/argocd-agent-principal --context <control-plane-context>

# Expected output:
# INFO[0001] Agent my-first-agent connected successfully
# INFO[0002] Creating a new queue pair for client my-first-agent
```

### 6.3 List Connected Agents

```bash
argocd-agentctl agent list \
  --principal-context <control-plane-context> \
  --principal-namespace argocd
```

### 6.4 Test Application Synchronization (Managed Mode)

Propagate a default AppProject from principal to the agent:

```bash
kubectl patch appproject default -n argocd \
  --context <control-plane-context> --type='merge' \
  --patch='{"spec":{"sourceNamespaces":["*"],"destinations":[{"name":"*","namespace":"*","server":"*"}]}}'
```

Verify the AppProject is synchronized to the agent:

```bash
# Check that the AppProject appears on the workload cluster
kubectl get appprojs -n argocd --context <workload-cluster-context>
```

Create a test application on the principal:

```bash
cat <<EOF | kubectl apply -f - --context <control-plane-context>
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: test-app
  namespace: my-first-agent
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: guestbook
  destination:
    server: https://<principal-external-ip:port>?agentName=my-first-agent # For example, https://172.18.255.200:443?agentName=my-first-agent
    namespace: guestbook
  syncPolicy:
    syncOptions:
    - CreateNamespace=true
EOF
```

Verify the application is synchronized to the agent:

```bash
# Check that the application exists on the principal
kubectl get applications -n my-first-agent --context <control-plane-context>

# Check that the application appears on the workload cluster
kubectl get applications -n argocd --context <workload-cluster-context>
```

Access the Argo CD UI to see your agent and its applications!

## Next Steps

### Security Hardening
1. **Replace development certificates** with production-grade PKI
2. **Configure proper RBAC** in Argo CD
3. **Set up network policies** to restrict traffic
4. **Enable audit logging** for compliance

### Scaling
1. **Add more agents** using the [Adding Agents guide](../../user-guide/adding-agents.md)
2. **Configure AppProjects** for multi-tenancy
3. **Set up ApplicationSets** for automated app deployment
4. **Implement GitOps workflows** with your Git repositories

### Operations
1. **Monitor agent connectivity** and health
2. **Set up backup procedures** for Argo CD state
3. **Plan certificate rotation** schedules
4. **Configure alerts** for agent disconnections

## Troubleshooting

### Common Issues

**Agent Cannot Connect**:
```bash
# Check network connectivity
kubectl run debug --rm -it --image=busybox --context <workload-cluster-context> \
  -- nc -zv <principal-external-ip> 8443

# Verify certificates
kubectl get secrets -n argocd --context <workload-cluster-context> | grep tls

# Check client certificate
kubectl get secret argocd-agent-client-tls -n argocd --context <workload-cluster-context> -o yaml
```

**Principal Service Not Accessible**:
```bash
# Check service status
kubectl get svc argocd-agent-principal -n argocd --context <control-plane-context>

# For LoadBalancer, check external IP assignment
kubectl describe svc argocd-agent-principal -n argocd --context <control-plane-context>
```

**Applications Not Syncing**:
```bash
# Check principal logs for sync errors
kubectl logs -n argocd deployment/argocd-agent-principal --context <control-plane-context>

# Verify namespace exists on principal (managed mode)
kubectl get namespace my-first-agent --context <control-plane-context>

# Check application controller logs on workload cluster
kubectl logs -n argocd deployment/argocd-application-controller --context <workload-cluster-context>
```

**Certificate Issues**:
```bash
# Regenerate agent certificate
argocd-agentctl pki issue agent my-first-agent \
  --principal-context <control-plane-context> \
  --agent-context <workload-cluster-context> \
  --agent-namespace argocd \
  --upsert

# Check certificate validity
kubectl get secret argocd-agent-client-tls -n argocd --context <workload-cluster-context> \
  -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -text -noout
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
kubectl patch secret argocd-secret -n argocd \
  --context <workload-cluster-context> \
  --patch='{"data":{"server.secretkey":"'$(openssl rand -base64 32 | base64 -w 0)'"}}'
```

## Related Documentation

- [Agent Modes](../../concepts/agent-modes/index.md) - Understanding autonomous vs managed modes
- [Adding More Agents](../../user-guide/adding-agents.md) - Scale your deployment
- [Application Synchronization](../../user-guide/applications.md) - How apps sync between clusters
- [AppProject Synchronization](../../user-guide/appprojects.md) - Managing project boundaries
- [Live Resources](../../user-guide/live-resources.md) - Viewing resources across clusters
