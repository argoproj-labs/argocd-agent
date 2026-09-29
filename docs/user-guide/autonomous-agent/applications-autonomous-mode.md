# Application Synchronization (autonomous agents)

This document covers how Argo CD `Applications` are synchronized between **autonomous** agents and the principal.

For **managed** agents, see [Application synchronization (managed agents)](../managed-agent/applications-managed-mode.md). For how managed and autonomous modes differ at a conceptual level, see [Agent modes](../../concepts/agent-modes.md).

## Overview

Application synchronization in argocd-agent maps Applications to agents using **namespaces** on the principal that correspond to each agent.

For autonomous agents, Applications are created on the agent and synchronized to the principal; the principal acts as a read-only mirror for specifications but can still perform sync, refresh, and resource actions.

## Autonomous agent mode

### Creating Applications

In autonomous mode, Applications are created directly on the **agent cluster**. The agent then synchronizes these Applications to the principal, where they appear in a dedicated namespace named after the agent.

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

For more information, refer to [Managing AppProjects](appprojects-autonomous-mode.md).

### Status Synchronization

In autonomous mode, the agent is the source of truth:

- **Agent → Principal**: Both spec and status changes
- **Principal → Agent**: Spec modifications are reverted; sync and terminate operations are forwarded

The principal serves as a centralized view of all Applications across autonomous agents but doesn't control their configuration.

### Lifecycle Management

- **Creation**: Create Applications on the agent; they automatically appear on the principal
- **Updates**: Modify Applications on the agent; changes are propagated to the principal
- **Deletion**: Delete Applications on the agent; they're automatically removed from the principal
- **Local Control**: The agent maintains full control over its Applications

## Best Practices

1. **Project Management**: Be mindful of project names as they may be prefixed on the principal:
```yaml
   spec:
     project: microservices  # Becomes "agent-name-microservices" on principal
```

2. **Local Ownership**: Manage Applications entirely on the agent cluster

3. **Principal Monitoring**: Use the principal for centralized visibility across all autonomous agents

## Troubleshooting

### Application Not Appearing on Principal

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

- **Source UID Mismatch**: 
    - Usually resolved automatically by recreating the Application
    - Check logs for conflict resolution messages
- **Manual Intervention Required**:
    - Delete and recreate the Application if automatic resolution fails
    - Ensure the agent has the desired specification

## Monitoring and Observability

### Key Metrics to Monitor

- **Application Creation/Update/Delete Events**: Track synchronization activity
- **Status Update Frequency**: Monitor how often agents report status changes
- **Sync Errors**: Watch for failed synchronization attempts

### Log Events to Watch

- **Principal Logs**:
    - Application distribution events
    - Status update processing
    - Resync operations

- **Agent Logs**:
    - Application creation/update events
    - Status reporting activities
    - Conflict resolution actions

### Health Checks

- **Principal**: Monitor Application informer sync status
- **Agent**: Verify Application backend is running and synced
- **Network**: Ensure stable gRPC connection between principal and agents

## Ignore Sync Label

Applications can be labeled with `argocd-agent.argoproj-labs.io/ignore-sync: "true"` to keep them on the autonomous agent only, exempting them from synchronization to the principal — for example, local utilities or monitoring Applications specific to a workload cluster.

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

See [Ignore sync label](../ignore-sync.md) for the full label reference, including behavior for AppProjects.

## Security Considerations

### Access Control

- **Autonomous Mode**: Agents have full control; implement proper RBAC on each agent cluster
- **Network Security**: Ensure encrypted communication channels between principal and agents.

### Isolation

- **Namespace Isolation**: Each agent operates in its own namespace on the principal
- **Agent Authentication**: Proper authentication prevents unauthorized agents from connecting
- **Resource Limits**: Consider implementing resource quotas per agent namespace

### Audit and Compliance

- **Change Tracking**: All Application changes are logged and auditable
- **Source Tracking**: Source UID annotations provide clear provenance
