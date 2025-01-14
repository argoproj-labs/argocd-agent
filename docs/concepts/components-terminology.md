# Components and terminology

This chapter introduces the reader to the components and terminology of *argocd-agent*.

## Control plane cluster

The *control plane* or *control plane cluster* is the heart of the architecture. It holds all configuration, is responsible for distributing the configuration among agents and hosts all central components used for observability and management. At the minimum, this cluster will host the *argocd-agent*'s [principal](#principal) component and the Argo CD API server, including the Argo CD web UI.

The control plane cluster itself will not perform reconciliation of Argo CD `Applications` to any cluster, including itself.

Typically, there exists only one control plane cluster in any given setup.

## Workload cluster

A *workload cluster* is a cluster that is the target for applications. In the classical Argo CD architecture, a workload cluster is one that you connect your Argo CD installation to, i.e. using `argocd cluster add` or its declarative equivalent. 

In the case of *argocd-agent*, the workload cluster will host at least the *argocd-agent*'s [agent](#agent) component and Argo CD's application controller for local reconciliation. In a nutshell, each workload cluster needs to have a low footprint version of Argo CD installed to it.

## Principal

The *principal* is a central component of *argocd-agent*. It runs on the control plane cluster, and its main tasks are to distribute configuration and receive status information from and to the [agents](#agent), respectively.

*Agents* need to be registered with the *principal*, and the *principal* will authenticate agents once they connect.

The *principal* provides additional services to the Argo CD API server, so that users will be able to view live resources and container logs from workload clusters.

The *principal* typically requires a limited set of privileges on the control plane cluster.

## Agent

The *agent* is a component that gets installed to each workload cluster. It will be configured to connect to a specific *principal*, from which it will receive all configuration and to which it will send status updates.

Depending on the features an *agent* should provide, it will require limited to extended set of privileges on the workload cluster.

Each *agent* can run in one of the following modes: *managed* or *autonomous*. For more information, refer to the chapter about [agent modes](./agent-modes/index.md).