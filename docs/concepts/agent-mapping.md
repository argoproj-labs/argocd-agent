# Agent Mapping Modes

argocd-agent supports two modes for mapping applications to agents: **namespace-based mapping** (default) and **destination-based mapping**. This document explains both approaches, their trade-offs, and when to use each.

!!! important "Managed Mode Only"
    Destination-based mapping only applies to **managed agents**. Autonomous agents do not require this feature because the agent is the source of truth and controls where applications are stored locally. On the principal, autonomous apps are always stored in the `{agentName}` namespace for clear ownership.

!!! tip "Coming from traditional Argo CD?"
    If you've used Argo CD before, destination-based mapping uses the same model. It routes on `spec.destination.name`, the field traditional Argo CD has always used to target a cluster added with `argocd cluster add`.

## Overview

When an Application is created on the control plane (principal), argocd-agent needs to determine which agent should manage it. The mapping mode controls how this routing decision is made.

| Feature                  | Namespace-Based Mapping                  | Destination-Based Mapping              |
|--------------------------|------------------------------------------|----------------------------------------|
| Routing mechanism        | Application namespace                    | `spec.destination.name` on principal   |
| App namespaces per agent | Single namespace                         | Multiple namespaces                    |
| Multi-tenancy            | Limited                                  | Full support                           |
| Configuration            | Default (no config)                      | Requires flag                          |
| App namespace on agent   | Agent's namespace (typically `argocd`)   | Preserves original namespace           |
| Supported agent modes    | Managed and Autonomous                   | Managed only                           |
| Familiar to Argo CD users | argocd-agent specific concept            | Same `destination.name` targeting as traditional Argo CD |

## Namespace-Based Mapping (Default)

In namespace-based mapping, the **namespace of the Application resource** determines which agent handles it. This is the default behavior and requires no additional configuration.

### How It Works

1. **Application Creation**: When you create an Application on the principal, place it in a namespace that matches the target agent's name.

2. **Routing**: The principal uses the Application's namespace to route events to the correct agent.

3. **Agent Processing**: The agent receives the Application and creates it in its configured namespace (typically `argocd`).

For detailed namespace configuration (access control, auto-creation, best practices), see [Namespaces Configuration](../configuration/namespaces.md).

### Example

```yaml
# Application targeting agent-managed
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: guestbook
  namespace: agent-managed  # This determines the target agent
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    path: guestbook
  destination:
    server: https://kubernetes.default.svc  # Ignored for routing
    namespace: guestbook
```

The diagram below shows namespace-based mapping with two agents. Each agent has a dedicated namespace on the control plane (`agent-prod`, `agent-staging`), and applications placed in those namespaces are synced to the corresponding agent.

```mermaid
graph TB
    subgraph control["Control Plane Cluster"]
        principal[Principal]
        argocd_ns["Namespace: argocd<br/>(Principal's namespace)"]
        agent1_ns["Namespace: agent-prod<br/>(Agent's apps)"]
        agent2_ns["Namespace: agent-staging<br/>(Agent's apps)"]
    end
    
    subgraph workload1["Workload Cluster 1"]
        agent1[Agent: agent-prod]
    end
    
    subgraph workload2["Workload Cluster 2"]
        agent2[Agent: agent-staging]
    end
    
    principal --> agent1_ns
    principal --> agent2_ns
    agent1 -.->|syncs from| agent1_ns
    agent2 -.->|syncs from| agent2_ns
```

### Limitations

1. **Single Agent Per Namespace**: Each namespace on the principal can only target one agent. You cannot have applications in the same namespace targeting different agents.

2. **Namespace Management Overhead**: Requires creating and managing a namespace for each agent on the principal cluster.

3. **Limited Multi-Tenancy**: Teams sharing an agent must either share a namespace or use separate agents.

4. **App Namespace Rewriting**: Applications are always stored in the agent's configured namespace (typically `argocd`), regardless of their original namespace on the principal.

## Destination-Based Mapping

With Destination-based mapping, the principal uses the Application's `spec.destination.name` field to determine the target agent. Routing only depends on this field, not on the Application's namespace, so no special namespace configuration is required to use this mode.

### How It Works

1. **Application Creation**: Create Applications in the `argocd` namespace by default. Other namespaces are optional and require Argo CD's apps-in-any-namespace configuration. Set `spec.destination.name` to the target agent's name.

2. **Routing**: The principal maintains a lookup table (`appToAgent`) mapping applications to agents based on `destination.name`.

3. **Agent Processing**: The agent creates the Application in a namespace matching the original namespace from the principal (not forced to the agent's namespace).

The diagram below shows the simplest case. Unlike namespace-based mapping, a single namespace is not tied to a single agent: `App1` and `App2` both live in `argocd` but target different agents using `spec.destination.name`. This is the same targeting model traditional Argo CD has always used.

```mermaid
graph TB
    subgraph control["Control Plane Cluster"]
        principal[Principal]
        argocd_ns["Namespace: argocd"]
        app2["App2<br/>destination.name: agent-prod"]
        app1["App1<br/>destination.name: agent-staging"]
    end

    subgraph workload2["Workload Cluster 2"]
        agent2[Agent: agent-prod]
    end

    subgraph workload1["Workload Cluster 1"]
        agent1[Agent: agent-staging]
    end

    principal --> argocd_ns
    argocd_ns --> app2
    argocd_ns --> app1
    app2 -.->|"routed by destination.name"| agent2
    app1 -.->|"routed by destination.name"| agent1
```

Namespace isolation is an optional extension, not a requirement. If you also want per-team namespaces, destination-based mapping supports that too, matching Argo CD's apps-in-any-namespace feature: multiple namespaces can route to the same agent, and each namespace is preserved on the agent side. This requires enabling apps-in-any-namespace (`application.namespaces`) for those namespaces. The diagram below shows two teams (`team-a`, `team-b`) both targeting `agent-prod`:

```mermaid
graph TB
    subgraph control["Control Plane Cluster"]
        principal[Principal]
        argocd_ns["Namespace: argocd<br/>(Principal's namespace)"]
        team_a_ns["Namespace: team-a<br/>Apps with destination.name: agent-prod"]
        team_b_ns["Namespace: team-b<br/>Apps with destination.name: agent-prod"]
    end
    
    subgraph workload["Workload Cluster"]
        agent[Agent: agent-prod]
        agent_team_a["Namespace: team-a<br/>(preserved)"]
        agent_team_b["Namespace: team-b<br/>(preserved)"]
    end
    
    principal --> team_a_ns
    principal --> team_b_ns
    team_a_ns -.->|"routed by destination.name"| agent
    team_b_ns -.->|"routed by destination.name"| agent
    agent --> agent_team_a
    agent --> agent_team_b
```

### Configuration

Enable destination-based mapping on both the principal and agent:

**Principal:**

```bash
# Via flag
argocd-agent principal --destination-based-mapping

# Via environment variable
ARGOCD_PRINCIPAL_DESTINATION_BASED_MAPPING=true
```

**Agent:**

```bash
# Via flag
argocd-agent agent --destination-based-mapping --create-namespace

# Via environment variables
ARGOCD_AGENT_DESTINATION_BASED_MAPPING=true
ARGOCD_AGENT_CREATE_NAMESPACE=true
```

!!! note "Create Namespace"
    With destination-based mapping, the agent creates the apps in the same namespace as the principal.
    Users can manually create this namespace on the agent or use the `--create-namespace` flag to allow the agent to create namespaces automatically when applications target namespaces that don't exist.

!!! note "Allowed Namespaces"
    With destination-based mapping, applications can exist in any namespace. Both principal and agent have `--allowed-namespaces` flags to control which namespaces they will manage. The flag supports glob patterns for flexible namespace matching.

!!! note "RBAC Permissions"
    The default installation grants cluster-wide Application permissions. For tighter security, use RoleBindings per namespace instead of ClusterRoleBinding.

### Example

```yaml
# Application defined on principal targeting agent-managed via destination.name
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: guestbook
  namespace: team-alpha  # Can be any namespace
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    path: guestbook
  destination:
    name: agent-managed  # This determines the target agent
    namespace: guestbook
  syncPolicy:
    syncOptions:
      - CreateNamespace=true
```

### Benefits

1. **Multi-Tenancy**: Multiple teams can have applications in different namespaces all targeting the same agent.

2. **Namespace Preservation**: Applications retain their original namespace on the agent, enabling better isolation and RBAC.

3. **Flexible Organization**: Applications can be organized by team, environment, or any other criteria without coupling to agents.

4. **Apps-in-Any-Namespace**: Works seamlessly with ArgoCD's "apps-in-any-namespace" feature.

## AppProject Configuration

If your Applications stay in the `argocd` namespace, no AppProject changes are needed. `sourceNamespaces` is only required for the optional namespace isolation described above, to let the AppProject on the agent allow Applications in those other namespaces:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: default
  namespace: argocd
spec:
  sourceNamespaces:
    - <namespaces>  # Only needed for Applications outside the argocd namespace
  # ... other configuration
```

Unlike namespace-based mapping, where the `sourceNamespaces` field is removed, this field is preserved when using destination-based mapping, so it can list any additional namespaces you choose to allow.

## Migration Guide

### From Namespace-Based to Destination-Based

!!! warning "Breaking Change"
    Switching mapping modes changes how applications are routed and stored. Plan for a maintenance window and test thoroughly in a non-production environment first.

#### What Happens During Migration

When migrating, be aware of these changes:

| Aspect | Before (Namespace-Based) | After (Destination-Based) |
|--------|-------------------------|--------------------------|
| App location on agent | `argocd/app-name` | `original-namespace/app-name` |
| Routing key | `app.metadata.namespace` | `app.spec.destination.name` |
| Redis key namespace | Agent name (e.g., `agent-managed_app`) | App namespace (e.g., `team-alpha_app`) |

#### Migration Issues to Expect

1. **Duplicate Applications**: Existing apps in `argocd` namespace on the agent will be orphaned. New copies will be created in the original namespace.

2. **Redis Cache Misses**: Key format changes will cause cache misses until ArgoCD refreshes.

3. **Namespace Creation**: Target namespaces may not exist on the agent cluster.

#### Recommended Migration Steps

1. **Backup**: Export all Application manifests.

2. **Update AppProjects**: Ensure `sourceNamespaces` includes all namespaces where apps will be created (or set to `["*"]`).

3. **Update ALL Applications FIRST**: Add `spec.destination.name` to every application:
   ```yaml
   spec:
     destination:
       name: agent-managed  # Must match agent name
       namespace: my-app-namespace
   ```

4. **Delete apps from agent** (optional but recommended): This ensures clean recreation in new namespaces.

5. **Enable on Principal**: Add `--destination-based-mapping` flag and restart.

6. **Enable on Agent**: Add `--destination-based-mapping` and `--create-namespace` flags and restart.

7. **Verify**: Check apps appear in correct namespaces on the agent.

8. **Cleanup**: Delete orphaned apps from `argocd` namespace on agent if step 4 was skipped.

## Choosing the Right Mode

### Use Namespace-Based Mapping When:

- You have a simple setup with one application namespace per agent
- You don't need multi-tenant isolation
- You want the simplest possible configuration
- Backward compatibility with existing deployments is required

### Use Destination-Based Mapping When:

- You are using **managed agents** (not autonomous)
- Multiple teams share agents but need namespace isolation
- You want applications organized by team/project rather than by agent
- You need the apps-in-any-namespace feature
- You're setting up a new deployment with multi-tenancy requirements
- You're already familiar with Argo CD's traditional `destination.name`/cluster-secret model and want to reuse that mental model directly

## Troubleshooting

### Application Not Appearing on Agent

1. **Namespace-Based**: Verify the Application namespace matches the agent name exactly.

2. **Destination-Based**: Verify `spec.destination.name` matches the agent name and both principal and agent have destination-based mapping enabled.

### Redis Cache Misses

If you see "error getting cached app managed resources" in the ArgoCD UI:

1. **Check Mapping Mode Consistency**: Ensure both principal and agent use the same mapping mode.

### Application Stuck in Wrong Namespace

If applications appear in the agent's namespace (e.g., `argocd`) when they should be in a custom namespace:

1. Verify destination-based mapping is enabled on the agent.
2. Check that the agent was restarted after configuration change.
3. Verify if the application namespace exists on the managed cluster.

## Related Documentation

- [Agent Modes (Managed vs Autonomous)](./agent-modes/index.md)
- [Namespaces Configuration](../configuration/namespaces.md)
- [Architecture Overview](./architecture.md)
