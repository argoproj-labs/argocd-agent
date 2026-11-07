# Adding Agents Declaratively

[Adding New Agents](./adding-agents.md) registers an agent with `argocd-agentctl`. The CLI does not do anything special: it creates ordinary Kubernetes resources. You can create the same resources from manifests instead, for example when the control plane is managed with GitOps or when the CLI has no access to the cluster.

This page describes those resources, with the cluster secret that maps an agent on the principal in most detail.

!!! tip "Looking for automated onboarding?"
    With [agent self-registration (Beta)](./self-registration.md) the principal creates the cluster secret itself when an agent connects for the first time.

## What an Agent Needs

| Resource | Cluster | Imperative command | Declarative equivalent |
|----------|---------|-------------------------|------------------------|
| Cluster secret `cluster-<agent-name>` | Principal | `argocd-agentctl agent create` | [The Cluster Secret](#the-cluster-secret) |
| Namespace `<agent-name>` | Principal | `kubectl create namespace` | [The Agent Namespace](#the-agent-namespace) |
| Secrets `argocd-agent-client-tls` and `argocd-agent-ca` | Agent | `argocd-agentctl pki issue agent` | [The Agent's Own Credentials](#the-agents-own-credentials) |

The one-time setup of the principal (certificate authority, principal and resource proxy certificates, JWT signing key) is not specific to an agent. [TLS & Certificates](../configuration/tls-certificates.md) describes how to create it with cert-manager or by hand.

## The Cluster Secret

The principal learns about an agent from an Argo CD cluster secret in its own namespace. The same secret tells Argo CD how to reach the agent's cluster through the principal's resource proxy.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cluster-my-agent
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: cluster
    argocd-agent.argoproj-labs.io/agent-name: my-agent
  annotations:
    argocd.argoproj.io/skip-reconcile: "true"
    managed-by: argocd-agent
type: Opaque
stringData:
  name: my-agent
  server: https://argocd-agent-resource-proxy.argocd.svc.cluster.local:9090?agentName=my-agent
  config: |
    {
      "tlsClientConfig": {
        "insecure": false,
        "certData": "<base64-encoded client certificate>",
        "keyData": "<base64-encoded client key>",
        "caData": "<base64-encoded CA certificate>"
      }
    }
```

| Field | Value | Read by |
|-------|-------|---------|
| `metadata.namespace` | The namespace the principal runs in. The principal does not look at secrets anywhere else. | Principal, Argo CD |
| `metadata.name` | `cluster-<agent-name>`. The mapping does not depend on the name, but self-registration looks the secret up by it, and so do `argocd-agentctl agent inspect`, `reconfigure` and `print-tls`. `agent list` prints the name without the prefix. | Principal (self-registration), CLI |
| Label `argocd.argoproj.io/secret-type` | `cluster`. Marks the secret as an Argo CD cluster. | Principal, Argo CD |
| Label `argocd-agent.argoproj-labs.io/agent-name` | The agent's name. This label is what maps the secret to an agent, see [How an Agent Is Mapped](#how-an-agent-is-mapped). | Principal |
| Annotation `argocd.argoproj.io/skip-reconcile` | `"true"`. Keeps the application controller on the control plane from reconciling Applications that target the agent. Requires Argo CD v3.4 or later, see [Hybrid Architecture](./managed-agent/hybrid-architecture.md#skip-reconcile-annotation). | Argo CD |
| Annotation `managed-by` | `argocd-agent`. `argocd-agentctl validate` only checks cluster secrets that carry it. | CLI |
| `name` | The agent's name. Argo CD resolves `spec.destination.name` against this field, so it must be unique among all cluster secrets. The principal sets it as the destination of the Applications it receives from an autonomous agent. | Argo CD, Principal |
| `server` | The address of the principal's resource proxy as Argo CD reaches it, with the agent's name in the `agentName` query parameter. The host must be one of the names in the resource proxy's server certificate, because Argo CD verifies it. The principal uses the parameter to match AppProject destinations that are given as a server URL, and `argocd-agentctl validate` expects the URL to use `https`. | Argo CD, Principal, CLI |
| `config` | The TLS client configuration Argo CD uses for the resource proxy, see [The Client Certificate](#the-client-certificate). | Argo CD |

The agent's name has to follow the [agent naming requirements](./adding-agents.md#agent-naming-requirements).

!!! important
    The example uses the fully qualified service name of the resource proxy, which is the name in the certificate issued by the cert-manager and manual procedures in [TLS & Certificates](../configuration/tls-certificates.md). `argocd-agentctl agent create` writes `argocd-agent-resource-proxy:9090` unless `--resource-proxy-server` is given. Use whichever name your resource proxy certificate contains.

### The Client Certificate

Argo CD connects to the resource proxy with the client certificate stored in `config`. The resource proxy takes the agent's name from the certificate, so the certificate has to meet three conditions:

* Its common name (CN) is the agent's name.
* It is signed by the certificate authority the resource proxy trusts for client certificates. That is the secret `argocd-agent-ca` in the principal's namespace, unless the principal is started with a different `--resource-proxy-ca-secret-name`.
* Its extended key usage allows client authentication. Either the certificate has no extended key usage, or the list includes `client auth`. A certificate limited to `server auth` is rejected.

`caData` is the certificate of the authority that signed the resource proxy's server certificate. Argo CD uses it to verify the resource proxy.

The three values are the PEM files, base64-encoded on a single line. If the certificate comes from cert-manager, the values in its secret are already base64-encoded and can be copied as they are. [Step 3: Agent Certificates](../configuration/tls-certificates.md#step-3-agent-certificates) issues such a certificate in the secret `<cluster-name>-principal`, where the cluster name is the agent's name:

```bash
AGENT=my-agent
NAMESPACE=argocd
RESOURCE_PROXY=argocd-agent-resource-proxy.${NAMESPACE}.svc.cluster.local:9090

CERT=$(kubectl get secret "${AGENT}-principal" -n "${NAMESPACE}" -o jsonpath='{.data.tls\.crt}')
KEY=$(kubectl get secret "${AGENT}-principal" -n "${NAMESPACE}" -o jsonpath='{.data.tls\.key}')
CA=$(kubectl get secret argocd-agent-resource-proxy-tls -n "${NAMESPACE}" -o jsonpath='{.data.ca\.crt}')
CA=${CA:-$(kubectl get secret argocd-agent-ca -n "${NAMESPACE}" -o jsonpath='{.data.tls\.crt}')}

if [ -z "${CERT}" ] || [ -z "${KEY}" ] || [ -z "${CA}" ]; then
  echo "secret ${AGENT}-principal is missing tls.crt or tls.key, or no CA was found in argocd-agent-resource-proxy-tls (ca.crt) or argocd-agent-ca (tls.crt)" >&2
  exit 1
fi

# The manifest holds the private key, so keep it readable by its owner only.
touch "cluster-${AGENT}.yaml"
chmod 600 "cluster-${AGENT}.yaml"

cat > "cluster-${AGENT}.yaml" <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: cluster-${AGENT}
  namespace: ${NAMESPACE}
  labels:
    argocd.argoproj.io/secret-type: cluster
    argocd-agent.argoproj-labs.io/agent-name: "${AGENT}"
  annotations:
    argocd.argoproj.io/skip-reconcile: "true"
    managed-by: argocd-agent
type: Opaque
stringData:
  name: "${AGENT}"
  server: https://${RESOURCE_PROXY}?agentName=${AGENT}
  config: |
    {
      "tlsClientConfig": {
        "insecure": false,
        "certData": "${CERT}",
        "keyData": "${KEY}",
        "caData": "${CA}"
      }
    }
EOF
```

The example takes `caData` from the `ca.crt` of the secret `argocd-agent-resource-proxy-tls`, which is the authority that signed the resource proxy's server certificate. Certificates made with `argocd-agentctl pki issue` or by hand have no `ca.crt`, so the example falls back to `tls.crt` of the secret `argocd-agent-ca`, the authority that signs the certificates in that guide. If a different authority signed your server certificate, set `caData` to its certificate instead.

cert-manager only fills `ca.crt` for issuers that expose their certificate authority, such as the CA issuer used in that guide. With certificate files created by hand, encode them first, for example with `base64 < client.crt | tr -d '\n'`.

cert-manager renews the client certificate in the secret `<cluster-name>-principal` before it expires, but it does not update the cluster secret built from it, which keeps the copy of the old certificate. Once that certificate expires, the resource proxy rejects the connections from Argo CD. Repeat the steps above after each renewal and apply the new manifest, or let the tool that manages the cluster secret do it.

!!! warning "The secret contains a private key"
    Do not commit the generated manifest to Git as it is. Store it with the secret management tool you already use for Argo CD cluster secrets, such as Sealed Secrets or External Secrets.

If you only want to avoid the PKI managed by `argocd-agentctl`, the CLI can also build the cluster secret from certificates you provide. `argocd-agentctl agent create <agent-name> --tls-from-secret <secret> --ca-from-secret <secret> --resource-proxy-server argocd-agent-resource-proxy.argocd.svc.cluster.local:9090` reads `tls.crt`, `tls.key` and `ca.crt` from existing secrets instead of issuing a new certificate. Without `--resource-proxy-server` the CLI writes `argocd-agent-resource-proxy:9090`, a name that the resource proxy certificates in [TLS & Certificates](../configuration/tls-certificates.md) do not contain, so Argo CD would fail to verify them. Use a host that is in your resource proxy certificate.

### How an Agent Is Mapped

The principal maps a cluster secret to an agent by the value of the `argocd-agent.argoproj-labs.io/agent-name` label. It watches the cluster secrets in its namespace, so a new secret takes effect without a restart.

Which agent an Application is sent to depends on the [mapping mode](./managed-agent/agent-mapping.md):

* With **destination-based mapping**, it is the agent named in `spec.destination.name`. This is why the `name` field and the label carry the same value.
* With **namespace-based mapping**, it is the agent whose name equals the Application's namespace on the principal.

In both modes the agent must have a cluster secret. Without one, the principal does not send Applications to a managed agent and logs:

```text
Application is targeting an invalid agent, skipping creation
```

Applications coming from an autonomous agent are rejected as well, with the error `cluster mapping not found for agent <name>`.

A few rules follow from the way the mapping is kept:

* One agent maps to one cluster secret. Do not create a second secret with the label of an agent that is already mapped. The principal logs `Agent <name> is already mapped to cluster <cluster>` and ignores the new secret at first, but a later change to either secret, or the removal of either one, can replace or remove the agent's mapping. If it happens, remove the duplicate and restart the principal.
* Do not rename an agent by changing the label of its secret. The principal refuses the change if the new name is already mapped, and otherwise neither cleans up the old agent nor resynchronizes the new one. Delete the secret and create a new one instead.
* When the secret is deleted, the principal disconnects the agent and discards its state.

## The Agent Namespace

With namespace-based mapping, the Applications of a managed agent live in a namespace named after the agent on the principal's cluster. Autonomous agents use such a namespace in both mapping modes: the principal writes the Applications it receives from the agent into it.

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-agent
```

The namespace also has to be allowed on the principal and in Argo CD. [Namespaces](../configuration/namespaces.md) describes both settings and how the principal can create the namespaces of autonomous agents itself.

Managed agents with destination-based mapping do not need a namespace per agent.

## The Agent's Own Credentials

The agent authenticates to the principal with a client certificate of its own, stored on the agent's cluster. It is a different certificate from the one in the cluster secret, although both carry the agent's name as common name.

| Secret | Namespace | Keys | Content |
|--------|-----------|------|---------|
| `argocd-agent-client-tls` | The agent's namespace | `tls.crt`, `tls.key` | Client certificate with the agent's name as CN, signed by the principal's certificate authority |
| `argocd-agent-ca` | The agent's namespace | `ca.crt` or `tls.crt` | Certificate of the authority that signed the principal's server certificate, without the private key. The agent uses it to verify the principal and reads every key of the secret as a certificate, so the secret must not contain a private key. In [TLS & Certificates](../configuration/tls-certificates.md) the same authority also signs the client certificates, but if your server certificate comes from a different authority, use that one here |

[TLS & Certificates](../configuration/tls-certificates.md) shows how to issue both with [cert-manager](../configuration/tls-certificates.md#using-cert-manager) or [by hand](../configuration/tls-certificates.md#manual-certificate-management). The agent itself is deployed and configured as described in [Adding New Agents](./adding-agents.md#step-5-deploy-agent-to-workload-cluster).

## Verification

The CLI reads the same resources, so it can be used to check agents that were created from manifests:

```bash
# The agent is listed
argocd-agentctl agent list --principal-context <control-plane-context>

# The cluster secret can be parsed
argocd-agentctl agent inspect my-agent --principal-context <control-plane-context>

# Runs all checks of the principal installation. Among them: the cluster secrets
# have an https server, the agentName parameter and the skip-reconcile annotation
argocd-agentctl validate principal \
  --principal-context <control-plane-context> \
  --principal-namespace argocd
```

Once the agent is connected, the cluster shows a successful connection status in Argo CD.

## Related Documentation

* [Adding New Agents](./adding-agents.md)
* [Agent self-registration (Beta)](./self-registration.md)
* [Agent Mapping Modes](./managed-agent/agent-mapping.md)
* [TLS & Certificates](../configuration/tls-certificates.md)
* [Namespaces](../configuration/namespaces.md)
