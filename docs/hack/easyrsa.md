# BYO-CA

**NOTE:** This should not be considered official documentation. It should help you get going, but not spoon-feed you. You are expected to hack a little. If there's something wrong or missing in this docs, please feel free to submit a PR.

## Setting up the CA

You can up a (non-production, transient) local certificate authority. We recommend using [EasyRSA](https://github.com/OpenVPN/easy-rsa/releases) for this purpose. 

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

## Issuing the principal's certificates

For the principal, a server certificate is required:

```
./easysrsa --san <your SAN entries here> build-server-full <your principal name>
```

It is important to figure out the right DNS names or IP addresses where your agents will access the principal.