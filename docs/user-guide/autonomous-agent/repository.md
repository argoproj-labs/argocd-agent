# Repository Management (autonomous agents)

This document explains how Argo CD `Repository` secrets and `Repository Credential Templates` (repo-creds) are synchronized between the principal (control plane) and agents (workload clusters).

## Overview

In Argo CD Agent, two types of secrets govern Git repository access:

- **Repository secrets** (`argocd.argoproj.io/secret-type: repository`): credentials scoped to a single repository URL.
- **Repository credential templates** (`argocd.argoproj.io/secret-type: repo-creds`): credentials applied automatically to any repository whose URL matches a given prefix, useful for granting access to all repositories under an organisation or host in a single secret.

With **autonomous agents**, secrets are created and managed locally on the workload cluster; they are not synced back to the principal.

| Aspect | Repository Secret | Repo-Creds |
|--------|------------------|------------|
| Label | `secret-type: repository` | `secret-type: repo-creds` |
| Scope | Specific repository URL | URL prefix pattern |
| Use case | Credentials for a single repo | Credentials for all repos under an org or host |

## Repository Secrets (Autonomous mode)

In autonomous mode, repository secrets are created and managed **locally on the workload cluster**. Repository credentials remain completely isolated to each agent cluster with no synchronization to the principal.

### Creating Repositories

Repository secrets are created directly in the argocd installation namespace on the autonomous agent cluster. These repositories are immediately available to local Argo CD Applications and do not require project scoping for basic functionality.

#### Local Repository Management

Autonomous agents handle repository secrets entirely within their local cluster:

1. **Local Creation**: Repository secrets are created directly on the agent cluster
2. **Immediate Availability**: Repositories are immediately usable by local Argo CD Applications
3. **No Distribution**: Repositories remain isolated to the specific agent cluster
4. **Independent Management**: Each agent manages its own set of repository credentials

### Example: Creating a Repository on an Autonomous Agent

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: frontend-repo
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repository
type: Opaque
stringData:
  type: git
  url: https://github.com/myorg/frontend-app.git
  username: deploy-user
  password: ghp_xyz789token
  # Note: project field optional for autonomous agents
  # Only needed if associating with local AppProjects
```

### Local Project Association

While not required for basic functionality, repositories on autonomous agents can still be associated with local AppProjects:

```yaml
stringData:
  # ... other repository fields
  project: local-frontend-project  # References local AppProject
```

### Repository Lifecycle in Autonomous Mode

- **Creation**: Create repository secrets directly on the agent cluster
- **Updates**: Modify repository secrets on the agent cluster; changes take effect immediately
- **Deletion**: Delete repository secrets on the agent cluster; Applications using the repository will lose access
- **Isolation**: Repository changes on one autonomous agent do not affect other agents or the principal

### Security Considerations for Autonomous Agents

Since repository credentials remain local to each agent cluster:

1. **Credential Isolation**: Each agent can use different credentials for the same repository
2. **Independent Rotation**: Repository credentials can be rotated independently on each agent
3. **Local RBAC**: Repository access is controlled entirely by local Kubernetes RBAC
4. **No Central Visibility**: Principal cluster has no visibility into autonomous agent repository configurations

!!! note "Repository Independence"
    Repository credentials on autonomous agents are completely independent. The same repository URL can use different credentials on different agent clusters.

## Repository Credential Templates

Repository credential templates (repo-creds) let you define credentials once and have them automatically applied to any repository whose URL starts with a given prefix. This is useful when you manage many repositories under the same organisation or host and want to avoid duplicating credentials.

Repo-creds are stored as Kubernetes Secrets with the label `argocd.argoproj.io/secret-type: repo-creds` and follow the same synchronization model as repository secrets.

### Creating Repo-Creds (Autonomous Mode)

For autonomous agents, repo-creds are created and managed locally on the workload cluster. They follow the same steps and patterns described in [Repository Secrets](#repository-secrets-autonomous-mode) above.

## More Information

For more information about Argo CD Agent configuration and other features, see:

- [Agent Configuration Reference](../../configuration/reference/agent.md)
- [Principal Configuration Reference](../../configuration/reference/principal.md)
- [Application Management](applications-autonomous-mode.md)
- [AppProject Synchronization](appprojects-autonomous-mode.md)
