# Application Synchronization (managed agents)

This document covers how Argo CD `Applications` are synchronized between the principal (control plane) and **managed** agents on workload clusters.

For **autonomous** agents, see [Application synchronization (autonomous agents)](../autonomous-agent/applications-autonomous-mode.md). For how managed and autonomous modes differ at a conceptual level, see [Agent modes](../../concepts/agent-modes.md).

## Overview

Application synchronization in argocd-agent supports two mapping modes that determine how Applications are routed to agents:

- **Namespace-based mapping** (default): Applications are mapped to agents using **namespaces**, where each namespace on the principal corresponds to a specific agent.
- **Destination-based mapping**: Applications are mapped to agents using `spec.destination.name`, allowing multiple namespaces to route to the same agent.

For managed agents, Applications are created on the principal and distributed to agents; agents send status updates back to the principal.

!!! tip "Choosing a Mapping Mode"
    If you are unsure which mode to use, see [Agent Mapping Modes](./agent-mapping.md) for a detailed comparison. Destination-based mapping is recommended for multi-tenant environments and when using ApplicationSets targeting multiple agents.

## Managed Agent Mode

### Creating Applications

In managed mode, Applications are created on the **principal cluster** (control plane). The principal determines which agent should receive an Application based on the active mapping mode:

- **Namespace-based mapping** (default): The Application's namespace determines the target agent
- **Destination-based mapping**: The Application's `spec.destination.name` determines the target agent

### Namespace-Based Mapping (Default)

#### Namespace to Agent Mapping

Applications are mapped to agents through a simple naming convention:

- **Namespace name on principal** = **Agent name**
- Example: Applications in namespace `production-cluster` are sent to the agent named `production-cluster`

#### Example: Creating an Application (Namespace-Based)

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: production-cluster  # This determines the target agent
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: guestbook
  destination:
    server: https://kubernetes.default.svc  # Will be transformed
    namespace: guestbook
  syncPolicy:
    syncOptions:
    - CreateNamespace=true
```

When this Application is created in namespace `production-cluster` on the principal, it will be automatically sent to the managed agent named `production-cluster`.

#### Agent-Side Transformation (Namespace-Based)

When an Application is sent to a managed agent using namespace-based mapping, it undergoes transformation:

1. **Destination Server**: Transformed to point to the local cluster:
```yaml
   destination:
     server: ""
     name: "in-cluster"
     namespace: "guestbook"
```

2. **Namespace**: Changed to the agent's local namespace:
```yaml
   metadata:
     namespace: argocd  # Agent's local namespace
```

3. **Source UID Annotation**: Added to track the original source for synchronization purposes

### Destination-Based Mapping

#### How Routing Works

With destination-based mapping enabled, the principal routes Applications to agents based on `spec.destination.name`. The Application's namespace is preserved rather than being used as a routing key.

- Applications can live in any namespace on the principal
- Multiple teams can share a namespace while targeting different agents
- Applications from multiple namespaces can route to the same agent

#### Example: Creating an Application (Destination-Based)

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: argocd  # Can be any namespace
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: guestbook
  destination:
    name: production-cluster  # This determines the target agent
    namespace: guestbook
  syncPolicy:
    syncOptions:
    - CreateNamespace=true
```

When this Application is created on the principal with `destination.name: production-cluster`, it will be routed to the managed agent named `production-cluster` regardless of the Application's namespace.

#### Agent-Side Transformation (Destination-Based)

When an Application is sent to a managed agent using destination-based mapping, the transformation differs from namespace-based mapping:

1. **Destination Server**: Transformed to point to the local cluster:
```yaml
   destination:
     server: ""
     name: "in-cluster"
     namespace: "guestbook"
```

2. **Namespace**: The Application's **original namespace is preserved** on the agent:
```yaml
   metadata:
     namespace: team-a  # Preserved from the principal
```

!!! note "Namespace Creation"
    With destination-based mapping, the agent must create Applications in namespaces that may not exist yet. Use the `--create-namespace` flag on the agent to automatically create namespaces when needed.

### Status Synchronization

In managed mode, the agent continuously monitors Application status changes and sends updates back to the principal:

- **Principal → Agent**: Spec changes (configuration, source repo, destination, etc.)
- **Agent → Principal**: Status updates (sync status, health, operation results, etc.)

The principal maintains the "source of truth" for the Application specification, while the agent reports back the actual state of the deployment.

### Conflict Resolution

If an Application is modified directly on the managed agent cluster (outside of the principal), these changes will be **automatically reverted** to maintain the principal as the single source of truth.

### Lifecycle Management

- **Creation**: Create Applications on the principal — in the agent's namespace (namespace-based mapping) or with `spec.destination.name` set to the agent (destination-based mapping)
- **Updates**: Modify Applications on the principal; changes are automatically propagated
- **Deletion**: Delete Applications on the principal; they're automatically removed from the agent
- **Agent Connection**: When an agent connects, it receives all Applications that are mapped to it

## Best practices

### Namespace-Based Mapping

1. **Namespace Organization**: Use clear, descriptive namespace names that match your agent names:
```
   production-east
   production-west
   staging-cluster
   development-cluster
```

2. **Application Naming**: Use consistent naming conventions within each namespace:
```yaml
   metadata:
     name: frontend-prod
     namespace: production-east
```

3. **Monitor Status**: Regularly check Application status on the principal to ensure successful deployments

4. **Avoid Direct Changes**: Never modify Applications directly on agent clusters; always use the principal

### Destination-Based Mapping

1. **Use Consistent destination.name**: Ensure `spec.destination.name` exactly matches the agent name:
```yaml
   spec:
     destination:
       name: production-east  # Must match agent name
       namespace: my-app
```

2. **Organize by Team or Project**: Since Applications are not bound to agent-specific namespaces, organize them by team, project, or environment instead:
```yaml
   metadata:
     name: frontend-prod
     namespace: team-platform  # Need not match agent
```

3. **Enable Namespace Creation**: Use `--create-namespace` on agents to handle namespaces that may not exist yet

4. **Configure Allowed Namespaces**: Restrict which namespaces the agent and principal can operate in using `--allowed-namespaces` with glob patterns for security

5. **AppProject sourceNamespaces**: Ensure your AppProjects have `sourceNamespaces` configured to allow Applications from the namespaces you use

## Troubleshooting

### Application Not Appearing on Agent (Managed Mode)

1. **Check Namespace** (namespace-based mapping): Verify the Application is created in the correct namespace on the principal
2. **Check destination.name** (destination-based mapping): Verify `spec.destination.name` matches the agent name exactly and both principal and agent have `--destination-based-mapping` enabled
3. **Verify Agent Connection**: Ensure the agent is connected and the namespace name matches the agent name
4. **Review Logs**: Check principal logs for distribution events and agent logs for reception
5. **Check Source UID**: Look for source UID annotations to verify proper synchronization
6. **Check Allowed Namespaces** (destination-based mapping): Verify the Application's namespace is included in `--allowed-namespaces` on both principal and agent

### Status Not Updating

1. **Check Agent Health**: Verify the agent is running and connected
2. **Review Application Controller**: Ensure Argo CD's application-controller is running on the agent
3. **Inspect Annotations**: Check for proper source UID annotations
4. **Monitor Network**: Verify stable network connectivity between agent and principal

### Sync Conflicts

- **Source UID Mismatch**: 
    - Usually resolved automatically by recreating the Application
    - Check logs for conflict resolution messages
- **Cache Issues** (Managed Mode):
    - Agent may revert unexpected changes
    - Review Application cache logs on the agent
- **Manual Intervention Required**:
    - Delete and recreate the Application if automatic resolution fails
    - Ensure the principal has the desired specification

## Monitoring and observability

### Key Metrics to Monitor

- **Application Creation/Update/Delete Events**: Track synchronization activity
- **Status Update Frequency**: Monitor how often agents report status changes
- **Sync Errors**: Watch for failed synchronization attempts
- **Cache Hit/Miss Rates**: Monitor cache effectiveness on managed agents

### Log Events to Watch

- **Principal Logs**:
    - Application distribution events
    - Status update processing
    - Resync operations

- **Agent Logs**:
    - Application creation/update events
    - Status reporting activities
    - Cache operations (managed mode)
    - Conflict resolution actions

### Health Checks

- **Principal**: Monitor Application informer sync status
- **Agent**: Verify Application backend is running and synced
- **Network**: Ensure stable gRPC connection between principal and agents

## Ignore Sync Label

Applications can be labeled with `argocd-agent.argoproj-labs.io/ignore-sync: "true"` to keep them on the principal only, exempting them from distribution to the matching agent — for example, control-plane infrastructure Applications that should never be sent to a workload cluster.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: principal-only-app
  namespace: production-cluster
  labels:
    argocd-agent.argoproj-labs.io/ignore-sync: "true"  # Skip sync to agent
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: guestbook
  destination:
    server: https://kubernetes.default.svc
    namespace: guestbook
```

This Application will remain only on the principal cluster and will not be sent to the `production-cluster` agent, even though it's created in that agent's namespace.

See [Ignore sync label](../ignore-sync.md) for the full label reference, including behavior for AppProjects.

## Security Considerations

### Access Control

- **Managed Mode**: Principal controls all Application specifications; implement RBAC on the principal
- **Network Security**: Ensure encrypted communication channels between principal and agents.

### Isolation

- **Namespace Isolation**: Each agent operates in its own namespace on the principal
- **Agent Authentication**: Proper authentication prevents unauthorized agents from connecting
- **Resource Limits**: Consider implementing resource quotas per agent namespace

### Audit and Compliance

- **Change Tracking**: All Application changes are logged and auditable
- **Source Tracking**: Source UID annotations provide clear provenance
- **Access Logs**: Monitor who creates/modifies Applications on the principal
