# ArgoCD-Agent with gitops-operator/openshift operator

This guide will cover argocd-operator/gitops-operator setup for argocd-agent on openshift clusters, for argocd-agent setup refer [quickstart](https://github.com/argoproj-labs/argocd-agent/blob/main/docs/hack/quickstart.md) guide. This document will setup argocd-agent in `autonomous` mode.

_Note: There are still some limitations to setup, one of them being sharing redis instance between argocd instances is not supported right now._  

## Pre-requisite

- Kuberenetes clusters with argocd-opeator/gitops-operator install on both hub and spoke cluster.
- The operator should be installed in [cluster scope](https://argocd-operator.readthedocs.io/en/stable/usage/basics/#cluster-scoped-instance) mode.
- [Apps-in-Any-Namespaces](https://argocd-operator.readthedocs.io/en/stable/usage/apps-in-any-namespace/) needs to be enabled.
- Argocd-agent needs to be deployed appropriately.
- Argocd-agent will be set with agent name `argocd`


### argocd instance on Principal cluster

Creating argocd instance for principal/hub cluster, argocd instance will be sharing repo component with agent cluster
For simplicity, this guide deploy all the resources on namespace `argocd`, i.e. argo-cd instance, arogcd-agent instance and application. As we are deploying everything on `argocd` namespace, agent name will be used is `argocd`. 

```yaml
  apiVersion: argoproj.io/v1beta1
  kind: ArgoCD
  metadata:
    name: gp
    namespace: argocd
  spec:
    controller:
      enabled: false
    redis:
      enabled: true
    repo:
      enabled: true
    server:
      route:
        enabled: true
    sourceNamespaces:
    - argocd
```

This will setup argocd instance with argocd-application-controller disabled and running server, repo and redis components.
Once the argocd instance is up and running, repo service need to be updated from `ClusterIP` to use `LoadBalancer`.

### argocd instance on Agent cluster

Creating argocd instance for agent/spoke cluster, this instance will be using the shared repo instance from principal and, have locally running redis and argocd-application-controller instance.
Update the below manifest with loadbalancer ip to create argocd instance. 
```yaml
  apiVersion: argoproj.io/v1beta1
  kind: ArgoCD
  metadata:
    name: ga
    namespace: argocd
  spec:
    repo:
      remote: <repo LoadBalancer IP addr>:8081
    server:
      enabled: false
    sourceNamespaces:
    - argocd
```

Once the resources are up, check if `default` AppProject is created, create a AppProject if not present using below manifest.

```yaml
  apiVersion: argoproj.io/v1alpha1
  kind: AppProject
  metadata:
    name: default
    namespace: argocd
  spec:
    clusterResourceWhitelist:
    - group: '*'
      kind: '*'
    destinations:
    - namespace: '*'
      server: '*'
    sourceNamespaces:
    - '*'
    sourceRepos:
    - '*'
```

### Deploying application

As this documentation's using argocd-agent in autonomous mode, application will be created in the agent cluster.
For simplicity, we will be using `argocd` namespace for application development as well.

Use the below manifest or autonomous-guestbook application [example](https://github.com/argoproj-labs/argocd-agent/blob/main/hack/demo-env/apps/autonomous-guestbook.yaml) from demo.
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: guestbook
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: kustomize-guestbook
  destination:
    server: https://kubernetes.default.svc
    namespace: guestbook
  syncPolicy:
    syncOptions:
      - "CreateNamespace=true"
```

List the application on both the cluster, if present on both the cluster, argocd-agent is working as expected.

To access the application one can us argocd CLI/UI using the route created on principal cluster. 