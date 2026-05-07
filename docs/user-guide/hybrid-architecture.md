# Hybrid Architecture: Running Agent alongside an existing Argo CD instance

This guide explains how to run argocd-agent (principal + agent) alongside a pre-existing Argo CD installation on the same hub cluster. This enables you to try out argocd-agent without replacing your existing setup, gradually migrate applications, or run both architectures long-term.

!!! tip "Who is this for?"
    This guide is for teams that already have a working Argo CD installation managing applications and want to adopt argocd-agent incrementally. If you are setting up argocd-agent from scratch, see the [Getting Started](../getting-started/index.md) guide instead.

## Overview

In a hybrid architecture, the traditional Argo CD components (server, app controller, repo-server, Redis) continue to operate on the hub cluster. The argocd-agent principal is installed alongside them. Both systems share the same Argo CD UI and API, but each manages a distinct set of applications:

- **Traditional apps**: managed by the hub app controller, deployed to spokes via direct cluster access
- **Agent apps**: managed by the principal, deployed to spokes via the agent pull model

The two systems coexist through isolation mechanisms on both sides: label selectors on the principal and agent, the `skip-reconcile` annotation on agent cluster secrets, and the `ignore-unmanaged-apps` flag as an additional safety net.

## Architecture

```mermaid
flowchart TB
    subgraph hub [Hub Cluster]
        ArgoServer["Argo CD Server"]
        AppController["App Controller"]
        RepoServer["Repo Server"]
        Redis["Redis"]
        Principal["Principal"]
        RedisProxy["Redis Proxy"]

        ArgoServer -->|all Redis traffic| RedisProxy
        RedisProxy -->|"non-agent keys (cluster|*, mfst|*, etc.)"| Redis
        RedisProxy -->|"agent keys (app|resources-tree|*, app|managed-resources|*)"| Principal
        AppController --> Redis
    end

    subgraph spoke [Spoke Cluster]
        Agent["Agent"]
        SpokeAppCtrl["App Controller"]
        SpokeRepo["Repo Server"]
        SpokeRedis["Redis"]
        SpokeAppCtrl --> SpokeRedis
    end

    Principal <-->|"gRPC (agent-initiated)"| Agent
    AppController -.->|"traditional apps (direct access)"| spoke
```

**Hub cluster** runs all existing Argo CD components unchanged, plus the principal and its Redis proxy. The Argo CD server is reconfigured to talk to the Redis proxy instead of Redis directly. The proxy transparently routes traffic: traditional app data goes to the Redis on the hub cluster, agent app data is forwarded to the appropriate agent via the principal.

**Spoke cluster** runs the agent, a local app controller, repo-server, and Redis. The agent initiates a gRPC connection to the principal. The local app controller reconciles applications that the principal pushes to the spoke.

## How Isolation Works

The following mechanisms prevent conflicts between the traditional setup and the agent setup.

### Label Selector

The principal and agent are configured with a label selector. They only watch and process Kubernetes resources (Applications, AppProjects, Repositories) that carry the matching label. The existing app controller is unaware of this label and continues to process all applications as before.

```yaml
# Principal configuration (argocd-agent-params ConfigMap)
principal.label-selector: "migrate=true"

# Agent configuration (argocd-agent-params ConfigMap on spoke)
agent.label-selector: "migrate=true"
```

The label selector is combined with a built-in exclusion of resources labeled `argocd-agent.argoproj-labs.io/ignore-sync=true`, using Kubernetes label selector AND semantics.

### skip-reconcile Annotation

When you create an agent using `argocd-agentctl agent create`, the resulting cluster secret is stamped with the annotation `argocd.argoproj.io/skip-reconcile: "true"`. This tells the hub app controller to skip reconciliation for all applications targeting that cluster.

This is the key mechanism that prevents the hub app controller from interfering with applications that have been migrated to the agent. The agent cluster secret can coexist with a traditional cluster secret for the same physical spoke cluster because they have different names and point to different endpoints (the agent secret points to the resource proxy).

!!! warning "Requires Argo CD v3.4+"
    The `skip-reconcile` annotation is only supported in Argo CD v3.4 and later. On older versions, the hub app controller will still attempt to reconcile applications targeting agent clusters, which will cause conflicts.

### Agent Label Selector

The agent also supports a label selector (`agent.label-selector`), which filters resources at the Kubernetes API level on the spoke cluster. When configured, the agent's informers only list and watch Applications, AppProjects, and Repository secrets that match the selector. Pre-existing apps on the spoke that lack the label are invisible to the agent.

```yaml
# Agent configuration (argocd-agent-params ConfigMap on spoke)
agent.label-selector: "migrate=true"
```

When the principal pushes an application to the spoke, it preserves the application's existing labels. Since only labeled apps are picked up by the principal in the first place, the spoke-side label selector naturally ensures the agent only processes apps that came through the principal.

### ignore-unmanaged-apps (Additional Safety Net)

As an additional safeguard, the `ignore-unmanaged-apps` flag tells the agent to skip any application that lacks the `argocd.argoproj.io/source-uid` annotation, which is automatically set by the agent when it creates an application locally on the spoke. This is an in-memory filter that runs after the label selector.

This flag is passed via the `--ignore-unmanaged-apps` CLI flag or the `ARGOCD_AGENT_IGNORE_UNMANAGED_APPS` environment variable on the agent Deployment:

```yaml
# Add to the agent Deployment's container env
- name: ARGOCD_AGENT_IGNORE_UNMANAGED_APPS
  value: "true"
```

This is useful as a defense-in-depth measure: if a pre-existing spoke app happens to carry the same label used for migration, the agent will still skip it because it lacks the `source-uid` annotation. Without this flag, the agent would treat such apps as managed, send status events to the principal for apps that don't exist there, and cause errors.

## Prerequisites

Before setting up the hybrid architecture, ensure:

- **Argo CD v3.4+** is installed on the hub cluster (required for `skip-reconcile`)
- The hub cluster has a working Argo CD installation with server, app controller, repo-server, and Redis
- Spoke clusters are network-accessible from the hub (for the traditional setup) and can initiate outbound connections to the hub (for the agent setup)
- You have `argocd-agentctl` installed for PKI management and agent creation

## Setup Guide

### Step 1: Install the Principal

Deploy the principal on the hub cluster alongside the existing Argo CD installation. Configure the label selector to ensure the principal only watches resources you explicitly label.

```yaml
# argocd-agent-params ConfigMap
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
  namespace: argocd
data:
  principal.label-selector: "migrate=true"
  principal.destination-based-mapping: "true"
  ..............
```

!!! note "Destination-based mapping"
    Enabling destination-based mapping is recommended for hybrid setups. It allows applications to reside in any namespace on the hub and routes to agents based on `spec.destination.name` rather than the application's namespace. This means your existing applications can stay in their current namespaces during migration.

### Step 2: Point the Argo CD Server to the Redis Proxy

The Redis proxy is a component of the principal that intercepts Redis traffic from the Argo CD server. It routes agent-related keys (resource trees, managed resources) to the correct agent and forwards everything else to the Redis on the hub cluster.

Update the Argo CD configuration to use the Redis proxy:

```bash
kubectl patch configmap argocd-cmd-params-cm -n argocd --type merge \
  -p '{"data":{"redis.server":"argocd-agent-redis-proxy:6379"}}'
```

After patching, restart the Argo CD server to pick up the change:

```bash
kubectl rollout restart deployment argocd-server -n argocd
```

!!! important
    This step is critical. Without it, the Argo CD UI cannot display resource trees or managed resources for agent-managed applications. The Redis proxy is transparent for traditional apps: their data continues to flow through to the Redis on the hub cluster.

### Step 3: Create the Agent

Use `argocd-agentctl` to create the agent on the hub. This creates a cluster secret with the `skip-reconcile` annotation and TLS credentials for the agent.

```bash
argocd-agentctl agent create agent-spoke-1 \
  --namespace argocd
```

This creates:

- A Kubernetes secret named `cluster-agent-spoke-1` in the `argocd` namespace
- The secret has the annotation `argocd.argoproj.io/skip-reconcile: "true"`
- The cluster's `server` URL points to the principal's resource proxy
- TLS client credentials for the agent to authenticate with the principal

You can verify the secret:

```bash
kubectl get secret cluster-agent-spoke-1 -n argocd \
  -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/skip-reconcile}'
# Output: true
```

### Step 4: Install the Agent on the Spoke

Deploy the agent, app controller, repo-server, and Redis on the spoke cluster. Configure the agent to connect to the principal and enable the same label selector and mapping mode.

```yaml
# argocd-agent-params ConfigMap on the spoke cluster
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
  namespace: argocd
data:
  agent.destination-based-mapping: "true"
  agent.create-namespace: "true"
  agent.allowed-namespaces: "*"
  agent.label-selector: "migrate=true"
  ......................
```

### Step 5: Verify Connectivity

Check that the agent has connected to the principal:

```bash
# On the hub, check principal logs for agent connection
kubectl logs deployment/argocd-agent-principal -n argocd | grep "agent-spoke-1"

# Verify the agent is connected
kubectl logs deployment/argocd-agent-agent -n argocd | grep "connected"
```

Verify that existing applications are unaffected:

```bash
# Traditional apps should still be Synced/Healthy
argocd app list
```

## Migrating Applications

Once the hybrid setup is running, you can migrate applications from the traditional setup to the agent-based setup. Migration is done per-application.

### Step 1: Prepare the AppProject

The AppProject must be available on the spoke cluster before migrating applications that reference it. Ensure the project's `destinations` and `sourceNamespaces` include the agent name, then add the migration label that matches the principal's label selector.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: my-project
  namespace: argocd
  labels:
    migrate: "true"    # Matches the principal's label selector
spec:
  sourceNamespaces:
    - "agent-spoke-1"  # Must match agent name
  destinations:
    - namespace: '*'
      name: agent-spoke-1   # Must match agent name
  sourceRepos:
    - '*'
```

### Step 2: Prepare Repository Secrets

If your application uses private repositories, ensure the repository secret is project-scoped and labeled. Only project-scoped repository secrets are distributed to agents.

```bash
kubectl label secret repo-my-private-repo -n argocd migrate=true
```

### Step 3: Migrate the Application

Migration involves two changes to the Application: updating the destination to point to the agent cluster, and adding the migration label.

**Update the destination** to point to the agent cluster name. Since the agent cluster secret has `skip-reconcile`, the hub app controller will stop reconciling this application:

```bash
kubectl patch application my-app -n argocd --type merge \
  -p '{"spec":{"destination":{"name":"agent-spoke-1","server":""}}}'
```

**Add the migration label** so the principal picks up the application:

```bash
kubectl label application my-app -n argocd migrate=true
```

The principal will now sync this application to the spoke agent. The spoke's local app controller will reconcile it and deploy the resources.

### Step 4: Verify

```bash
# Check app status in the UI or CLI
argocd app get my-app

# Verify the resource tree loads in the UI (goes through Redis proxy -> agent)
# Verify the app shows Synced/Healthy
```

## How the UI Works in Hybrid Mode

The Argo CD UI works seamlessly for both traditional and agent-managed applications. All applications appear in the same UI, and users interact with them the same way.

Behind the scenes, the Redis proxy handles the routing:

| Operation | Traditional Apps | Agent Apps |
| --------- | --------------- | ---------- |
| App list / status | Argo CD server reads from Redis (via proxy, forwarded to real Redis) | Argo CD server reads from Redis (via proxy, forwarded to real Redis) |
| Resource tree | `GET app\|resources-tree\|...` forwarded to real Redis | `GET app\|resources-tree\|...` routed to agent via principal |
| Managed resources | `GET app\|managed-resources\|...` forwarded to real Redis | `GET app\|managed-resources\|...` routed to agent via principal |
| Pod logs | Argo CD server connects to spoke directly | Routed through resource proxy to agent |
| Sync / Refresh | App controller on hub handles it | Principal forwards to agent |

## Other Topologies

While this guide focuses on the most common single-hub, multi-spoke topology, the hybrid approach applies to other patterns as well.

### Dual-Instance (Config vs Apps)

Some teams run two Argo CD instances: one for cluster configuration and one for applications, to segregate roles. With the agent architecture, this separation happens naturally:

- Cluster configuration can remain with the hub Argo CD instance
- Application delivery moves to agents on the spoke clusters

The agent architecture eliminates the need for a second Argo CD instance purely for role separation.

### Hub-Per-Environment

Teams that run separate Argo CD instances per environment (dev, staging, production) can consolidate to a single principal serving agents across all environments. Each environment gets its own agent, and AppProjects control which applications can target which agents.

### ApplicationSet-Centric

If you rely heavily on ApplicationSets for fleet management:

- The ApplicationSet controller stays on the hub cluster
- Generated Application CRs follow the normal migration flow (label + destination change)
- The ApplicationSet CR itself is **not** synced to agents; only the generated Applications are

This means you can continue using cluster generators, Git generators, and other ApplicationSet features. The generated applications are picked up by the principal when they match the label selector.

!!! note
    ApplicationSets work best with destination-based mapping, as one ApplicationSet can target multiple agents by varying `spec.destination.name` across generated applications.

## Known Issues and Limitations

### skip-reconcile Requires Argo CD v3.4+

The `argocd.argoproj.io/skip-reconcile` annotation is only supported in Argo CD v3.4 and later. On older versions, the hub app controller will still attempt to reconcile applications targeting agent clusters, leading to conflicts.

## Validation Checklist

After setting up the hybrid architecture, verify the following:

### Infrastructure

- [ ] Principal is running and logs show the configured label selector
- [ ] Agent is connected to the principal (check principal logs)
- [ ] Redis proxy is running (the `argocd-agent-redis-proxy` service has endpoints)
- [ ] `argocd-cmd-params-cm` has `redis.server` pointing to the Redis proxy
- [ ] Agent cluster secrets have `argocd.argoproj.io/skip-reconcile: "true"`
- [ ] Traditional cluster secrets do NOT have `skip-reconcile`

### Traditional Apps

- [ ] Existing apps without the migration label continue to sync normally
- [ ] The principal logs show NO activity for unlabeled apps
- [ ] Resource tree loads in the UI for traditional apps

### Agent Apps

- [ ] Labeled apps are picked up by the principal
- [ ] Apps are synced to the spoke and show Synced/Healthy
- [ ] Resource tree loads in the UI for agent apps
- [ ] Pod logs are accessible through the UI
- [ ] The hub app controller does NOT reconcile agent apps

### Coexistence

- [ ] Both traditional and agent apps can be synced simultaneously without interference
- [ ] Modifying a traditional app does not affect agent apps, and vice versa
- [ ] Adding the migration label moves an app to agent management
- [ ] Removing the migration label returns an app to traditional management
