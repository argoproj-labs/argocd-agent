# Reverse-proxy via OpenShift Routes and frp

This folder contains shell scripts and Kubernetes resources to establish a reverse tunnel from the K8s cluster, back to your local development environment.

This configuration is:
- Primarily for users who have access to short-lifetime, development-focused OpenShift clusters
- Allows you to develop argocd-agent, or run E2E tests, on OpenShift w/o requiring additional loadbalancers

Like other `reverse-tunnels/` directories, these are for development/test environment use only.

This script:
- Enables the resource-proxy/redis-proxy feature when developing/testing on remote K8s clusters, as Argo CD (running on vcluster-control-plane) is now able to tunnel to principal running locally.
- Converts the `LoadBalancer` Services that `make setup-e2e` leaves in place on OpenShift (`argocd-redis` in all three vclusters, plus the control plane's `argocd-server`) to OpenShift Routes (which don't require external load balancer)
- `argocd-server` already serves HTTPS, so it's exposed via a direct passthrough Route. 
- `argocd-redis` speaks plaintext by default, so a self-signed TLS cert is generated and wired in, before it's exposed via a passthrough Route, since passthrough routing depends on reading SNI from a TLS handshake. 

The tunnel that is enabled is:
* Argo CD 'server' workload  (on vcluster-control-plane) -> managed-agent cluster Secret (on vcluster-control-plane) -> frps Deployment (on vcluster) -> frpc Container (running on local machine) -> argocd-agent 'principal' OS process (local machine)



### How to enable

To enable the configuration:

```
# Setup environment as usual
make setup-e2e

# Installs frps Deployment/Services on vcluster, update 'managed-agent' cluster Secret to point to frps Deployment, and start local frpc container
./hack/dev-env/reverse-tunnels/openshift-routes/setup.sh

```

To test the configuration:

```
# Start E2E tests
make start-e2e
```



### Configuration

Running 'setup-e2e', followed by 'reverse-tunnels/openshift-routes/setup.sh', enables this configuration:


'vcluster-control-plane' vcluster (on k8s cluster):
- argocd namespace:
    - Argo CD workloads
        - Argo CD Server: `--redis-use-tls --redis-insecure-skip-tls-verify` args added, so it speaks TLS to the principal's Redis-proxy listener via the frp tunnel
        - Argo CD Repo Server: same `--redis-use-tls --redis-insecure-skip-tls-verify` args added
        - Argo CD 'agent-managed' Cluster Secret
            - `.data.server` field points to Service, `https://frp-container-internal:9090?agentName=agent-managed`
            - `.data.config` field is updated to enable insecure mode, and remove `caData` from TLS config
    - K8s Service
        - `frp-container-internal`: internal service that directs traffic on ports 9090/6379 to the `frp-container` Deployment (below)

    - frp:
        - Deployment `frp-container` (runs `frps`):
            - redirects traffic from argocd (above) to the local frpc container (below)
        - Service `frp-container-external` (ClusterIP) + Route `frp-container-external` (passthrough): Exposes the `frps` Deployment externally, without a cloud LoadBalancer (allows local machine to connect to cluster frps)
    - Service `argocd-redis` (ClusterIP, with an added TLS-only port) + Route `argocd-redis` (passthrough) - used by `make start-e2e` to reach this redis, instead of the `kubectl port-forward` it previously used
    - Secret `argocd-redis-proxy-tls`: self-signed cert/key for the principal's own Redis-proxy listener (reached by Argo CD Server/Repo Server above via the frp tunnel at `frp-container-internal:6379`)
    - Service `argocd-server` (ClusterIP) + Route `argocd-server` (passthrough)

'vcluster-agent-managed' vcluster (on k8s cluster):
- argocd-managed namespace:
    - Service `argocd-redis` (ClusterIP, with an added TLS-only port) + Route `argocd-redis` (passthrough)

'vcluster-agent-autonomous' vcluster (on k8s cluster):
- argocd-autonomous namespace:
    - Service `argocd-redis` (ClusterIP, with an added TLS-only port) + Route `argocd-redis` (passthrough)

Running Locally:
- Principal process: its Redis-proxy listener (reached by Argo CD Server/Repo Server via the frp tunnel) is TLS-enabled using the `argocd-redis-proxy-tls` Secret above, and its own upstream connection to vcluster-control-plane's `argocd-redis` goes over the Route (TLS, insecure) instead of a `kubectl port-forward`
    - resource-proxy: traffic is redirected from cluster frps Deployment to here
- `frpc` container: Ultimately redirects traffic from remote argocd server (via cluster frps) to local resource-proxy in principal process
