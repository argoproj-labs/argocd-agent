# Reverse-proxy via rathole

This folder contains shell scripts and Kubernetes resources to establish a reverse tunnel from the K8s cluster, back to your local development environment.

Primarily this enables the resource-proxy feature when developing on remote K8s clusters, as Argo CD (running on vcluster-control-plane) is now able to tunnel to principal running locally.

This also allows all the E2E tests to pass in this configuration. Previously, the resource proxy E2E tests were failing when running against a remote cluster.


**NOTE**: The current implementation requires Service LoadBalancer support from K8s (which is available from, for example, Red Hat's 'clusterbot' service).

The tunnel that is enabled is:
* Argo CD 'server' workload  (on vcluster-control-plane) -> managed-agent cluster Secret (on vcluster-control-plane) -> rathole Deployment (on vcluster) -> rathole Container (running on local machine) -> argocd-agent 'principal' OS process (local machine)



### How to enable

To enable the configuration:

```
# Setup environment as usual
make setup-e2e

# Installs Rathole Deployment/Services on vcluster, update 'managed-agent' cluster Secret to point to Rathole Deployment, and start local rathole container
./hack/dev-env/reverse-tunnel/setup.sh

```

To test the configuration:

```
# Start E2E tests
make start-e2e
```



### Configuration

Running 'setup-e2e', followed by 'reverse-tunnel/setup.sh', enables this configuration:


'vcluster-control-plane' vcluster (on k8s cluster):
- argocd namespace:
    - Argo CD workloads
        - Argo CD Server (no change)
        - Argo CD 'agent-managed' Cluster Secret
            - `.data.server` field points to Service, `https://rathole-container-internal:9090?agentName=agent-managed`
            - `.data.config` field is updated to enable insecure mode, and remove `caData` from TLS config
    - K8s Service
        - `rathole-container-internal`: internal service that directs traffic on port 9090 to `rathole` Deployment (below)

    - Rathole:
        - Deployment `rathole`:
            - redirects traffic from argocd (above) to local rathole container (below)
        - Service `rathole-container-external`: Redirect LoadBalancer to `rathole` Deployment (allows local machine to connect to cluster rathole)

Running Locally:
- Principal process (no change)
    - resource-proxy: traffic is redirect from cluster rathole Deployment to here
- `rathole` container: Ultimately redirects traffic from remote argocd server (via cluster rathole) to local resource-proxy in principal process
