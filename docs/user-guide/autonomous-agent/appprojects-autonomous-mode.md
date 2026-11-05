# AppProject Synchronization (autonomous agents)

This document explains how Argo CD `AppProjects` are synchronized between **autonomous** agents and the principal.

For **managed** agents, see [AppProject synchronization (managed agents)](../managed-agent/appprojects-managed-mode.md). For how modes differ conceptually, see [Agent modes](../../concepts/agent-modes.md).

## Overview

AppProjects in argocd-agent work differently from standard Argo CD deployments. While Applications can be mapped to agents using namespaces, AppProjects require a different synchronization strategy due to their traditional placement in the Argo CD installation namespace.

On autonomous agents, AppProjects are **created on the agent cluster** and **synchronized to the principal**, where they are transformed (including name prefixing) for UI/API visibility and validation.

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
  roles:
  - name: read-only
    policies:
    - "p, proj:my-project:read-only, applications, get, my-project/*, allow"
  # (...)
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
  roles:
  - name: read-only
    policies:
    - "p, proj:agent-production-my-project:read-only, applications, get, agent-production-my-project/agent-production/*, allow" # Since the project '.name' was prefixed with the agent name above, the agent logic performs the same translation here. We also update the 5th field, including updating the namespace to principal cluster namespace.
  # (...)
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

3. **Destinations**: All destinations are transformed to point at the agent cluster:
```yaml
   destinations:
   - name: agent-production  # The agent name
     server: "*"
     namespace: "*"  # Preserves original namespace restrictions
```

4. **Namespace Mapping**: The project is placed in the Argo CD namespace on the principal (same as where other AppProjects reside)

5. **Roles**: Role policies are translated so that RBAC references remain valid on the principal. For each policy line in `.spec.roles[].policies[]`:
   - The `proj:<project>:<role>` subject is updated to use the prefixed project name
   - The object field is transformed from `<project>/<app>` to `<project>/<agent-namespace>/<app>` (since Application-in-any-namespace is enabled on the principal for autonomous agents)
   - Only policies targeting supported resource types (`applications`, `applicationsets`, `logs`, `exec`) are transformed; others are preserved as-is
```
   # Original (on agent):
   p, proj:my-project:read-only, applications, get, my-project/*, allow
   # Transformed (on principal):
   p, proj:agent-production-my-project:read-only, applications, get, agent-production-my-project/agent-production/*, allow
```

### Lifecycle Management

- **Creation**: Creating an AppProject on the agent automatically creates it on the principal (with prefixed name)
- **Updates**: Changes to AppProjects on the agent are propagated to the principal
- **Deletion**: Deleting an AppProject on the agent removes it from the principal
- **Conflict Resolution**: The prefixed naming prevents conflicts between AppProjects from different autonomous agents

## Best Practices

1. **Use Meaningful Names**: Choose AppProject names that make sense when prefixed with the agent name

2. **Plan for Conflicts**: Remember that the principal will see `{agent-name}-{project-name}`, so plan accordingly

3. **Local Management**: Only create AppProjects that are specific to the autonomous agent's workload

## Troubleshooting

### AppProject Not Appearing on Principal

1. **Check Agent Mode**: Ensure the agent is in autonomous mode
2. **Verify Creation**: Confirm the AppProject was created on the agent cluster
3. **Check Naming**: Look for the prefixed name on the principal (`{agent-name}-{project-name}`)
4. **Review Connectivity**: Ensure the agent can communicate with the principal

### Pattern Matching Issues

1. **Test Patterns**: Use tools like `fnmatch` to test glob patterns
2. **Check Case Sensitivity**: Ensure agent names match the expected case
3. **Verify Wildcards**: Confirm wildcard patterns are correctly specified

## Ignore Sync Label

AppProjects can be labeled with `argocd-agent.argoproj-labs.io/ignore-sync: "true"` to keep them on the autonomous agent only, exempting them from synchronization back to the principal.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: agent-only-project
  namespace: argocd
  labels:
    argocd-agent.argoproj-labs.io/ignore-sync: "true"  # Skip sync to principal
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

This AppProject will remain only on the autonomous agent cluster and will not be synchronized back to the principal.

See [Ignore sync label](../ignore-sync.md) for the full label reference, including behavior for Applications and repository secrets.

## Security Considerations

- **Autonomous mode**: Agents can create AppProjects locally—enforce RBAC on each workload cluster.
