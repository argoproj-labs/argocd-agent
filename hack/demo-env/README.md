# There are dragons beyond this point

**WARNING:*** The scripts in this directory are not supposed to be used anywhere except for development or demo purposes. 

## Description

This directory and sub-directories contain scripts, kustomize manifests and other stuff that allow you to set-up a development and demo environment for `argocd-agent`. It comes without warranty. Running any of these scripts can fiddle with your connected cluster up to the point of no return, could break things on your local system, etc etc.

The scripts are targeting the author's development system. Do not run them against yours or be prepared to dive into undocumented configuration and to clean up after yourself.

It uses `vcluster` to create three virtual clusters:

* vcluster-control-plane - For hosting the control plane and principal
* vcluster-agent-managed - A cluster with agent in managed mode
* vcluster-agent-autonomous - A cluster with agent in autonomous mode

It will install Argo CD to each of those vclusters, in varying degrees of completeness.

Both, vclusters and Argo CD installations, will require that LoadBalancer functionality is available on the host cluster (metalllb will be totally ok).

## Set up

To setup, run

```
./hack/demo-env/setup-vcluster-env.sh create
```

This will create three vclusters on your current cluster, and install opinionated Argo CD into each of them.

You will need `vcluster` in your `$PATH`, and the current kubeconfig context must be configured to connect to your cluster as a cluster admin.

## Details

### Endpoints

Your LoadBalancer (e.g. metallb) is supposed to issue IP addresses in the range `192.168.56.200-254` and to accept requests for particular IPs. If it's not, you're going to have to modify some of the manifests, patches and other stuff to adapt to your particular environment.

By default, the scripts in this directory will configure:

* The Argo CD UI on the control plane to be available at `https://192.168.56.220`
* The redis server on the control plane to be exposed to `192.168.56.222`
* The repository server on the control plane to be exposed to `192.168.56.222`

### Credentials

This is a local development environment. It comes with pre-configured credentials for the sake of simplicity.

You can authenticate to the the Argo CD UI or API server with user `admin` and password `adminadmin`. Creative, isn't it.

You will need to generate credentials for the agents. Run the `gen-creds.sh` script before you start any of the agent or principal components.
