# JWT Signing Key Management with argocd-agentctl

The `argocd-agentctl` CLI includes commands to manage JWT signing keys used by the argocd-agent principal. These commands allow you to create, inspect, and delete JWT signing keys stored in Kubernetes secrets.

## Commands Overview

The JWT commands are available under the `jwt` subcommand:

```bash
argocd-agentctl jwt --help
```

Available subcommands:
- `create-key` - Create a new JWT signing key
- `inspect-key` - Inspect an existing JWT signing key
- `delete-key` - Delete a JWT signing key

## Creating a JWT Signing Key

The `create-key` command generates a new 4096-bit RSA private key and stores it in a Kubernetes secret:

```bash
argocd-agentctl jwt create-key
```

### Options

- `--force, -f`: Force recreation of the JWT key if it already exists
- `--principal-context`: Kubernetes context for the principal cluster
- `--principal-namespace`: Namespace where the principal is installed (default: "argocd")

### Examples

Create a JWT signing key in the default namespace:
```bash
argocd-agentctl jwt create-key
```

Create a JWT signing key with force recreation:
```bash
argocd-agentctl jwt create-key --force
```

Create a JWT signing key in a specific context and namespace:
```bash
argocd-agentctl jwt create-key \
  --principal-context my-cluster \
  --principal-namespace argocd-system
```

## Inspecting a JWT Signing Key

The `inspect-key` command displays information about an existing JWT signing key:

```bash
argocd-agentctl jwt inspect-key
```

### Options

- `--output, -o`: Output format (json, yaml, text) - default: json
- `--principal-context`: Kubernetes context for the principal cluster
- `--principal-namespace`: Namespace where the principal is installed

### Examples

Inspect with JSON output (default):
```bash
argocd-agentctl jwt inspect-key
```

Inspect with YAML output:
```bash
argocd-agentctl jwt inspect-key --output yaml
```

Inspect with human-readable text output:
```bash
argocd-agentctl jwt inspect-key --output text
```

### Sample Output

JSON output:
```json
{
  "keyType": "RSA",
  "keyLength": 4096,
  "created": "Mon, 16 Dec 2024 00:15:30 +0000",
  "sha256": "a1b2c3d4e5f6789012345678901234567890abcdef..."
}
```

## Deleting a JWT Signing Key

The `delete-key` command removes the JWT signing key secret:

```bash
argocd-agentctl jwt delete-key
```

### Options

- `--force, -f`: Force deletion without confirmation prompt
- `--principal-context`: Kubernetes context for the principal cluster
- `--principal-namespace`: Namespace where the principal is installed

### Examples

Delete with confirmation prompt:
```bash
argocd-agentctl jwt delete-key
```

Delete without confirmation:
```bash
argocd-agentctl jwt delete-key --force
```

## Key Specifications

- **Algorithm**: RSA
- **Key Length**: 4096 bits
- **Format**: PKCS#8 PEM encoded
- **Secret Name**: `argocd-agent-jwt`
- **Secret Field**: `jwt.key`
- **Secret Type**: `Opaque`

## Integration with Principal

Once created, the JWT signing key secret can be automatically used by the argocd-agent principal without any additional configuration. The principal will:

1. Try to load from `--jwt-key` file path (if specified)
2. Generate a temporary key if `--insecure-jwt-generate` is enabled
3. **Load from the `argocd-agent-jwt` secret** (new default behavior)

## Security Considerations

- JWT signing keys are critical security components
- Store them securely in Kubernetes secrets
- Use RBAC to restrict access to the secrets
- Rotate keys periodically for security
- The 4096-bit RSA keys provide strong cryptographic security
- Keys are generated using cryptographically secure random sources

## Troubleshooting

### Permission Errors
Ensure your Kubernetes context has permissions to create/read/delete secrets in the principal's namespace:
```bash
kubectl auth can-i create secrets --namespace argocd
kubectl auth can-i get secrets --namespace argocd
kubectl auth can-i delete secrets --namespace argocd
```

### Context and Namespace Configuration
Set default values using environment variables:
```bash
export ARGOCD_AGENT_PRINCIPAL_CONTEXT="my-cluster"
export ARGOCD_AGENT_PRINCIPAL_NAMESPACE="argocd-system"
```

### Verifying the Secret
Check if the secret was created correctly:
```bash
kubectl get secret argocd-agent-jwt -n argocd -o yaml
```

The secret should contain a `jwt.key` field with PEM-encoded RSA private key data. 