# Getting started

## Prerequisites

Before applying the manifests to the cluster, a couple of prerequisites are required to get you started quickly. We need at least:

* A set of TLS certificates and private keys,
* an RSA key used to sign JWT tokens and
* a credentials file

### TLS certificates and CA

It is recommended to set up a (non-production, transient) local certificate authority. We recommend using [EasyRSA](https://github.com/OpenVPN/easy-rsa/releases) for this purpose. 

To get started with EasyRSA:

* Download the latest EasyRSA release
* Extract the tar ball and enter the directory that was created (e.g. `EasyRSA-3.2.0`)

Create a file `vars` in that directory, with the following contents:

```
set_var EASYRSA_PKI            "$HOME/argo-agent-pki"

set_var EASYRSA_REQ_COUNTRY    ""
set_var EASYRSA_REQ_PROVINCE   ""
set_var EASYRSA_REQ_CITY       ""
set_var EASYRSA_REQ_ORG        "argocd-agent temporary"
set_var EASYRSA_REQ_EMAIL      "me@example.net"
set_var EASYRSA_REQ_OU         "argocd-agent"
set_var EASYRSA_NO_PASS        1
set_var EASYRSA_KEY_SIZE       4096
```

Still in this directory, run

```
./easyrsa init-pki
```

This will create your `$HOME/argo-agent-pki` directory, which will be used to store all certificates and private keys. Then, run

```
./easyrsa build-ca
```

to initialize the actual CA.