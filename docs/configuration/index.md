# Configuration Overview

This section covers how to configure the argocd-agent components (principal and agent) for your deployment.

## Configuration Methods

Both the principal and agent components can be configured using three methods, listed in order of precedence:

| Method | Precedence | Recommended For |
|--------|------------|-----------------|
| **Command line flags** | Highest | Local development, testing, overrides |
| **Environment variables** | Medium | Container deployments, CI/CD pipelines |
| **ConfigMap entries** | Lowest | Production Kubernetes deployments |

The recommended approach for production deployments is to use ConfigMap entries mounted to the component deployments.

### Precedence Example

If you set a parameter via all three methods:

```bash
# Command line (wins)
argocd-agent principal --metrics-port=9000

# Environment variable (overridden)
export ARGOCD_PRINCIPAL_METRICS_PORT=8500

# ConfigMap (overridden)
# principal.metrics.port: "8000"
```

The effective value will be `9000` (from the command line flag).

### Exception: Runtime-Switchable Debug Settings

Three debug settings are re-read from the `argocd-agent-params` ConfigMap while
the component runs, so that they can be changed without a restart:

* `<component>.log.level`
* `<component>.log.full-detail`
* `<component>.pprof.enabled`

For these keys the precedence above is reversed: the flag or environment
variable provides the value the component starts with, but once the ConfigMap
has been read its value wins, including over a command line flag. Removing the
key reverts the setting to the startup value. Invalid values are logged and ignored.

See [Observability](observability.md#changing-settings-without-a-restart) for
details.

## ConfigMap Configuration

For production deployments, create an `argocd-agent-params` ConfigMap in the same namespace as the component:

### Principal ConfigMap Example

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
  namespace: argocd
data:
  principal.listen.host: "0.0.0.0"
  principal.listen.port: "8443"
  principal.log.level: "info"
  principal.namespace: "argocd"
  principal.metrics.port: "8000"
  principal.healthz.port: "8003"
  principal.tls.secret-name: "argocd-agent-principal-tls"
  principal.tls.server.root-ca-secret-name: "argocd-agent-ca"
  principal.tls.client-cert.require: "true"
  principal.auth: "mtls:CN=([^,]+)"
```

### Agent ConfigMap Example

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
  namespace: argocd
data:
  agent.server.address: "argocd-agent-principal.example.com"
  agent.server.port: "8443"
  agent.mode: "autonomous"
  agent.namespace: "argocd"
  agent.log.level: "info"
  agent.creds: "mtls:^CN=(.+)$"
  agent.tls.client.insecure: "false"
  agent.tls.root-ca-secret-name: "argocd-agent-ca"
  agent.tls.secret-name: "argocd-agent-client-tls"
```

## Configuration Topics

This section is organized by task to help you find what you need:

| Topic | Description |
|-------|-------------|
| [Authentication](authentication.md) | Configure authentication methods (mTLS, header-based) |
| [TLS & Certificates](tls-certificates.md) | Set up PKI, manage certificates, configure TLS settings |
| [Namespaces](namespaces.md) | Namespace model, access control, automatic namespace creation |
| [Networking](networking.md) | Transport protocols, compression, keepalives, service mesh integration |
| [High Availability](ha.md) | Configure active/passive HA with replication |
| [Observability](observability.md) | Logging, metrics, profiling, and health checks |
| [Reference: Principal](reference/principal.md) | Complete principal parameter reference |
| [Reference: Agent](reference/agent.md) | Complete agent parameter reference |

!!! tip "Mapping Mode Configuration"
    argocd-agent supports two mapping modes: **namespace-based** (default) and **destination-based**. If you plan to use destination-based mapping, both principal and agent must have `--destination-based-mapping` enabled. See [Agent Mapping Modes](../concepts/agent-mapping.md) for a conceptual overview, or the [Principal](reference/principal.md#destination-based-mapping) and [Agent](reference/agent.md#destination-based-mapping) reference pages for configuration details.

## Quick Start

### Minimal Principal Configuration

```yaml
# ConfigMap
principal.namespace: "argocd"
principal.auth: "mtls:CN=([^,]+)"
principal.tls.client-cert.require: "true"
```

### Minimal Agent Configuration

```yaml
# ConfigMap
agent.server.address: "<principal-address>"
agent.server.port: "8443"
agent.namespace: "argocd"
agent.creds: "mtls:"
```

## Security Best Practices

1. **Use mTLS authentication** for production deployments
2. **Never use `--insecure-*` flags** in production, with one exception: `--insecure-plaintext` is required when running behind a service mesh (Istio, Linkerd) that handles TLS at the sidecar level. See [Networking](networking.md#service-mesh-integration) for details.
3. **Store secrets properly** - TLS certificates and JWT keys should be in Kubernetes Secrets, not ConfigMaps
4. **Rotate certificates regularly** - Implement automated certificate rotation
5. **Restrict network access** - Use network policies to limit access to metrics and health endpoints

For detailed security guidance, see the [Authentication](authentication.md) and [TLS & Certificates](tls-certificates.md) sections.
