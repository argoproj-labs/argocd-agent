# Argo CD Cluster Secret to Kubeconfig Converter

This utility converts Argo CD cluster secrets stored in a Kubernetes cluster into kubectl-compatible kubeconfig contexts.

## Overview

Argo CD stores cluster connection information as Kubernetes secrets with the label `argocd.argoproj.io/secret-type=cluster`. This script reads these secrets and converts them into standard kubectl kubeconfig format, allowing you to access all your Argo CD-managed clusters directly with kubectl.

## Prerequisites

- Python 3.6 or higher
- Access to a Kubernetes cluster that contains Argo CD cluster secrets
- kubectl configured to access the source cluster

## Installation

1. Install the required Python dependencies:

```bash
pip install -r requirements.txt
```

Or install manually:

```bash
pip install kubernetes PyYAML
```

## Usage

### Basic Usage

```bash
# Convert using current context
python3 clustersecret-to-kubeconf.py

# Convert using specific context
python3 clustersecret-to-kubeconf.py --context <source-context>
```

Where `<source-context>` is the kubectl context for the cluster that contains your Argo CD cluster secrets. If not specified, the current context will be used.

**Note**: By default, the script writes to `~/.kube/argocd-clusters` to avoid overwriting your existing kubeconfig file.

### Advanced Usage

```bash
# Use a custom kubeconfig file
python3 clustersecret-to-kubeconf.py --context my-cluster --kubeconfig /path/to/custom/config

# Write to your main kubeconfig (use with caution)
python3 clustersecret-to-kubeconf.py --context my-cluster --kubeconfig ~/.kube/config

# Handle development clusters with self-signed certificates
python3 clustersecret-to-kubeconf.py --insecure

# Dry run to see what would be converted
python3 clustersecret-to-kubeconf.py --context my-cluster --dry-run
```

### Examples

1. **List available contexts first:**
   ```bash
   kubectl config get-contexts
   ```

2. **Convert cluster secrets from the current context (writes to ~/.kube/argocd-clusters):**
   ```bash
   python3 clustersecret-to-kubeconf.py
   ```

3. **Convert cluster secrets from a specific context:**
   ```bash
   python3 clustersecret-to-kubeconf.py --context my-argo-cd-cluster
   ```

4. **Convert and write to a custom kubeconfig:**
   ```bash
   python3 clustersecret-to-kubeconf.py --context my-argo-cd-cluster --kubeconfig ~/.kube/argo-clusters
   ```

5. **Use the converted clusters with kubectl:**
   ```bash
   # Use the separate kubeconfig file
   kubectl --kubeconfig ~/.kube/argocd-clusters config get-contexts
   
   # Or set KUBECONFIG environment variable
   export KUBECONFIG=~/.kube/argocd-clusters
   kubectl config get-contexts
   ```

6. **Dry run to preview what will be converted:**
   ```bash
   python3 clustersecret-to-kubeconf.py --dry-run
   ```

## How It Works

1. **Connects to Source Cluster**: Uses the specified kubectl context to connect to the cluster containing Argo CD cluster secrets.

2. **Discovers Cluster Secrets**: Searches for Kubernetes secrets with the label `argocd.argoproj.io/secret-type=cluster` across all namespaces.

3. **Parses Secret Data**: Extracts cluster configuration from the secret's `config` field, which contains:
   - Cluster server URL
   - TLS certificate authority data
   - Client certificate and key data (for mTLS)
   - Username and password (for basic auth)
   - Insecure skip TLS verify flag

4. **Creates Kubeconfig Entries**: For each cluster secret, creates:
   - A cluster entry with server URL and CA certificate
   - A user entry with authentication credentials
   - A context entry linking the cluster and user

5. **Updates Kubeconfig**: Appends the new contexts to a separate kubeconfig file (default: `~/.kube/argocd-clusters`) to avoid overwriting your existing configuration.

## Supported Authentication Methods

The script supports the following authentication methods that Argo CD uses:

- **TLS Client Certificates**: Uses `certData` and `keyData` from the cluster secret
- **Basic Authentication**: Uses `username` and `password` from the cluster secret
- **Certificate Authority**: Uses `caData` for server certificate validation
- **Insecure Connections**: Respects the `insecure` flag for skipping TLS verification

## Output

After successful conversion, you can use kubectl to switch between the converted contexts:

```bash
# List all contexts in the Argo CD kubeconfig
kubectl --kubeconfig ~/.kube/argocd-clusters config get-contexts

# Switch to a converted context
kubectl --kubeconfig ~/.kube/argocd-clusters config use-context <cluster-name>

# Test the connection
kubectl --kubeconfig ~/.kube/argocd-clusters cluster-info

# Or set the KUBECONFIG environment variable for convenience
export KUBECONFIG=~/.kube/argocd-clusters
kubectl config get-contexts
kubectl config use-context <cluster-name>
kubectl cluster-info
```

## Merging with Main Kubeconfig (Optional)

If you want to merge the Argo CD clusters with your main kubeconfig, you can use kubectl's built-in merge functionality:

```bash
# Merge Argo CD clusters into your main kubeconfig
kubectl config view --kubeconfig ~/.kube/argocd-clusters --flatten > /tmp/argocd-clusters.yaml
kubectl config view --flatten > /tmp/main-config.yaml

# Combine them (Argo CD clusters will be appended)
cat /tmp/main-config.yaml /tmp/argocd-clusters.yaml > ~/.kube/config

# Clean up
rm /tmp/argocd-clusters.yaml /tmp/main-config.yaml
```

**Note**: Always backup your existing kubeconfig before merging:
```bash
cp ~/.kube/config ~/.kube/config.backup
```

## Troubleshooting

### No Cluster Secrets Found

If you see "No Argo CD cluster secrets found", check:

1. You're connected to the correct cluster that contains Argo CD
2. Argo CD is properly installed and has cluster secrets
3. You have permissions to read secrets in the Argo CD namespace

### Permission Errors

Ensure you have the necessary RBAC permissions to read secrets:

```bash
kubectl auth can-i get secrets --all-namespaces
```

### TLS Connection Issues

If you encounter SSL/TLS certificate errors when connecting to development clusters:

```bash
# Use the --insecure flag for development clusters with self-signed certificates
python3 clustersecret-to-kubeconf.py --insecure

# Or configure your kubeconfig to handle the certificates properly
kubectl config set-cluster my-cluster --insecure-skip-tls-verify=true
```

### Invalid Cluster Configuration

If some clusters fail to convert, check the Argo CD cluster secret format. The script expects the standard Argo CD cluster secret structure.

## Security Considerations

- The script reads sensitive cluster credentials from Kubernetes secrets
- Generated kubeconfig files contain authentication credentials
- Ensure proper file permissions on your kubeconfig file
- Consider using separate kubeconfig files for different environments

## Contributing

This script is part of the argocd-agent project. Please follow the project's contribution guidelines when making changes. 