# Managed mode

## Overview

In *managed mode*, the control plane cluster is responsible for maintaining the configuration of an agent. The agent receives all of its configuration (e.g. `Applications` and `AppProjects`) from the [principal](../components-terminology.md#principal). 

For example, to create a new Argo CD `Application` on the workload cluster, it must be created on the control plane cluster. The principal will observe the creation of a new `Application` and emit a creation event that the agent on the workload cluster will pick up. The agent then will create the `Application` in its local cluster. From there, it will be picked up by the Argo CD *application controller* for reconciliation. The agent will observe any changes to the `Application`'s status field and transmits them to the principal, which merges them into the leading copy of the `Application`.

Likewise, if an `Application` is to be deleted, it must be deleted on the control plane cluster. Once the principal observes the deletion event, it will emit a deletion event that the agent on the workload cluster will pick up. The agent then deletes the `Application` from its local cluster, and transmits the result back to the principal.

Similar procedures apply to modifications of an `Application` in this mode.

Changes to `Application` resources on the workload cluster that are not originating from the principal will be reverted.

## Architectural considerations

* The minimum requirement on any workload cluster in *managed* mode is to have an agent and the Argo CD *application-controller* installed
* The *application-controller* can be configured to use the *repository-server* and *redis-server* on either the control plane cluster, or on the local workload cluster.
* The Argo CD *applicationset-controller* must be running on the control plane cluster, if you intend to use `ApplicationSets`.
* If the Argo CD *application-controller* is configured to use the *redis-server* or the *repository-server* on the control plane cluster, the control plane cluster becomes a single point of failure (SPoF) for the workload cluster.

## Why chose this mode

* Provides the classical Argo CD experience
* Create and manage applications from the Argo CD UI, CLI or API
* It has the lowest footprint on the workload cluster
* Allows use of ApplicationSet generators that span over multiple clusters, such as cluster or cluster-decision generators

## Why not chose this mode

* Very limited support for the app-of-apps pattern
* In the case the control plane cluster is compromised, it may affect workload clusters in managed mode, too
* As noted [previously](#architectural-considerations), the control plane cluster might become a SPoF

## Pre-existing Applications

When deploying the agent to a cluster with existing Argo CD applications, those applications won't have the source UID annotation that the agent uses to track managed resources. By default, the agent logs errors during resync for these unmanaged applications.

To suppress these errors, enable the `--ignore-unmanaged-apps` flag:

```bash
argocd-agent agent --ignore-unmanaged-apps
```

Or via environment variable:

```bash
ARGOCD_AGENT_IGNORE_UNMANAGED_APPS=true
```

When enabled, applications without the source UID annotation are silently skipped during resync. This is useful for:

* Gradual migration from standalone Argo CD to agent-managed deployments
* Clusters with a mix of managed and unmanaged applications
* Avoiding noisy error logs in environments with pre-existing applications

See the [Agent Configuration Reference](../../configuration/reference/agent.md#ignore-unmanaged-apps) for more details.

