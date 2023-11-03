# argocd-agent's roadmap

The following is a list of things and items we want to support in the future. Here, the future may mean anything between short term, mid term and long term. We have not yet decided on timelines for the items.

Please note: Not all of these items are scoped for the v1.0 release.

## General

* Docs, docs, docs
* Proper end-to-end test suite

## Authentication and authorization

* SPIFFE/SPIRE authentication method to support mutual, zero-trust authentication between the control plane and the agents
* mTLS between the agent and the control plane (right now, only the server verifies its identity using TLS)

## Usability

* Provide a CLI to automate bootstraping an agent on a cluster and to register an agent with the control plane
* Ability to sync ApplicationSet and AppProject resources from the control plane to the agent

## Scalability

* Provide Application backends other than the Kubernetes API (for example, a database or a more scalable key-value store) to support more than a couple of thousands of clusters