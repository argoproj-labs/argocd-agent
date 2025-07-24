# AppProject Synchronization

This document explains how Argo CD `AppProjects` are synchronized between the principal (control plane) and agents (workload clusters), covering both managed and autonomous agent modes.

## Overview

AppProjects in argocd-agent work differently from standard Argo CD deployments. While Applications can be mapped to agents using namespaces, AppProjects require a different synchronization strategy due to their traditional placement in the Argo CD installation namespace.

The synchronization mechanism varies depending on the agent mode:

- **Managed agents**: AppProjects are created on the principal and distributed to agents
- **Autonomous agents**: AppProjects are created on the agent and synchronized back to the principal

## Managed Agent Mode

### Creating AppProjects

In managed mode, AppProjects must be created on the **principal cluster** (control plane). The principal determines which agents should receive an AppProject by examining two key fields:

1. **`.spec.sourceNamespaces`**: Defines which namespaces can contain Applications using this project
2. **`.spec.destinations`**: Defines which clusters/namespaces Applications can deploy to

### Distribution Logic

The principal distributes an AppProject to a managed agent when **both** conditions are met:

1. The agent name matches one of the patterns in `.spec.destinations[].name`
2. The agent name matches one of the patterns in `.spec.sourceNamespaces`

This uses glob pattern matching, so wildcards like `agent-*` are supported.

### Example: Creating an AppProject for Managed Agents

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: my-project
  namespace: argocd
spec:
  # This project will be distributed to agents matching "agent-*" pattern
  sourceNamespaces:
  - agent-*
  destinations:
  - name: agent-*
    namespace: "*"
    server: "*"
  sourceRepos:
  - "*"
```

When this AppProject is created on the principal, it will be automatically distributed to all connected managed agents whose names match the `agent-*` pattern.

### Agent-Specific Transformation

When an AppProject is sent to an agent, it undergoes transformation to make it agent-specific:

1. **Destinations**: Only destinations matching the agent are kept, and they're transformed to point to the local cluster:

```yaml
   destinations:
   - name: "in-cluster"
     server: "https://kubernetes.default.svc"
     namespace: "*"
```

2. **Source Namespaces**: Limited to only the agent's namespace:

```yaml
   sourceNamespaces:
   - agent-production  # The specific agent namespace
```

3. **Roles**: Removed since they're not relevant on the workload cluster

### Lifecycle Management

- **Creation**: When you create an AppProject on the principal, it's automatically distributed to matching agents
- **Updates**: Changes to AppProjects on the principal are propagated to affected agents
- **Deletion**: Deleting an AppProject on the principal removes it from all agents
- **Agent Connection**: When an agent connects, it receives all AppProjects that should be synchronized to it

## Autonomous Agent Mode

### Creating AppProjects

In autonomous mode, AppProjects are created directly on the **agent cluster**. The agent then synchronizes these AppProjects back to the principal.

### Example: Creating an AppProject on an Autonomous Agent

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: my-project
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

### Principal-Side Transformation

When an AppProject is received from an autonomous agent, the principal applies transformations:

1. **Name Prefixing**: The project name is prefixed with the agent name to avoid conflicts:

```
   Original: my-project
   On Principal: agent-production-my-project
```

2. **Namespace Mapping**: The project is placed in the agent's corresponding namespace on the principal

### Lifecycle Management

- **Creation**: Creating an AppProject on the agent automatically creates it on the principal (with prefixed name)
- **Updates**: Changes to AppProjects on the agent are propagated to the principal
- **Deletion**: Deleting an AppProject on the agent removes it from the principal
- **Conflict Resolution**: The prefixed naming prevents conflicts between AppProjects from different autonomous agents

## Best Practices

### For Managed Agents

1. **Use Descriptive Patterns**: Use clear glob patterns in `sourceNamespaces` and `destinations` to target the right agents:

```yaml
   sourceNamespaces:
   - "production-*"
   - "staging-*"
   destinations:
   - name: "production-*"
   - name: "staging-*"
```

2. **Test Connectivity**: Ensure agents are connected before creating AppProjects, or they'll receive them upon next connection

3. **Monitor Distribution**: Check agent logs to verify AppProject distribution is working correctly

### For Autonomous Agents

1. **Use Meaningful Names**: Choose AppProject names that make sense when prefixed with the agent name

2. **Plan for Conflicts**: Remember that the principal will see `{agent-name}-{project-name}`, so plan accordingly

3. **Local Management**: Only create AppProjects that are specific to the autonomous agent's workload

## Troubleshooting

### AppProject Not Appearing on Agent

1. **Check Agent Mode**: Ensure the agent is in managed mode
2. **Verify Patterns**: Confirm the agent name matches patterns in `sourceNamespaces` and `destinations`
3. **Check Connectivity**: Verify the agent is connected to the principal
4. **Review Logs**: Check principal and agent logs for synchronization errors

### AppProject Not Appearing on Principal

1. **Check Agent Mode**: Ensure the agent is in autonomous mode
2. **Verify Creation**: Confirm the AppProject was created on the agent cluster
3. **Check Naming**: Look for the prefixed name on the principal (`{agent-name}-{project-name}`)
4. **Review Connectivity**: Ensure the agent can communicate with the principal

### Pattern Matching Issues

1. **Test Patterns**: Use tools like `fnmatch` to test glob patterns
2. **Check Case Sensitivity**: Ensure agent names match the expected case
3. **Verify Wildcards**: Confirm wildcard patterns are correctly specified

## Security Considerations

- **Managed Mode**: Only the principal can create AppProjects, maintaining central control
- **Autonomous Mode**: Agents can create AppProjects, so ensure proper RBAC on agent clusters

## Monitoring and Observability

- **Principal Logs**: Monitor AppProject distribution events
- **Agent Logs**: Watch for AppProject creation/update/deletion events
- **Metrics**: Use available metrics to track AppProject synchronization success/failure rates
- **Health Checks**: Implement monitoring to detect synchronization issues
