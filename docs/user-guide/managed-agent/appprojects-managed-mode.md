# AppProject synchronization (managed agents)

This guide explains how Argo CD `AppProjects` are synchronized between the principal (control plane) and **managed** agents.

For **autonomous** agents, see [AppProject synchronization (autonomous agents)](../autonomous-agent/appprojects-autonomous-mode.md). For how modes differ conceptually, see [Agent modes](../../concepts/agent-modes.md).

## Overview

AppProjects in argocd-agent work differently from standard Argo CD deployments. While Applications can be mapped to agents using namespaces, AppProjects require a different synchronization strategy due to their traditional placement in the Argo CD installation namespace.

With managed agents, AppProjects are created on the principal and distributed to agents.

!!! tip "Choosing a Mapping Mode"
    If you are unsure which mode to use, see [Agent Mapping Modes](./agent-mapping.md) for a detailed comparison.

## Managed Agent Mode

### Creating AppProjects

In managed mode, AppProjects must be created on the **principal cluster** (control plane). The principal determines which agents should receive an AppProject by examining two key fields:

1. **`.spec.sourceNamespaces`**: Defines which namespaces can contain Applications using this project
2. **`.spec.destinations`**: Defines which clusters/namespaces Applications can deploy to

### Distribution Logic

The principal distributes an AppProject to a managed agent based on the active mapping mode.
Glob pattern matching is used throughout, so wildcards like `agent-*` are supported.

**Namespace-based mapping** (default): the agent name must match **both**:

1. A pattern in `.spec.destinations[].name` (or via server URL `?agentName=` param)
2. A pattern in `.spec.sourceNamespaces`

**Destination-based mapping**: the agent name must match:

1. A pattern in `.spec.destinations[].name` (or via server URL `?agentName=` param)

`.spec.sourceNamespaces` is not consulted for routing in this mode — it is preserved on the AppProject sent to the agent and controls where Applications may live on the workload cluster.

In both modes, a destination deny pattern (`!name`) matching the agent causes the AppProject to be withheld from that agent. See [Agent Mapping Modes](./agent-mapping.md) for a detailed comparison.

### Example: Creating an AppProject for Managed Agents

**Namespace-based mapping** (both fields required for routing):

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: my-project
  namespace: argocd
spec:
  # This project will be distributed to agents matching "agent-*" pattern
  sourceNamespaces:
  - agent-*          # agent name must match here (routing)
  destinations:
  - name: agent-*    # and here
    namespace: "guestbook"
    server: "*"
  sourceRepos:
  - "*"
```

**Destination-based mapping** (only destinations required for routing):

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: my-project
  namespace: argocd
spec:
  destinations:
  - name: agent-*    # agent name must match here
    namespace: "guestbook"
    server: "*"
  sourceNamespaces:
  - "*"              # controls app namespaces on the workload cluster, not routing
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

2. **Source Namespaces**:
    - **Namespace-based mapping**: removed from the AppProject sent to the agent, since it was only used on the principal for routing.
    - **Destination-based mapping**: preserved on the AppProject sent to the agent, where it controls which namespaces Applications may be created in on the workload cluster.

3. **Roles**: Removed since they're not relevant on the workload cluster

### Lifecycle Management

- **Creation**: When you create an AppProject on the principal, it's automatically distributed to matching agents
- **Updates**: Changes to AppProjects on the principal are propagated to affected agents
- **Deletion**: Deleting an AppProject on the principal removes it from all agents
- **Agent Connection**: When an agent connects, it receives all AppProjects that should be synchronized to it


## Best Practices

1. **Use Descriptive Patterns**: Use clear glob patterns to target the right agents. In namespace-based mapping both `sourceNamespaces` and `destinations` must match; in destination-based mapping only `destinations` is used for routing:
```yaml
   # namespace-based mapping
   sourceNamespaces:
   - "production-*"
   destinations:
   - name: "production-*"

   # destination-based mapping (sourceNamespaces controls app placement, not routing)
   destinations:
   - name: "production-*"
   sourceNamespaces:
   - "*"
```

2. **Test Connectivity**: Ensure agents are connected before creating AppProjects, or they'll receive them upon next connection

3. **Monitor Distribution**: Check agent logs to verify AppProject distribution is working correctly

## Troubleshooting

### AppProject Not Appearing on Agent

1. **Check Agent Mode**: Ensure the agent is in managed mode
2. **Verify Patterns**: Confirm the agent name matches patterns in `destinations`. In namespace-based mapping it must also match `sourceNamespaces`
3. **Check Connectivity**: Verify the agent is connected to the principal
4. **Review Logs**: Check principal and agent logs for synchronization errors

### Pattern Matching Issues

1. **Test Patterns**: Use tools like `fnmatch` to test glob patterns
2. **Check Case Sensitivity**: Ensure agent names match the expected case
3. **Verify Wildcards**: Confirm wildcard patterns are correctly specified

## Ignore Sync Label

AppProjects can be labeled with `argocd-agent.argoproj-labs.io/ignore-sync: "true"` to keep them on the principal only, overriding the normal distribution logic so the project is not sent to any agent regardless of matching `sourceNamespaces`/`destinations` patterns.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: principal-only-project
  namespace: argocd
  labels:
    argocd-agent.argoproj-labs.io/ignore-sync: "true"  # Skip sync to agents
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

See [Ignore sync label](../ignore-sync.md) for the full label reference, including behavior for Applications and repository secrets.

## Security Considerations

- **Managed Mode**: Only the principal can create AppProjects, maintaining central control
