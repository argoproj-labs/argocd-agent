# Development Environment for `argocd-agent`

⚠️ Warning: This is a development environment for `argocd-agent`. It is not intended for production use and should not be used as such.

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

### Before you start

You will need to have kubectl, helm, kustomize, vcluster, jq, and htpasswd installed on your system. You can install them using your package manager of choice, or you can use the following commands to install them on a Debian-based system:

- [snap](https://snapcraft.io/docs/installing-snapd)
- [go](https://go.dev/doc/install)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [kustomize](https://kubectl.docs.kubernetes.io/installation/kustomize/)
- [vcluster](https://github.com/loft-sh/vcluster/releases)
- [jq](https://stedolan.github.io/jq/download/)
- htpasswd can be installed using the following command: `sudo apt-get install apache2-utils` on Debian-based systems or `sudo dnf install httpd-tools` on Fedora-based systems.
- [microk8s](https://microk8s.io/#install-microk8s). The author uses microk8s as the host Kubernetes cluster, but you can use any Kubernetes cluster that supports LoadBalancer functionality if you prefer. If you aren't quite sure if you should or should not use a microk8s cluster to follow along, then you probably should just to make following the steps easier for now.There are future plans to make this work with kind clusters as well, but for now, microk8s is the easiest way to go for now.

**Note:** 

One thing to note is that one of the agents will try to use port 9090 on the host system. So if you have something running on that port like Cockpit, then you will need to stop it or change the port in the agent's configuration.

```shell
sudo systemctl stop cockpit.socket
```

If you run this and get an error of something along the lines of `cockpit.socket` not being found, then you can ignore it and continue along. It just means that you don't have Cockpit installed on your system.Nothing to worry about.

### Setting up Host cluster

The author uses [microk8s](https://microk8s.io/) as the host Kubernetes cluster.

Once MicroK8s is installed, you will want to start MicroK8s

```shell
sudo microk8s start
```

You can check the status of microk8s with

```shell
sudo microk8s status --wait-ready
```

Then, you will want to enable metallb and hostpath storage in that cluster. Metallb gives your cluster load balancer capabilities, and the hostpath storage will allow vcluster to persist its configuration:

```shell
# Enable the metallb addon in microk8s
# Adjust the range as needed. Currently set to values used in the steps below
sudo microk8s enable metallb:192.168.56.200-192.168.56.254
# Enable hostpath storage for vcluster to use
sudo microk8s enable hostpath-storage
```

You may want to export the kubectl configuration from microk8s so that your non-root user can easily use it. The following assumes that you will not have an existing kubectl configuration in your user's home, as it will overwrite any existing configuration.

```shell
mkdir -p ~/.kube && microk8s config > ~/.kube/config
sudo chmod 644 ~/.kube/config
```

What's left is to install the metallb configuration. You should be good now.

```shell
kubectl apply -n metallb-system -f hack/dev-env/resources/metallb-ipaddresspool.yaml
```

### Virtual clusters

To setup all required virtual clusters, run

```shell
chmod +x ./hack/dev-env/setup-vcluster-env.sh
./hack/dev-env/setup-vcluster-env.sh create
```

This will create three vclusters on your current cluster, and install an opinionated Argo CD into each of them.

You will need `vcluster` in your `$PATH`, and the current kubeconfig context must be configured to connect to your cluster as a cluster admin.

Please note: The script requires `vcluster` version 0.20-beta4 or newer to function correctly.

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

You will need to generate credentials for the agents. Before you start any of the agent or principal components run:

```shell
chmod +x ./hack/dev-env/gen-creds.sh
./hack/dev-env/gen-creds.sh
```

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

Once you have three terminal windows open and they are running the respective scripts (start-principal.sh, start-agent-autonomous.sh, start-agent-managed.sh), then you can now create the Argo CD applications in the vcluster with the autonomous agent or have the application run in the managed mode.

### Install Argo CD App in autonomous vcluster

To install the Argo CD application in the autonomous vcluster, you can use the following command:

```shell
kubectl config use-context vcluster-agent-autonomous
kubectl apply -f ./hack/dev-env/apps/autonomous-guestbook.yaml
```

### Install Argo CD App in managed vcluster

To install the Argo CD application in the managed vcluster, you can use the following command:

```shell
kubectl config use-context vcluster-control-plane
kubectl apply -f hack/dev-env/apps/managed-guestbook.yaml
```

### View the Argo CD UI

You can view the Argo CD UI by navigating to `https://192.168.56.220`.

username: `admin`

password: `should be in output from setup-vcluster-env.sh`

If you have applied the autonomous application and the managed application, you should see both applications in the Argo CD UI.

Keep in mind that you manage the autonomous application from the vcluster-agent-autonomous and the managed application from the vcluster-control-plane, but you can view both applications in the Argo CD UI.

### Resetting Dev Environment

If you want to reset the development environment, you can run the following command:

```shell
sudo microk8s reset
```

### Starting a debug session from vscode

There is a vscode launch configuration to assist with debugging components in `hack/vscode/launch.json`

### Running in a cluster using Open Cluster Management

For running in a cluster using [Open Cluster Management (OCM)](https://open-cluster-management.io/),
see [here](https://github.com/open-cluster-management-io/ocm/tree/main/solutions/argocd-agent) for more information.

### Future work

- Migrating to only using kind clusters instead of vclusters
- Lean on NodePort services to expose the services needed for intra cluster communication instead of relying on LoadBalancer based Ingresses which require us to configure metallb on our development workstation.
