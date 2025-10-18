# Migration from Traditional Multi-Cluster Argo CD

This guide outlines the strategy for migrating from a traditional, multi-cluster Argo CD installation to argocd-agent. The migration involves transitioning from a **push-based architecture** (where the control plane reaches out to workload clusters) to a **pull-based architecture** (where agents reach back to the control plane).

!!! warning "Migration Complexity"
    Migration from traditional Argo CD to argocd-agent requires careful planning and will involve service interruption. This is an architectural change that affects your entire GitOps infrastructure.

    Please be advised that this documentation is by no means a complete guide yet. Its purpose is to give you a rough overview about what is involved in migration and where the risks are. The devil will always be in the details.


## Understanding the Architectural Shift

### Traditional Multi-Cluster Argo CD

- **Push Model**: Control plane directly connects to remote cluster APIs
- **High Network Requirements**: Requires stable, low-latency connections to all clusters
- **Credential Management**: Control plane stores kubeconfig credentials for all clusters
- **Single Control Plane**: All application controllers run on the control plane cluster
- **Network Exposure**: Remote clusters must be network-accessible from control plane

### argocd-agent Architecture

- **Pull Model**: Lightweight agents connect back to the central control plane
- **Network Resilient**: Tolerates intermittent connections and high latency
- **Zero Trust**: Control plane never needs direct access to workload clusters
- **Distributed Compute**: Application controllers run locally on workload clusters
- **Inbound Connections Only**: Only agents initiate connections to the control plane

## Migration Strategy Overview

The migration process involves these high-level phases:

1. **[Assessment and Planning](#1-assessment-and-planning)** - Inventory your current setup
2. **[Control Plane Preparation](#2-control-plane-preparation)** - Set up argocd-agent control plane
3. **[Workload Cluster Migration](#3-workload-cluster-migration)** - Migrate clusters one by one
4. **[Application Migration](#4-application-migration)** - Transition your applications and AppProjects
5. **[Validation and Cleanup](#5-validation-and-cleanup)** - Verify and clean up old resources

## 1. Assessment and Planning

### Inventory Your Current Setup

Document your existing Argo CD installation:

```bash
# List all clusters configured in Argo CD
argocd cluster list

# Document all applications and their target clusters
argocd app list -o wide

# Note any ApplicationSets in use
kubectl get applicationsets -A
```

**Key items to document:**

- Number of managed clusters and their locations/network characteristics
- Applications per cluster and their sync patterns
- AppProjects and their cluster targeting patterns
- Repository configurations and access patterns
- RBAC configurations and user access patterns
- Custom resource hooks, sync waves, and automation
- Network connectivity requirements and constraints

### Choose Agent Operation Modes

For each workload cluster, decide on the operational mode:

**[Managed Mode](../concepts/agent-modes/managed.md)** - Choose when:

- You want centralized application management
- Applications are deployed from the control plane
- You need consistent policy enforcement across clusters
- Network connectivity is generally reliable

**[Autonomous Mode](../concepts/agent-modes/autonomous.md)** - Choose when:

- Clusters need to operate independently
- Applications are managed via GitOps (app-of-apps pattern) directly on the workload clusters
- Air-gapped or highly autonomous environments
- Network connectivity is unreliable or restricted

!!! tip "Mixed Modes"
    You can run different agents in different modes. For example, development clusters in managed mode and production clusters in autonomous mode.

### Plan Network Architecture

**For the Control Plane:**

- Determine how to expose the principal's gRPC service to agents
- Plan for LoadBalancer, Ingress, or NodePort configuration
- Consider TLS termination and certificate management

**For Workload Clusters:**

- Ensure outbound connectivity to the control plane
- Plan for proxy configurations if needed
- Consider firewall rules for agent connections

### Certificate Management Strategy

argocd-agent requires mTLS for security:

- **Development/Testing**: Use the provided `argocd-agentctl` tool
- **Production**: Integrate with your existing PKI or certificate management system
- Plan certificate rotation and renewal processes

## 2. Control Plane Preparation

### 2.1 Set Up argocd-agent Control Plane

Deploy the argocd-agent control plane alongside or in place of your existing Argo CD:

!!! note "Deployment Options"
    You can either deploy argocd-agent control plane on a new cluster or alongside your existing Argo CD. For production migrations, we recommend starting with a new cluster to minimize risk.

Follow the [Getting Started with Kubernetes](../getting-started/kubernetes/index.md) guide to:

1. Install the control plane components
2. Configure the principal service
3. Set up certificate authority and certificates
4. Configure apps-in-any-namespace support

### 2.2 Migrate Control Plane Data

If migrating from an existing Argo CD installation:

**Repository Configuration:**

```bash
# Export repository secrets from old Argo CD
kubectl get secrets -n argocd -l argocd.argoproj.io/secret-type=repository -o yaml > repositories.yaml

# Apply to new control plane
kubectl apply -f repositories.yaml -n argocd --context <control-plane-context>
```

**RBAC and User Configuration:**

```bash
# Export ConfigMaps containing RBAC policies
kubectl get configmap argocd-rbac-cm -n argocd -o yaml > rbac.yaml
kubectl get configmap argocd-cm -n argocd -o yaml > config.yaml

# Review and apply to new control plane
kubectl apply -f rbac.yaml -f config.yaml -n argocd --context <control-plane-context>
```

### 2.3 Expose Principal Service

Configure network access for agents to reach the principal:

```bash
# Example: LoadBalancer exposure
kubectl patch svc argocd-agent-principal -n argocd --context <control-plane-context> \
  --patch '{"spec":{"type":"LoadBalancer"}}'
```

See [Getting Started - Principal Setup](../getting-started/kubernetes/index.md#31-deploy-principal-component) for detailed configuration options.

## 3. Workload Cluster Migration

Migrate workload clusters one at a time to minimize risk and allow for testing.

### 3.1 Choose Migration Order

**Recommended order:**

1. Start with development/testing clusters
2. Move to staging environments
3. Migrate production clusters last
4. Consider cluster criticality and interdependencies

### 3.2 Per-Cluster Migration Process

For each workload cluster:

#### 3.2.1 Install Agent Components

Follow the [Getting Started guide](../getting-started/kubernetes/index.md#step-4-workload-cluster-setup) to:

1. Install Argo CD components appropriate for your chosen mode
2. Deploy the argocd-agent
3. Configure certificates and connectivity
4. Verify agent connection to principal

#### 3.2.2 Verify Connectivity

```bash
# Check agent logs for successful connection
kubectl logs -n argocd deployment/argocd-agent --context <workload-cluster-context>

# Verify cluster appears in control plane
argocd cluster list --context <control-plane-context>
```

#### 3.2.3 Configure Cluster-Specific Settings

- Set up any cluster-specific repository access
- Configure resource quotas and limits
- Apply RBAC policies as needed

## 4. Application Migration

### 4.1 Migration Strategy by Mode

#### Managed Mode Applications

1. **Create Applications on Control Plane**: Applications are created in namespaces named after the target agent

```bash
# Application will be deployed to the workload cluster by the agent
kubectl apply -f - <<EOF
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: my-workload-cluster  # Must match agent name
spec:
  # ... application specification
  destination:
    name: my-workload-cluster  # Points to workload cluster
EOF
```

2. **Verify Synchronization**: Check that applications appear and sync on workload clusters

#### Autonomous Mode Applications

1. **Create Applications on Workload Cluster**: Applications are managed locally and reported to control plane

```bash
# Create application on workload cluster
kubectl apply -f my-app.yaml --context <workload-cluster-context>
```

2. **Verify Observability**: Check that application status appears on control plane UI

### 4.2 ApplicationSet Considerations

!!! warning "ApplicationSet Limitations"
    ApplicationSets are not fully supported in the current version of argocd-agent. Plan to migrate ApplicationSets to individual Applications or wait for future support.

### 4.3 AppProject Migration

AppProjects in argocd-agent work differently from traditional Argo CD due to the distributed nature of the architecture. The migration approach depends on your chosen agent mode.

!!! info "AppProject Behavior Changes"
    Unlike traditional Argo CD where AppProjects are shared across all clusters, argocd-agent uses different synchronization strategies based on agent mode. See [AppProject Synchronization](./appprojects.md) for detailed behavior.

#### Understanding AppProject Distribution

**Traditional Argo CD:**
- AppProjects are created once on the control plane
- All clusters share the same AppProject definitions
- Projects can reference multiple clusters directly

**argocd-agent:**
- **Managed Mode**: AppProjects created on control plane, distributed to matching agents
- **Autonomous Mode**: AppProjects created on agents, synchronized back to control plane
- Projects are transformed for each agent's local context

#### Migrating AppProjects for Managed Mode

1. **Inventory Existing AppProjects**:
```bash
# Export current AppProjects
kubectl get appprojects -n argocd -o yaml > legacy-appprojects.yaml
```

2. **Update AppProject Specifications**: Modify each AppProject to use agent-aware patterns:

```yaml
# Before (Traditional Argo CD)
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: production-project
  namespace: argocd
spec:
  destinations:
  - name: production-cluster-1
    server: https://prod-cluster-1.example.com
  - name: production-cluster-2  
    server: https://prod-cluster-2.example.com
  sourceRepos:
  - "*"

---
# After (argocd-agent Managed Mode)
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: production-project
  namespace: argocd
spec:
  # Use glob patterns matching your agent names
  sourceNamespaces:
  - "production-*"
  destinations:
  - name: "production-*"
    namespace: "*"
    server: "*"
  sourceRepos:
  - "*"
```

3. **Create Updated AppProjects**: Apply the modified AppProjects to the argocd-agent control plane:

```bash
# Apply updated AppProjects
kubectl apply -f updated-appprojects.yaml --context <control-plane-context>
```

4. **Verify Distribution**: Check that AppProjects are distributed to the correct agents:

```bash
# Check AppProjects on workload clusters
kubectl get appprojects -n argocd --context <workload-cluster-context>

# Verify the transformed AppProject content
kubectl get appproject production-project -n argocd -o yaml --context <workload-cluster-context>
```

#### Migrating AppProjects for Autonomous Mode

1. **Create AppProjects on Workload Clusters**: For autonomous agents, create AppProjects directly on each workload cluster:

```yaml
# Create on each autonomous agent cluster
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: local-project  # Will become "agent-name-local-project" on control plane
  namespace: argocd
spec:
  destinations:
  - name: "in-cluster"
    namespace: "*"
    server: "https://kubernetes.default.svc"
  sourceNamespaces:
  - argocd
  sourceRepos:
  - "*"
```

```bash
# Apply to each autonomous agent
kubectl apply -f autonomous-appproject.yaml --context <workload-cluster-context>
```

2. **Verify Control Plane Synchronization**: Check that AppProjects appear on the control plane with prefixed names:

```bash
# List AppProjects on control plane - look for prefixed names
kubectl get appprojects -A --context <control-plane-context>

# Example: "local-project" becomes "production-agent-local-project"
```

#### AppProject Migration Strategies

**Strategy 1: Direct Mapping** (Recommended for Managed Mode)
- Create one AppProject per agent pattern
- Use glob patterns to target multiple similar agents
- Suitable when agents have similar requirements

```yaml
# Single AppProject for all production agents
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: production-workloads
spec:
  sourceNamespaces:
  - "prod-*"
  destinations:
  - name: "prod-*"
```

**Strategy 2: Per-Agent Projects** (Recommended for Autonomous Mode)
- Create specific AppProjects on each agent
- Allows for agent-specific customization
- Better isolation between agents

```yaml
# Specific AppProject on each autonomous agent
apiVersion: argoproj.io/v1alpha1
kind: AppProject  
metadata:
  name: edge-services  # Becomes "edge-01-edge-services" on control plane
```

**Strategy 3: Hybrid Approach**
- Use managed mode for common, shared projects
- Use autonomous mode for agent-specific projects
- Mix of central control and local autonomy

#### Handling Complex AppProject Scenarios

**Multi-Cluster Projects**: If your legacy AppProjects span multiple clusters:

1. **Split by Agent**: Create separate AppProjects for each agent group
2. **Use Patterns**: Leverage glob patterns to target multiple agents
3. **Consolidate**: Merge similar projects where possible

**RBAC Integration**: Update AppProject roles and policies:

```yaml
# Update roles for argocd-agent context
spec:  
  roles:
  - name: developers
    policies:
    - p, proj:production-project:developers, applications, get, production-*, allow
    - p, proj:production-project:developers, applications, sync, production-*, allow
```

**Repository Access**: Ensure repository credentials are available:

```bash
# Verify repository secrets are present on control plane
kubectl get secrets -n argocd -l argocd.argoproj.io/secret-type=repository

# For autonomous agents, ensure local repository access
kubectl get secrets -n argocd -l argocd.argoproj.io/secret-type=repository --context <workload-cluster-context>
```

#### Validation Steps

1. **Pattern Matching**: Verify your glob patterns work correctly:
```bash
# Test pattern matching (example using shell globbing)
for agent in production-east production-west staging-test; do
  if [[ "$agent" == production-* ]]; then
    echo "$agent matches production-* pattern"
  fi
done
```

2. **Agent Distribution**: Confirm AppProjects reach the intended agents:
```bash
# Check principal logs for distribution events
kubectl logs -n argocd deployment/argocd-agent-principal | grep -i appproject

# Check agent logs for received AppProjects
kubectl logs -n argocd deployment/argocd-agent --context <workload-cluster-context> | grep -i appproject
```

3. **Application Compatibility**: Ensure Applications can reference the new AppProjects:
```bash
# Verify Applications can find their AppProjects
argocd app list --context <control-plane-context>
argocd app get <app-name> --context <control-plane-context>
```

### 4.4 Testing and Validation

For each migrated application and AppProject:

1. **Verify Deployment**: Ensure applications deploy correctly on target clusters
2. **Test Sync Operations**: Verify sync, refresh, and rollback operations work
3. **Check Status Reporting**: Confirm health and sync status appear in control plane UI
4. **Validate RBAC**: Test user access and permissions with new AppProject structure
5. **Test AppProject Policies**: Verify project-level restrictions work as expected

## 5. Validation and Cleanup

### 5.1 Full System Validation

Once all clusters and applications are migrated:

**Connectivity Testing:**

```bash
# Verify all agents are connected
kubectl get agents -A --context <control-plane-context>

# Check principal logs for any connection issues
kubectl logs -n argocd deployment/argocd-agent-principal --context <control-plane-context>
```

**Application Health:**

```bash
# Verify all applications are healthy
argocd app list --context <control-plane-context>

# Check for any sync failures
argocd app list --sync-status OutOfSync --context <control-plane-context>
```

**Resource Accessibility:**

- Test [live resource viewing](./live-resources.md) functionality
- Verify resource actions and exec capabilities work as expected

### 5.2 Performance Validation

Monitor the new architecture:

- Agent connection stability and reconnection behavior  
- Application sync performance and reliability
- Resource utilization on both control plane and workload clusters
- Network traffic patterns and efficiency

### 5.3 Cleanup Legacy Resources

After successful validation:

**Remove Old Cluster Configurations:**

```bash
# Remove cluster secrets from legacy Argo CD
argocd cluster rm <cluster-server-url>
```

**Clean Up Legacy Applications:**

- Archive or remove application definitions from the old control plane
- Remove any cluster-specific kubeconfig secrets
- Update CI/CD pipelines to point to new control plane

**Clean Up Legacy AppProjects:**
```bash
# Remove legacy AppProjects from old control plane
kubectl delete appprojects <legacy-project-names> -n argocd --context <legacy-control-plane-context>

# Verify new AppProject structure is working
kubectl get appprojects -A --context <new-control-plane-context>
```

**Update Documentation:**

- Update runbooks and operational procedures
- Document new cluster onboarding processes
- Update disaster recovery procedures

## Post-Migration Considerations

### Operational Changes

**Cluster Onboarding:** Follow the [Adding Agents](./adding-agents.md) guide for future cluster additions.

**Certificate Management:** Plan for regular certificate rotation and renewal. See PKI management documentation for both [agent](../configuration/agent/pki-certificates.md) and [principal](../configuration/principal/pki-certificates.md) for details.

**Monitoring and Alerting:** Update monitoring systems to track agent connectivity and sync status.

### Rollback Planning

In case of critical issues:

1. **Keep Legacy Infrastructure**: Maintain your old Argo CD setup until migration is fully validated
2. **Application-Level Rollback**: Individual applications can be reverted to the legacy control plane
3. **Cluster-Level Rollback**: Entire clusters can be switched back by updating cluster configurations

### Getting Help

For migration support:

- [GitHub Discussions](https://github.com/argoproj-labs/argocd-agent/discussions) - Community support
- [#argo-cd-agent](https://cloud-native.slack.com/archives/C07L5SX6A9J) - Real-time chat on CNCF Slack
- [Issue Tracker](https://github.com/argoproj-labs/argocd-agent/issues) - Bug reports and feature requests

## Additional Resources

- [Architecture Overview](../concepts/architecture.md) - Understanding the technical foundations
- [Agent Modes](../concepts/agent-modes/index.md) - Detailed mode comparisons  
- [Configuration Guide](../configuration/index.md) - Advanced configuration options