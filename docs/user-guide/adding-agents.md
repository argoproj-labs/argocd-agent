# Adding New Agents

This document explains how to add new agents to your argocd-agent deployment and connect them to the principal using the `argocd-agentctl` CLI tool.

## Overview

Adding a new agent involves several steps:

1. **Prerequisites Setup**: Ensure PKI and principal are configured
2. **Agent Creation**: Use `argocd-agentctl` to create agent configuration
3. **Certificate Management**: Issue client certificates for the agent
4. **Agent Deployment**: Deploy the agent to the workload cluster
5. **Verification**: Confirm the agent connects successfully

## Prerequisites

Before adding a new agent, ensure you have:

- **Control Plane Ready**: Principal component installed and running
- **PKI Initialized**: Certificate Authority set up for the deployment
- **CLI Tool**: `argocd-agentctl` binary available and configured
- **Cluster Access**: Admin access to both principal and workload clusters
- **Agent Name**: Unique DNS-compliant name for the agent

### Agent Naming Requirements

Agent names must comply with [RFC 1123 DNS label standards](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names):

- Contain only lowercase letters, numbers, and hyphens
- Start and end with alphanumeric characters
- Maximum 63 characters
- Be unique across all agents

**Good Examples**: `production-cluster`, `staging-east`, `dev-team-a`  
**Bad Examples**: `Production_Cluster`, `staging.east`, `-dev-team`

## Step 1: Setup PKI (One-Time)

If you haven't already set up the PKI for your deployment, initialize it first:

```bash
# Initialize the Certificate Authority
argocd-agentctl pki init \
  --principal-context <control-plane-context> \
  --principal-namespace argocd

# Issue principal server certificate
argocd-agentctl pki issue principal \
  --principal-context <control-plane-context> \
  --ip <principal-ip-addresses> \
  --dns <principal-dns-names> \
  --upsert

# Issue resource proxy certificate
argocd-agentctl pki issue resource-proxy \
  --principal-context <control-plane-context> \
  --ip <resource-proxy-ip-addresses> \
  --dns <resource-proxy-dns-names> \
  --upsert

# Create JWT signing key
argocd-agentctl jwt create-key \
  --principal-context <control-plane-context> \
  --principal-namespace argocd \
  --upsert
```

!!! important
    Replace `<control-plane-context>`, `<principal-ip-addresses>`, and `<principal-dns-names>` with your actual values.

## Step 2: Create Agent Configuration

Create the agent configuration on the principal cluster:

```bash
argocd-agentctl agent create <agent-name> \
  --principal-context <control-plane-context> \
  --principal-namespace argocd \
  --resource-proxy-server <resource-proxy-service-name>:9090 
```

The resource proxy service's name is usually `argocd-agent-resource-proxy`.

!!! important
    The value given as `resource-proxy-service-name` must match a SAN entry in your resource proxy's TLS certificate

### What This Command Does

1. **Creates Cluster Secret**: Stores agent configuration as an Argo CD cluster secret
2. **Generates Client Certificate**: Creates mTLS certificate for Argo CD to authenticate to the resource proxy
3. **Validates Agent Name**: Ensures the agent name meets requirements
4. **Prevents Duplicates**: Checks that the agent doesn't already exist

## Step 3: Issue Agent Client Certificate

Generate and deploy the client certificate for the agent:

```bash
argocd-agentctl pki issue agent <agent-name> \
  --principal-context <control-plane-context> \
  --agent-context <workload-cluster-context> \
  --agent-namespace argocd \
  --upsert
```

This command:

- Generates a client certificate signed by the principal's CA
- Stores the certificate in the agent's cluster as a Kubernetes secret
- Configures the certificate with the agent's name as the subject

## Step 4: Create Agent Namespace

Create the required namespace for the agent on the principal cluster:

```bash
# Create namespace for managed agents
kubectl create namespace <agent-name> --context <control-plane-context>

# For autonomous agents, the namespace is typically created automatically
# but you can create it manually if needed
```

## Step 5: Deploy Agent to Workload Cluster

### Option A: Using Kubernetes Manifests

1. **Create Authentication Secret**:

```bash
kubectl create secret generic argocd-agent-agent-userpass \
  --from-literal=credentials="userpass:<agent-name>:<password>" \
  --namespace argocd \
  --context <workload-cluster-context>
```

2. **Deploy Agent Components**:

```bash
kubectl apply -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=main' \
  --context <workload-cluster-context>
```

3. **Configure Agent Parameters**:

```bash
kubectl patch configmap argocd-agent-params \
  --namespace argocd \
  --context <workload-cluster-context> \
  --patch '{"data":{"agent.server.address":"<principal-address>","agent.server.port":"<principal-port>","agent.mode":"<autonomous|managed>"}}'
```

### Option B: Using Helm (if available)

```bash
helm install argocd-agent-agent ./charts/agent \
  --namespace argocd \
  --kube-context <workload-cluster-context> \
  --set agent.server.address=<principal-address> \
  --set agent.server.port=<principal-port> \
  --set agent.mode=<autonomous|managed>
```

## Step 6: Configuration

### Agent Configuration Options

The agent behavior is controlled via the `argocd-agent-params` ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
  namespace: argocd
data:
  # Agent operational mode
  agent.mode: "autonomous"  # or "managed"
  
  # Principal connection settings
  agent.server.address: "<principal-address>"
  agent.server.port: "8443"
  
  # Authentication method
  agent.creds: "mtls:^CN=(.+)$"
  
  # TLS settings
  agent.tls.client.insecure: "false"
  agent.tls.secret-name: "argocd-agent-client-tls"
  agent.tls.root-ca-secret-name: "argocd-agent-ca"
  
  # Logging
  agent.log.level: "info"
```

### Authentication Methods

**mTLS Authentication**:
```yaml
agent.creds: "mtls:^CN=(.+)$"  # Regex to extract agent ID from cert subject
```

**UserPass Authentication** (deprecated):

```yaml
agent.creds: "userpass:/app/config/creds/userpass.creds"
```

## Step 7: Verification

### Check Agent Status

1. **Verify Agent Deployment**:
```bash
kubectl get pods -n argocd --context <workload-cluster-context>
kubectl logs -n argocd deployment/argocd-agent-agent --context <workload-cluster-context>
```

2. **Check Principal Connection**:
```bash
kubectl logs -n argocd deployment/argocd-agent-principal --context <control-plane-context>
```

3. **List Connected Agents**:
```bash
argocd-agentctl agent list --principal-context <control-plane-context>
```

### Expected Log Messages

**Successful Agent Connection**:
```
INFO[0001] Starting argocd-agent (agent) v0.1.0 (ns=argocd, mode=autonomous, auth=userpass)
INFO[0002] Authentication successful
INFO[0003] Connected to argocd-agent-principal v0.1.0
```

**Successful Principal Recognition**:
```
INFO[0001] Agent production-cluster connected successfully
INFO[0002] Creating a new queue pair for client production-cluster
```

## Troubleshooting

### Common Issues

**Agent Creation Fails**:
```bash
# Check if agent already exists
argocd-agentctl agent inspect <agent-name>

# Verify PKI is initialized
argocd-agentctl pki inspect

# Check principal cluster connectivity
kubectl get secrets -n argocd --context <control-plane-context>
```

**Connection Issues**:
```bash
# Verify certificates
kubectl get secrets -n argocd --context <workload-cluster-context> | grep tls

# Check network connectivity
kubectl exec -it deployment/argocd-agent-agent -n argocd --context <workload-cluster-context> -- \
  nc -zv <principal-address> <principal-port>

# Review authentication credentials
kubectl get secret argocd-agent-agent-userpass -n argocd --context <workload-cluster-context> -o yaml
```

**Certificate Issues**:
```bash
# Regenerate agent certificate
argocd-agentctl pki issue agent <agent-name> \
  --principal-context <control-plane-context> \
  --agent-context <workload-cluster-context> \
  --agent-namespace argocd \
  --upsert

# Check certificate expiration
kubectl get secret argocd-agent-client-tls -n argocd --context <workload-cluster-context> -o yaml | \
  base64 -d | openssl x509 -text -noout
```

## Managing Multiple Agents

### Bulk Agent Creation

For multiple agents, you can script the process:

```bash
#!/bin/bash
AGENTS=("prod-east" "prod-west" "staging" "dev")

for agent in "${AGENTS[@]}"; do
  echo "Creating agent: $agent"
  
  # Create agent configuration
  argocd-agentctl agent create "$agent" \
    --resource-proxy-server <resource-proxy-service-name>:9090

  # Issue client certificate
  argocd-agentctl pki issue agent "$agent" \
    --agent-context "cluster-$agent" \
    --agent-namespace argocd \
    --upsert
  
  # Create namespace for managed agents
  kubectl create namespace "$agent" --context control-plane || true
  
  echo "Agent $agent created successfully"
done
```

### Agent Lifecycle Management

**Inspect Agent Configuration**:
```bash
argocd-agentctl agent inspect <agent-name>
```

**Update Agent Configuration**:
```bash
argocd-agentctl agent reconfigure <agent-name> \
  --resource-proxy-server <new-server-address> \
  --reissue-client-cert
```

**Remove Agent**:
```bash
# Delete agent configuration
kubectl delete secret cluster-<agent-name> -n argocd --context <control-plane-context>

# Delete agent namespace (for managed agents)
kubectl delete namespace <agent-name> --context <control-plane-context>

# Remove agent deployment
kubectl delete -n argocd \
  -k 'https://github.com/argoproj-labs/argocd-agent/install/kubernetes/agent?ref=main' \
  --context <workload-cluster-context>
```

## Security Best Practices

1. **Use Strong Passwords**: Generate secure passwords for resource proxy authentication
2. **Regular Certificate Rotation**: Periodically regenerate client certificates
3. **Network Security**: Restrict network access to principal endpoints
4. **Audit Logging**: Monitor agent connection and authentication logs
5. **Principle of Least Privilege**: Only grant necessary permissions to agents

## Next Steps

After successfully adding agents:

1. **Configure Applications**: Create Applications in the appropriate namespaces
2. **Set up AppProjects**: Define project boundaries and access controls
3. **Monitor Resources**: Use the live resources feature to view cluster state
4. **Implement GitOps**: Configure your deployment pipelines

For more information, refer to:
- [Application Synchronization](./applications.md)
- [AppProject Synchronization](./appprojects.md)
- [Live Resources](./live-resources.md) 