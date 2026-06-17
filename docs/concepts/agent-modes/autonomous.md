# Autonomous mode

## Overview of autonomous mode

In *autonomous mode*, the workload cluster is wholly responsible for maintaining its own configuration. As opposed to the [managed mode](./managed.md), all configuration is first created on the workload cluster. The agent on this workload cluster will observe creation, modification and deletion of configuration and transmit them to the principal on the control plane cluster.

The principal will then create, update or delete this configuration on the control plane cluster. Users can use the Argo CD UI, CLI or API to inspect the status of the configuration, but they are not able to modify the configuration spec or delete it. Sync and terminate operations can still be triggered from the control plane and will be forwarded to the agent.

Keep in mind that you will not be able to perform configuration changes on the control plane (i.e. through the Argo CD UI, CLI or API) to Applications that are governed by an agent running in *autonomous* configuration mode. This includes parametrization (e.g. Kustomize or Helm parameters) as well as annotations, labels, changing Git parameters, sync options and others. Such spec-level modifications are automatically reverted to match the agent's version.

## Architectural considerations

* Autonomous mode requires all components except argocd-server and argocd-dex to be running on a workload cluster. 
* In this mode, workload clusters are truly autonomous and own their configuration. The control plane serves primarily as an observation tool, though sync and terminate operations can still be triggered from it.
* Argo CD configuration management must be externalized (e.g. app-of-apps, all changes must go through Git)

## Why chose this mode

* Provides a more true to the philosophy GitOps experience with central observability
* The control plane cluster will neither be a single point of failure nor of compromise
* Support for app-of-apps and sourcing of configuration through Git

## Why not chose this mode

* No configuration management on the control plane cluster for autonomous agents
* Heavier on the workload cluster than managed mode setups
* Limited possibilities for advanced multi-cluster rollouts of Applications (for example, progressive syncs)