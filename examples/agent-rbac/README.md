This directory contains examples for RBAC for the Agent's Resource Proxy to allow for different levels of CRUD operations on resources.
Argo CD Agent's resource proxy started with more strict permissions only allowing for operation on Secrets, ConfigMaps, Namespaces, and Argo CD resources (Applications, ApplicationSets, and AppProjects).

There are two examples that are taken from the [live resources](https://argocd-agent.readthedocs.io/latest/user-guide/live-resources/#rbac-requirements) docs page.
For more information about the resource proxy's permissions see that docs page.

The first example is a ClusterRole for that allows for permissions on common core Kubernetes resources that would be deployed by applications from all categories like workloads,
configuration, and RBAC, and networking.

The second example ClusterRole shows how RBAC could be configured for CustomResources that an application might deploy. Examples are shown for cert-manager, istio, and the monitoring stack.