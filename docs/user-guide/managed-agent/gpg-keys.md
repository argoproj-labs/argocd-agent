# GPG Key Synchronization

This document explains how GPG public keys are synchronized between the control-plane and managed clusters to enable Git commit signature verification.

## Overview

Argo CD supports verifying Git commits using GPG (GnuPG). In argocd-agent, GPG public keys are stored in the `argocd-gpg-keys-cm` ConfigMap on the control-plane and automatically propagated to managed agents. This enables centralized management of GPG keys for commit signature verification across all managed clusters.

GPG key synchronization varies by agent mode:

- **Managed agents**: GPG keys are created on the control-plane and automatically distributed to all connected managed agents.
- **Autonomous agents**: GPG key synchronization is not supported for autonomous agents and they must manage their own GPG keys locally.

## Adding GPG Keys

GPG public keys are added on the control-plane using the Argo CD CLI or the UI.

### Using the CLI

```bash
# Export your GPG public key
gpg --armor --export <KEY_ID> > path/to/gpg

# Login to Argo CD on the principal
argocd login <CONTROL_PLANE_ARGOCD_SERVER_URL> --username admin --password <ADMIN_PASSWORD> --insecure

# Add the GPG public key
argocd gpg add --from path/to/gpg

# Verify the key was added
argocd gpg list
```

### Using the UI

1. Navigate to **Settings** → **GnuPG keys** in the Argo CD web UI on the control-plane
2. Click **+ Add GnuPG key**
3. Paste the ASCII-armored public key block (the output of `gpg --armor --export <KEY_ID>`) into the **GnuPG public key data** field
4. Click **Create**

The argocd-agent principal automatically detects the change and pushes it to all connected managed agents.

**Note:** Manual changes to the `argocd-gpg-keys-cm` ConfigMap on a managed agent are automatically reverted to match the principal's version. The agent tracks the principal as the authoritative source using the `argocd.argoproj.io/source-uid` annotation.

## Configuring Signature Verification

Configure an AppProject to require signature verification:

```bash
argocd proj set my-project --signature-keys <KEY_ID>
```

Or apply a YAML manifest:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: my-project
  namespace: argocd
spec:
  signatureKeys:
    - keyID: "<KEY_ID>"
  sourceRepos:
    - '*'
  destinations:
    - namespace: '*'
      server: '*'
```

The AppProject is also synced to managed agents by the existing argocd-agent sync mechanism. Applications in this project will then require signed Git commits.

## Verifying GPG Key Synchronization

After adding a GPG key on the control-plane, verify that it has been synced to your managed agents:

```bash
# Confirm the key exists on the control-plane
argocd gpg list

# Check the ConfigMap on a managed agent cluster
kubectl get configmap argocd-gpg-keys-cm -n argocd --context <AGENT_CONTEXT> -o jsonpath='{.data}' | jq 'keys'
```

The key IDs listed on the agent should match those on the control-plane.

To verify that signature verification is working, deploy an Application:

- An Application pointing to a commit signed with a valid key should sync successfully.
- An Application pointing to an unsigned commit or a commit signed with an invalid key should fail to sync.
