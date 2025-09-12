# Application Synchronization

This document explains how Argo CD `Applications` are synchronized between the principal (control plane) and agents (workload clusters), covering both managed and autonomous agent modes.

## Overview

Application synchronization in argocd-agent follows a fundamentally different pattern than AppProjects. Applications are mapped to agents using **namespaces**, where each namespace on the principal corresponds to a specific agent. This provides a clear and scalable way to manage Applications across multiple clusters.

The synchronization mechanism varies depending on the agent mode:

- **Managed agents**: Applications are created on the principal and distributed to agents; agents send status updates back
- **Autonomous agents**: Applications are created on the agent and synchronized to the principal; principal acts as a read-only mirror for specifications but can still perform sync, refresh, and resource actions

## Managed Agent Mode

### Creating Applications

In managed mode, Applications must be created in the **agent's corresponding namespace** on the **principal cluster** (control plane). The principal determines which agent should receive an Application based on the namespace where it's created.

### Namespace to Agent Mapping

Applications are mapped to agents through a simple naming convention:

- **Namespace name on principal** = **Agent name**
- Example: Applications in namespace `production-cluster` are sent to the agent named `production-cluster`

### Example: Creating an Application for a Managed Agent

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

### Agent-Side Transformation

When an Application is sent to a managed agent, it undergoes transformation to make it agent-specific:

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

### Status Synchronization

In managed mode, the agent continuously monitors Application status changes and sends updates back to the principal:

- **Principal → Agent**: Spec changes (configuration, source repo, destination, etc.)
- **Agent → Principal**: Status updates (sync status, health, operation results, etc.)

The principal maintains the "source of truth" for the Application specification, while the agent reports back the actual state of the deployment.

### Conflict Resolution

If an Application is modified directly on the managed agent cluster (outside of the principal), these changes will be **automatically reverted** to maintain the principal as the single source of truth.

### Lifecycle Management

- **Creation**: Create Applications on the principal in the agent's namespace
- **Updates**: Modify Applications on the principal; changes are automatically propagated
- **Deletion**: Delete Applications on the principal; they're automatically removed from the agent
- **Agent Connection**: When an agent connects, it receives all Applications in its namespace

## Autonomous Agent Mode

### Creating Applications

In autonomous mode, Applications are created directly on the **agent cluster**. The agent then synchronizes these Applications to the principal, where they appear in a namespace named after the agent.

### Example: Creating an Application on an Autonomous Agent

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: argocd  # Local namespace on the agent
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
```

### Principal-Side Placement

When an Application is received from an autonomous agent, the principal places it in a namespace corresponding to the agent name:

- **Agent**: `production-agent`
- **Application created on agent**: `my-app`
- **Application on principal**: `my-app` in namespace `production-agent`

### Project Name Transformation

Applications from autonomous agents may have their project references transformed to avoid conflicts:

- If the Application uses a non-default project, it may be prefixed with the agent name
- Example: `my-project` becomes `production-agent-my-project` on the principal

For more information, refer to [Managing AppProjects](./appprojects.md#autonomous-agent-mode)

### Status Synchronization

In autonomous mode, the agent is the source of truth:

- **Agent → Principal**: Both spec and status changes
- **Principal → Agent**: Read-only mirror (no modifications sent back)

The principal serves as a centralized view of all Applications across autonomous agents but doesn't control them.

### Lifecycle Management

- **Creation**: Create Applications on the agent; they automatically appear on the principal
- **Updates**: Modify Applications on the agent; changes are propagated to the principal
- **Deletion**: Delete Applications on the agent; they're automatically removed from the principal
- **Local Control**: The agent maintains full control over its Applications

## Application Caching and Resilience

### Managed Agent Caching

Managed agents maintain a local cache of Application specifications to handle network disruptions:

- **Cache Purpose**: Ensures Applications remain in sync even when disconnected from the principal
- **Cache Storage**: Stores the last known good specification for each Application
- **Conflict Detection**: Uses source UID annotations to detect and resolve conflicts
- **Automatic Recovery**: When connectivity is restored, the cache helps detect and resolve any drift

### Resync Mechanisms

Both principal and agents implement resync mechanisms to handle restarts and connectivity issues:

#### On Agent Connection/Reconnection:

- **Managed Mode**: Principal sends a resource resync request to ensure agent has latest Applications
- **Autonomous Mode**: Principal requests updates from agent to refresh its view

#### On Principal Restart:

- **Managed Mode**: Principal re-sends all Applications to connected agents
- **Autonomous Mode**: Principal requests a full sync from each autonomous agent

## Best Practices

### For Managed Agents

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

### For Autonomous Agents

1. **Project Management**: Be mindful of project names as they may be prefixed on the principal:

```yaml
   spec:
     project: microservices  # Becomes "agent-name-microservices" on principal
```

2. **Local Ownership**: Manage Applications entirely on the agent cluster

3. **Principal Monitoring**: Use the principal for centralized visibility across all autonomous agents

## Troubleshooting

### Application Not Appearing on Agent (Managed Mode)

1. **Check Namespace**: Verify the Application is created in the correct namespace on the principal
2. **Verify Agent Connection**: Ensure the agent is connected and the namespace name matches the agent name
3. **Review Logs**: Check principal logs for distribution events and agent logs for reception
4. **Check Source UID**: Look for source UID annotations to verify proper synchronization

### Application Not Appearing on Principal (Autonomous Mode)

1. **Check Agent Mode**: Ensure the agent is running in autonomous mode
2. **Verify Creation**: Confirm the Application was created on the agent cluster  
3. **Check Connectivity**: Ensure the agent can communicate with the principal
4. **Review Project Names**: Look for prefixed project names on the principal

### Status Not Updating

1. **Check Agent Health**: Verify the agent is running and connected
2. **Review Application Controller**: Ensure Argo CD's application-controller is running on the agent
3. **Inspect Annotations**: Check for proper source UID annotations
4. **Monitor Network**: Verify stable network connectivity between agent and principal

### Sync Conflicts

1. **Source UID Mismatch**: 

   - Usually resolved automatically by recreating the Application
   - Check logs for conflict resolution messages

2. **Cache Issues** (Managed Mode):

   - Agent may revert unexpected changes
   - Review Application cache logs on the agent

3. **Manual Intervention Required**:

   - Delete and recreate the Application if automatic resolution fails
   - Ensure the principal has the desired specification

## Monitoring and Observability

### Key Metrics to Monitor

- **Application Creation/Update/Delete Events**: Track synchronization activity
- **Status Update Frequency**: Monitor how often agents report status changes
- **Sync Errors**: Watch for failed synchronization attempts
- **Cache Hit/Miss Rates**: For managed agents, monitor cache effectiveness

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

## Skip Sync Label

The skip sync label allows you to prevent specific Applications from being synchronized between the principal and agents. This is useful when you want to create Applications that should only exist on one side of the synchronization.

### Label Details

- **Label Key**: `argocd-agent.argoproj-labs.io/ignore-sync`
- **Label Value**: `"true"` (must be the exact string "true", case-sensitive)
- **Scope**: Works for both managed and autonomous agent modes

### Usage Examples

#### Preventing Application Sync to Agent (Managed Mode)

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

#### Preventing Application Sync to Principal (Autonomous Mode)

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: agent-only-app
  namespace: argocd
  labels:
    argocd-agent.argoproj-labs.io/ignore-sync: "true"  # Skip sync to principal
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

This Application will remain only on the autonomous agent cluster and will not be synchronized back to the principal.

### Important Notes

1. **Case Sensitivity**: The label value must be exactly `"true"` (lowercase). Values like `"TRUE"`, `"True"`, `"false"`, or empty strings will **not** trigger the skip sync behavior.

2. **Label Removal**: If you remove the skip sync label from an existing Application, it will begin synchronizing according to the normal rules for your agent mode.

3. **Namespace Rules Still Apply**: The skip sync label doesn't override namespace-based filtering. Applications must still be in allowed namespaces to be processed.

### Use Cases

- **Principal-Only Applications**: Applications that manage the control plane infrastructure itself
- **Agent-Only Applications**: Local utilities or monitoring applications specific to a workload cluster
- **Temporary Isolation**: Temporarily preventing sync during maintenance or testing
- **Staged Rollouts**: Controlling which Applications are synchronized during gradual agent deployments

## Security Considerations

### Access Control

- **Managed Mode**: Principal controls all Application specifications; implement RBAC on the principal
- **Autonomous Mode**: Agents have full control; implement proper RBAC on each agent cluster
- **Network Security**: Ensure encrypted communication channels between principal and agents

### Isolation

- **Namespace Isolation**: Each agent operates in its own namespace on the principal
- **Agent Authentication**: Proper authentication prevents unauthorized agents from connecting
- **Resource Limits**: Consider implementing resource quotas per agent namespace

### Audit and Compliance

- **Change Tracking**: All Application changes are logged and auditable
- **Source Tracking**: Source UID annotations provide clear provenance
- **Access Logs**: Monitor who creates/modifies Applications on the principal 