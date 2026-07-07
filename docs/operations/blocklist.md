# TLS Certificate Blocklist

The TLS certificate blocklist allows administrators to revoke agent access by blocking their client certificate SHA-256 fingerprints. Blocklisted agents are immediately disconnected and prevented from reconnecting until their fingerprint is removed.

The blocklist is stored in a ConfigMap (`argocd-agent-tls-blocklist`) in the principal's namespace. The principal watches this ConfigMap via an informer and updates its in-memory blocklist dynamically — no restart required. The blocklist persists across principal restarts.

## Applicability

The blocklist operates on client certificate fingerprints, so it applies only to agents using mTLS authentication.

## Finding an Agent's Fingerprint

Inspect an agent to retrieve its certificate fingerprint:

```bash
argocd-agentctl agent inspect <agent-name> -o json
```

The principal also logs each agent's fingerprint on successful authentication.

## Managing the Blocklist

### Using argocd-agentctl

```bash
# Add a fingerprint
argocd-agentctl blocklist add "A1:2B:3C:4D:..."

# Remove a fingerprint
argocd-agentctl blocklist remove "A1:2B:3C:4D:..."

# List all blocked fingerprints
argocd-agentctl blocklist list
```

## ConfigMap Format

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-tls-blocklist
  namespace: argocd
data:
  checksums: '["A1:2B:3C:4D:...","E5:F6:7G:8H:..."]'
```

The `checksums` value is a JSON array of SHA-256 fingerprints in colon-separated uppercase hex format.
