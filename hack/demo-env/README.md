# There are dragons beyond this point

**WARNING:*** The scripts in this directory are not supposed to be used anywhere except for development or demo purposes. 

## Description

This directory and sub-directories contain scripts, kustomize manifests and other stuff that allow you to set-up a development and demo environment for `argocd-agent`. It comes without warranty. Running any of these scripts can fiddle with your connected cluster up to the point of no return, could break things on your local system, etc etc.

The scripts are targeting the author's development system. Do not run them against yours or be prepared to dive into undocumented configuration and to clean up after yourself.

It uses [vcluster](https://github.com/loft-sh/vcluster) to create three virtual clusters:

* vcluster-control-plane - For hosting the control plane and principal
* vcluster-agent-managed - A cluster with agent in managed mode
* vcluster-agent-autonomous - A cluster with agent in autonomous mode

It will install Argo CD to each of those vclusters, in varying degrees of completeness.

Both, vclusters and Argo CD installations, will require that LoadBalancer functionality is available on the host cluster (metalllb will be totally ok).

## Set up

Make sure you have administrative access to a cluster and [vcluster](https://github.com/loft-sh/vcluster) is installed on your machine and within your `$PATH`. 

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

## Starting the components

### Running locally in your shell

There are three scripts which can be used to start the respective component locally on your system. You will have to have the vclusters created for them to work, and also you must have created the credentials already.

* `start-principal.sh` will start the agent's principal component
* `start-agent-autonomous.sh` will start an agent in autonomous mode
* `start-agent-managed.sh` will start an agent in managed mode

Starting multiple agents of the same type is not supported in this demo/testing environment.

Components will be started in insecure and transient mode, that means:

* TLS verification will be disabled on the agents,
* the principal will generate and use a self-signed, temporary TLS certificate and key
* the principal will generate and use a temporary RSA key for signing issued JWTs

### Starting a debug session from vscode

There is a vscode launch configuration to assist with debugging components in `hack/vscode/launch.json`

### Running in a cluster

To be written.
