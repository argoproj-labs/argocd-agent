# Features

The `argocd-agent` project provides building blocks to outsource compute in multi-cluster Argo CD setups. The general idea is to install the argocd-application-controller on each managed cluster, while keeping the argocd-server (which includes the API and web UI) on a central control plane cluster. The locations of other components (such as, argocd-redis, argocd-repository-server and argocd-application-controller) vary depending on your needs and requirements. You can read more about this in the [architectural overview](../concepts/architecture.md) section of the docs.

## Implemented

The following features are available and ready to use with the most recent release of argocd-agent:

* Works with vanilla Argo CD
* Sync protocol based on gRPC and Cloudevents
* Synchronization of Application resources between principal and agents
* Basic synchronization of AppProjects
* Live resource view of managed resources on the agents
* Two distinct sync modes for agents: [managed](../concepts/agent-modes/managed.md) and [autonomous](../concepts/agent-modes/autonomous.md)
* Pluggable authentication methods. Out of the box, mTLS and userpass are supported.
* Pluggable configuration backend. Out of the box, Kubernetes backend is supported.
* A CLI to manage agent configuration on the control plane

## Medium-term road map

The following items are planned to be implemented along the GA (1.0) release of argocd-agent:

* [Make desired manifests accessible to principal](https://github.com/argoproj-labs/argocd-agent/issues/344)
* [Make terminal pods accessible to principal](https://github.com/argoproj-labs/argocd-agent/issues/129)
* [Make pod logs accessible to principal](https://github.com/argoproj-labs/argocd-agent/issues/128)
* [Integrate with OpenTelemetry](https://github.com/argoproj-labs/argocd-agent/issues/119)
* [Integration with SPIFFE for authentication](https://github.com/argoproj-labs/argocd-agent/issues/345)
* [Compression of data exchanged between principal and agents](https://github.com/argoproj-labs/argocd-agent/issues/113)

## Longer-term road map

* [High availability for the principal](https://github.com/argoproj-labs/argocd-agent/issues/186)

## Miscellaneous

We track all bugs and feature requests on our [GitHub issue tracker](https://github.com/argoproj-labs/argocd-agent/issues) and map them to particular releases on our [milestones overview](https://github.com/argoproj-labs/argocd-agent/milestones).