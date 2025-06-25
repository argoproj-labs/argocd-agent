# Autonomous mode

## Overview of autonomous mode

In *autonomous mode*, the workload cluster is wholly responsible for maintaining its own configuration. As opposed to the [managed mode](./managed.md), all configuration is first created on the workload cluster. The agent on this workload cluster will observe creation, modification and deletion of configuration and transmit them to the principal on the control plane cluster.

The principal will then create, update or delete this configuration on the control plane cluster. Users can use the Argo CD UI, CLI or API to inspect the status of the configuration, but they are not able to perform changes to the configuration or delete it.

Keep in mind that you will not be able to perform any changes whatsoever on the control plane (i.e. through the Argo CD UI, CLI or API) to Applications that are governed by an agent running in *autonomous* configuration mode. This includes parametrization (e.g. Kustomize or Helm parameters) as well as annotations, labels, changing Git parameters, sync options and others.

## Architectural considerations

* Autonomous mode requires all components except argocd-server and argocd-dex to be running on a workload cluster. 
* In this mode, workload clusters are truly autonomous and only report back to the control plane. The control plane is merely an observation tool for autonomous agents.
* Argo CD configuration management must be externalized (e.g. app-of-apps, all changes must go through Git)

## Why chose this mode

* Provides a more true to the philosophy GitOps experience with central observability
* The control plane cluster will neither be a single point of failure nor of compromise
* Support for app-of-apps and sourcing of configuration through Git

## Why not chose this mode

* No configuration management on the control plane cluster for autonomous agents
* Heavier on the workload cluster than managed mode setups
* Limited possibilities for advanced multi-cluster rollouts of Applications (for example, progressive syncs)