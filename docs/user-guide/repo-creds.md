# Repository Credential Templates

This document explains how Argo CD `Repository Credential Templates` (repo-creds) are synchronized between the principal and agents.

## Overview

Repository credential templates allow you to define credentials once and have them automatically applied to any repository matching a URL pattern. In Argo CD Agent, repo-creds are stored as Kubernetes Secrets with the label `argocd.argoproj.io/secret-type: repo-creds`.

Repo-creds follow the same synchronization model as [repository secrets](repository.md):

- **Managed agents**: Repo-creds are created on the control plane and distributed to agents based on AppProject matching.
- **Autonomous agents**: Repo-creds are created and managed locally on the workload cluster.

## Creating Repo-Creds (Managed Mode)

Requirements for a repo-creds secret to be reconciled by the principal:

- The secret has label `argocd.argoproj.io/secret-type: repo-creds`
- The secret contains a non-empty `project` field in `.data`/`stringData`
- The secret resides in the Argo CD installation namespace (e.g., `argocd`)

### Example

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: github-creds
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repo-creds
type: Opaque
stringData:
  type: git
  url: https://github.com/myorg
  username: deploy-user
  password: ghp_xyz789token
  project: my-project  # Must reference an existing AppProject
```

Any repository URL starting with `https://github.com/myorg` will automatically use these credentials on agents matching the AppProject's patterns.

The distribution logic (AppProject matching, agent pattern matching) is identical to repository secrets. See the [Repository Management](repository.md) and [AppProjects](appprojects.md) guides for details.

## Lifecycle

- **Creation**: Repo-creds created on the principal are sent to matching agents
- **Updates**: Changes are propagated to all agents that received them
- **Deletion**: Deleting repo-creds on the principal removes them from all agents
- **Project Update**: Agents that no longer match updated AppProject patterns have repo-creds removed
- **Project Deletion**: Deleting an AppProject removes all associated repo-creds from agents

## Repo-Creds vs Repository Secrets

| Aspect | Repository Secret | Repo-Creds |
|--------|------------------|------------|
| Label | `secret-type: repository` | `secret-type: repo-creds` |
| Scope | Specific repository URL | URL prefix pattern |
| Use case | Credentials for a single repo | Credentials for all repos under an org or host |

Both types follow identical synchronization and lifecycle behavior.

## Troubleshooting

If repo-creds are not appearing on managed agents, verify:

1. The secret has label `argocd.argoproj.io/secret-type: repo-creds`
2. The `project` field is set and references an existing AppProject
3. The AppProject patterns match the agent name (check both `.spec.destinations` and `.spec.sourceNamespaces`)
4. The agent is connected (check principal and agent logs)

See the [Repository Management troubleshooting](repository.md#troubleshooting) section for detailed steps.
