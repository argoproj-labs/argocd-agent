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
    namespace: "guestbook"
    server: "*"
  sourceRepos:
  - "*"
```

When this AppProject is created on the principal, it will be automatically distributed to all connected managed agents whose names match the `agent-*` pattern.

### Agent-Specific Transformation

When an AppProject is sent to an agent, it undergoes transformation to make it agent-specific:

1. **Destinations**: Only destinations matching the agent are kept (using glob pattern matching), and they're transformed to point to the local cluster:

```yaml
   destinations:
   - name: "in-cluster"
     server: "https://kubernetes.default.svc"
     namespace: "guestbook"  # Preserves original namespace restrictions
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
  # Can also have multiple destinations - all will be transformed
  - name: "another-destination"
    namespace: "specific-ns"
    server: "https://kubernetes.default.svc"
  sourceNamespaces:
  - argocd
  sourceRepos:
  - "*"
```

When this AppProject is created on an autonomous agent named `agent-production`, it will be transformed and appear on the principal as:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: agent-production-my-project  # Prefixed with agent name
  namespace: argocd                  # Placed in Argo CD namespace on principal
spec:
  destinations:
  - name: agent-production           # All destinations point to agent
    namespace: "*"
    server: "*"
  - name: agent-production
    namespace: "specific-ns"         # Original namespace restrictions preserved
    server: "*"
  sourceNamespaces:
  - agent-production                 # Agent's namespace on principal
  sourceRepos:
  - "*"
```

### Principal-Side Transformation

When an AppProject is received from an autonomous agent, the principal applies transformations:

!!! note
    AppProjects from autonomous agents on the control plane are not used for reconciliation (there is no app controller running on the principal for these projects). Instead, they allow the Argo CD API (and UI, CLI) to determine which operations are valid to be performed on Applications that reference these AppProjects.

!!! warning "Important"
    AppProjects that are synced from autonomous agents should not be used by other Applications outside of that agent, as they may change unpredictably when the autonomous agent modifies its local AppProject configuration.

1. **Name Prefixing**: The project name is prefixed with the agent name to avoid conflicts:

```
   Original: my-project
   On Principal: agent-production-my-project
```

2. **Source Namespaces**: Transformed to allow Applications from the agent's namespace on the principal:

```yaml
   sourceNamespaces:
   - agent-production  # The agent's namespace on the principal
```

3. **Destinations**: All destinations are transformed to point to the agent cluster:

```yaml
   destinations:
   - name: agent-production  # The agent name
     server: "*"
     namespace: "*"  # Preserves original namespace restrictions
```

4. **Namespace Mapping**: The project is placed in the Argo CD namespace on the principal (same as where other AppProjects reside)

## Key Transformation Differences

The transformation logic differs significantly between managed and autonomous agents:

### Managed Agents (Principal → Agent)
- **Direction**: AppProject flows from principal to agent
- **Selection**: Uses glob pattern matching on `sourceNamespaces` and `destinations` to determine which agents receive the project
- **Destinations**: Filtered to only include destinations matching the agent, then transformed to `in-cluster`
- **Source Namespaces**: Replaced with the single agent namespace
- **Name**: Remains unchanged

### Autonomous Agents (Agent → Principal)
- **Direction**: AppProject flows from agent to principal  
- **Selection**: All AppProjects created on autonomous agents are synchronized
- **Destinations**: All destinations are transformed to point to the agent cluster (name = agent name, server = "*")
- **Source Namespaces**: Replaced with the agent's namespace on the principal
- **Name**: Prefixed with agent name to avoid conflicts

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
