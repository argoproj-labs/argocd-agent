# Getting started

This is **not** a guide to set-up argocd-agent for a production environment. It is rather a guide to help you get going with contributing to and hacking on argocd-agent. This guide does not intend to provide pure copy & paste instructions. Instead, it will assume a little bit of willingness to hackery on your side, as well as some core knowledge about the underlying technologies (JWT, TLS, etc)

Please note that some resource names might be out-of-date (e.g. have changed names, or were removed and replaced by something else). If you notice something, please figure it out and submit a PR to this guide. Thanks!

## Installing the principal

To install the principal, we will need the following things:

* An RSA key used to sign JWT tokens and
* A set of TLS certificates and private keys,
* A credentials file for authenticating agents
* Some Argo CD components installed, depending on the desired mode of agents

For the sake of simplicity, we will use the `argocd` namespace to install all required resources into the cluster.

The principal installation will come with a ClusterRole and ClusterRoleBinding, so make sure the account you'll be using for installation has sufficient permissions to do that.

### Generating the JWT signing key

The JWT signing key will be stored in a secret in the principal's namespace. We will first need to create the key:

```
openssl genrsa -out /tmp/jwt.key 2048
```

If you want a keysize different than 2048, just specify it.

Then, create the secret named `argocd-agent-principal-jwt` with the RSA key as field `jwt.key`:

```
kubectl create -n argocd secret generic --from-file=jwt.key=/tmp/jwt.key argocd-agent-jwt
```

### TLS certificates and CA

The principal itself will need a valid TLS server certificate and key pair in order to provide its service. This may be a self-signed certificate if you turn off TLS verification on the agents but that is insecure and shouldn't be used anywhere, really.

A much better idea is to use certificates issued by an existing CA or to set up your own CA. For development purposes, it is fine (and even encouraged) that such CA is of transient, non-production nature.

Once you have the server's certificate and key (in this example, stored in files named `server.crt` and `server.key` respectively) as well as the certificate of the CA (stored in a file named `ca.crt`), create a new secret with the contents of these files:

```
kubectl create -n argocd secret generic --from-file=tls.crt=server.crt --from-file=tls.key=server.key --from-file=ca.crt=ca.crt argocd-agent-tls
```

Keep the `ca.crt` file, as you will need it for the installation of agents, too.

### Apply the installation manifests

Now that the required secrets exist, it's time to apply the installation manifests and install the principal into the cluster:

```
kubectl apply -n argocd -k install/kubernetes/principal
```

This should create all required manifests in the cluster and spin up a deployment with a single pod.

To check its status:

```
kubectl get -n argocd deployments argocd-agent-principal
kubectl get -n argocd pods
```

### Configuring the principal

You can configure the principal by editing the `argocd-agent-params` ConfigMap in the principal's installation namespace. For an up-to-date example with comments, have a look at the
[example](https://github.com/jannfis/argocd-agent/blob/main/install/kubernetes/principal/principal-params-cm.yaml)

After a change to the `argocd-agent-params` ConfigMap, the principal needs to be restarted to pick up the changes:

```
kubectl rollout -n argocd restart deployment argocd-agent-principal
```

## Installing the agent

TODO.