# Getting Started with Kubernetes

This comprehensive guide will walk you through setting up argocd-agent on Kubernetes, including installing Argo CD on both the control plane and workload clusters, deploying the principal and agent components, and connecting your first agent.

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

Install a customized Argo CD instance that excludes components that will run on workload clusters:

```bash
# Apply the principal-specific Argo CD configuration
kubectl apply -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/argo-cd/principal?ref=main' \
  --context <control-plane-context>
```

This configuration includes:

- ✅ **argocd-server** (API and UI)
- ✅ **argocd-dex-server** (SSO, if needed)  
- ✅ **argocd-redis** (state storage)
- ✅ **argocd-repo-server** (Git repository access)
- ❌ **argocd-application-controller** (runs on workload clusters)
- ❌ **argocd-applicationset-controller** (not yet supported)

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

## Step 3: Install Principal

### 3.1 Deploy Principal Component

```bash
kubectl apply -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/principal?ref=main' \
  --context <control-plane-context>
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

```bash
# For managed agents
kubectl apply -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/argo-cd/agent-managed?ref=main' \
  --context <workload-cluster-context>

# For autonomous agents (alternative)
# kubectl apply -n argocd \
#   -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/argo-cd/agent-autonomous?ref=main' \
#   --context <workload-cluster-context>
```

This configuration includes:

- ✅ **argocd-application-controller** (reconciles applications)
- ✅ **argocd-repo-server** (Git access)
- ✅ **argocd-redis** (local state)
- ❌ **argocd-server** (runs on control plane)
- ❌ **argocd-dex-server** (runs on control plane)
- ❌ **argocd-applicationset-controller** (managed agents don't create their own ApplicationSets)

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

### 5.3 Create Agent Namespace on Principal

For managed agents, create a namespace on the principal where the agent's Applications will be created and managed:

```bash
kubectl create namespace my-first-agent --context <control-plane-context>
```

### 5.4 Create Agent Authentication Secret

```bash
# Get the password from the agent configuration (or use the one you set)
AGENT_PASSWORD=$(kubectl get secret cluster-my-first-agent -n argocd --context <control-plane-context> \
  -o jsonpath='{.data.config}' | base64 -d | jq -r '.password')

# Create userpass credentials file
kubectl create secret generic argocd-agent-agent-userpass \
  --from-literal=credentials="userpass:my-first-agent:${AGENT_PASSWORD}" \
  --namespace argocd \
  --context <workload-cluster-context>
```

### 5.5 Deploy Agent

```bash
kubectl apply -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=main' \
  --context <workload-cluster-context>
```

### 5.6 Configure Agent Connection

Update the agent configuration to connect to your principal:

```bash
kubectl patch configmap argocd-agent-params -n argocd --context <workload-cluster-context> \
  --patch "{\"data\":{
    \"agent.server.address\":\"<principal-external-ip>\",
    \"agent.server.port\":\"8443\",
    \"agent.mode\":\"managed\",
    \"agent.creds\":\"userpass:/app/config/creds/userpass.creds\"
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
# INFO[0001] Starting argocd-agent (agent) v0.1.0 (ns=argocd, mode=managed, auth=userpass)
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

### 6.4 Test Application Synchronization

Create a test application on the principal (managed mode):

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
    server: https://kubernetes.default.svc
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

# Check authentication
kubectl get secret argocd-agent-agent-userpass -n argocd --context <workload-cluster-context> -o yaml
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

## Related Documentation

- [Agent Modes](../../concepts/agent-modes/index.md) - Understanding autonomous vs managed modes
- [Adding More Agents](../../user-guide/adding-agents.md) - Scale your deployment
- [Application Synchronization](../../user-guide/applications.md) - How apps sync between clusters
- [AppProject Synchronization](../../user-guide/appprojects.md) - Managing project boundaries
- [Live Resources](../../user-guide/live-resources.md) - Viewing resources across clusters
