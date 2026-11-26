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

In autonomous mode, repository secrets are created and managed **locally on the workload cluster**. Repository credentials remain completely isolated to each agent cluster with no synchronization to the principal. Note that syncing from the control plane may still require a credential on the control plane; see [Syncing from the control plane](#syncing-from-the-control-plane).

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

## Syncing from the control plane

Applications on autonomous agents can be synced from the control plane (Argo CD UI, `argocd app sync` or the API). Before Argo CD's API server forwards the sync, it resolves the requested revision (a branch, tag or short commit SHA) to a commit SHA. It does this on the control plane, with the control plane's repo-server and the control plane's repository credentials. This happens before the operation reaches the principal or the agent.

Because repository secrets on autonomous agents are not synchronized to the principal, syncing an Application that uses a private repository fails if the control plane has no credential for that repository:

```
rpc error: code = FailedPrecondition desc = error resolving repo revision: rpc error: code = Unknown desc = failed to list refs: authentication required: Repository not found
```

Refresh and hard refresh from the control plane are not affected. Syncing on the workload cluster itself is not affected either.

### Providing a credential on the control plane

To sync private repositories from the control plane, create a repository secret or repo-creds template in the Argo CD namespace on the **control plane** cluster. It is used only to resolve revisions, so read-only access to the repository is enough. The credential is not sent to the agent; the agent keeps using its own credentials to fetch and render manifests.

If you scope a repository secret to a project, use the project name **as it appears on the principal**, which is prefixed with the agent name (`<agent-name>-<project-name>`, see [AppProject Synchronization](appprojects-autonomous-mode.md)). A secret scoped to the agent-side project name does not apply to the Application on the principal.

For example, for an AppProject `my-project` on the agent `agent-production`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: frontend-repo
  namespace: argocd  # on the control plane cluster
  labels:
    argocd.argoproj.io/secret-type: repository
type: Opaque
stringData:
  type: git
  url: https://github.com/myorg/frontend-app.git
  username: read-only-user
  password: <read-only token>
  project: agent-production-my-project  # optional; must use the principal-side name
```

A repository secret without a `project` field, or a repo-creds template whose URL prefix matches the repository, also works. A repo-creds template is only used when the matching repository secret has no credentials of its own. If a repository secret for the same URL already has credentials (for example, an expired token), Argo CD uses those instead, so update or remove that secret.

### Syncing to a full commit SHA

If you don't want any repository credentials on the control plane, you can still sync by passing a full 40-character commit SHA as the revision, for example `argocd app sync <agent-name>/<app-name> --revision <full-commit-sha>`. Argo CD does not contact the repository to resolve a full commit SHA for Git sources. Branch names, tags and short SHAs still require a credential on the control plane.

This workaround has some limits:

- **Automated sync**: if the Application has automated sync enabled, Argo CD rejects a revision that differs from the Application's `targetRevision`. This also applies when the control plane has a credential. To sync to a specific commit, set `targetRevision` to the full commit SHA on the agent. To sync the current `targetRevision`, provide a credential on the control plane and sync without a revision override.
- **Multi-source Applications**: the CLI does not accept `--revision` for multi-source Applications. Use `--revisions` together with `--source-positions` for each source, for example `argocd app sync <agent-name>/<app-name> --revisions <full-commit-sha> --source-positions 1`. Every private source whose revision is a branch, tag or short SHA needs either its own full-SHA override or a credential on the control plane.

## More Information

For more information about Argo CD Agent configuration and other features, see:

- [Agent Configuration Reference](../../configuration/reference/agent.md)
- [Principal Configuration Reference](../../configuration/reference/principal.md)
- [Application Management](applications-autonomous-mode.md)
- [AppProject Synchronization](appprojects-autonomous-mode.md)
