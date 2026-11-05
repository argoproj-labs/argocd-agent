# Experimental: Argo CD Agent Reverse Tunnels

The subdirectories of this directory contain optional configurations which build on top of the default `make setup-e2e` E2E configuration.

These optional configurations allow argocd-agent E2E tests (or development environment) to run within different K8s environments than the default MicroK8s + MetalLB that runs as part of GitHub PR CI.

These configuration differ from `make setup-e2e`:
- They expose Argo CD agent external endpoint via different mechanisms (e.g. `Service` LoadBalancer or `Route`)
- They use reverse tunnelling tools to allow K8s cluster to access local machine's development environment (e.g. the processes invoked via `goreman`)

Note: These configurations are experimental. E2E tests will still need to pass in the traditional CI environment that is configured by hack/dev-env scripts.

See subdirectories for details.