# Repository Management

This document explains how Argo CD `Repository` secrets and `Repository Credential Templates` (repo-creds) are synchronized between the principal (control plane) and agents (workload clusters).

## Overview

In Argo CD Agent, two types of secrets govern Git repository access:

- **Repository secrets** (`argocd.argoproj.io/secret-type: repository`): credentials scoped to a single repository URL.
- **Repository credential templates** (`argocd.argoproj.io/secret-type: repo-creds`): credentials applied automatically to any repository whose URL matches a given prefix, useful for granting access to all repositories under an organisation or host in a single secret.

Both types follow the same synchronization model based on agent mode:

- **Managed agents**: Secrets are created on the control plane and distributed to agents based on AppProject configuration and agent matching patterns.
- **Autonomous agents**: Secrets are created and managed locally on the workload cluster; they are not synced back to the principal.

| Aspect | Repository Secret | Repo-Creds |
|--------|------------------|------------|
| Label | `secret-type: repository` | `secret-type: repo-creds` |
| Scope | Specific repository URL | URL prefix pattern |
| Use case | Credentials for a single repo | Credentials for all repos under an org or host |

## Repository Secrets

### Managed Agent Mode

In managed mode, repository secrets are created on the **control plane** and automatically distributed to workload clusters based on AppProject configuration and agent matching patterns. Only project-scoped repositories are reconciled by the principal.

#### Creating Repositories

Repository secrets must be created in the argocd installation namespace on the control plane cluster with proper project association to enable distribution to managed agents.

Requirements for a repository secret to be reconciled by the principal:

- The secret has label `argocd.argoproj.io/secret-type: repository`
- The secret contains a non-empty `project` field in `.data`/`stringData`
- The secret resides in the Argo CD installation namespace (e.g., `argocd`)

#### Repository-to-Agent Distribution Logic

The principal distributes a repository secret to a managed agent using a **two-step matching process**:

1. **Project Association**: Repository must include a `project` field referencing an existing AppProject
2. **Pattern Matching**: Agent name must match **both**:
   - At least one destination pattern via `.spec.destinations` (either `name` or a `server` URL that includes `?agentName=<pattern>`; `*` is allowed)  
   - At least one pattern in the AppProject's `.spec.sourceNamespaces` fields

Learn more about AppProject matching logic in the [AppProjects guide](./appprojects.md).

#### Example: Repository Distribution Setup

First, create an AppProject that defines which agents should receive repositories:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: backend-services
  namespace: argocd
spec:
  # Agents matching these patterns can source Applications
  sourceNamespaces:
  - "backend-*"
  - "api-*"
  destinations:
  # Applications can deploy to these agent clusters
  - name: "backend-*"
    namespace: "production"
    server: "*"
  - name: "api-*" 
    namespace: "staging"
    server: "*"
  sourceRepos:
  - "https://github.com/myorg/*"
```

Then create a repository secret associated with this project:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: backend-repo
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repository
type: Opaque
stringData:
  type: git
  url: https://github.com/myorg/backend-services.git
  project: backend-services  # Associates with AppProject above
```

This repository will be distributed to agents named:

- `backend-prod` (matches `backend-*` in both sourceNamespaces and destinations)
- `backend-staging` (matches `backend-*` in both sourceNamespaces and destinations)
- `api-production` (matches `api-*` in both sourceNamespaces and destinations)

But **not** to agents like:

- `frontend-prod` (doesn't match any patterns above)
- `ops-eu` (doesn't match any patterns above)

#### Agent-Side Repository Processing

When a repository secret is sent to a managed agent, it undergoes processing:

1. **Local Creation**: Repository secret is created in the agent's argocd installation namespace
2. **Metadata Preservation**: Original repository configuration (URL, credentials, etc.) is preserved
3. **Project Context**: Repository remains associated with the original project name
4. **Source UID Tracking**: Agent adds a source UID annotation to track the principal as source
5. **Change Reconciliation**: Manual changes to managed repositories on agents are reverted to match the principal

#### Repository Lifecycle in Managed Mode

- **Creation**: Repository secrets created on the principal are automatically sent to matching agents
- **Updates**: Changes to repository secrets on the principal are propagated to all agents that received them
- **Deletion**: Deleting a repository secret on the principal removes it from all agents
- **Project Changes**: Updating AppProject patterns can change which agents receive repositories:
  - New matching agents receive the repository
  - Agents that no longer match will have the repository **removed automatically**

!!! note "Automatic Cleanup"
    When AppProject patterns change and an agent no longer matches, repositories are automatically removed from that agent on the next sync.

!!! note "Pattern Matching"
    Agent matching uses glob patterns and supports both destination `name` and destination `server` (with `?agentName=`). Deny patterns prefixed with `!` are supported.
    Common examples:
    - `*` - matches all agents
    - `prod-*` - matches agents starting with "prod-"
    - `!dev-*` - excludes agents starting with "dev-"

### Autonomous Agent Mode

In autonomous mode, repository secrets are created and managed **locally on the workload cluster**. Repository credentials remain completely isolated to each agent cluster with no synchronization to the principal.

#### Creating Repositories for Autonomous Agents

Repository secrets are created directly in the argocd installation namespace on the autonomous agent cluster. These repositories are immediately available to local Argo CD Applications and do not require project scoping for basic functionality.

#### Local Repository Management

Autonomous agents handle repository secrets entirely within their local cluster:

1. **Local Creation**: Repository secrets are created directly on the agent cluster
2. **Immediate Availability**: Repositories are immediately usable by local Argo CD Applications
3. **No Distribution**: Repositories remain isolated to the specific agent cluster
4. **Independent Management**: Each agent manages its own set of repository credentials

#### Example: Creating a Repository on an Autonomous Agent

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

#### Local Project Association

While not required for basic functionality, repositories on autonomous agents can still be associated with local AppProjects:

```yaml
stringData:
  # ... other repository fields
  project: local-frontend-project  # References local AppProject
```

#### Repository Lifecycle in Autonomous Mode

- **Creation**: Create repository secrets directly on the agent cluster
- **Updates**: Modify repository secrets on the agent cluster; changes take effect immediately
- **Deletion**: Delete repository secrets on the agent cluster; Applications using the repository will lose access
- **Isolation**: Repository changes on one autonomous agent do not affect other agents or the principal

#### Security Considerations for Autonomous Agents

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

### Creating Repo-Creds (Managed Mode)

Requirements for a repo-creds secret to be reconciled by the principal:

- The secret has label `argocd.argoproj.io/secret-type: repo-creds`
- The secret contains a non-empty `project` field in `.data`/`stringData`
- The secret resides in the Argo CD installation namespace (e.g., `argocd`)

#### Example

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

The distribution logic (AppProject matching, agent pattern matching) is identical to repository secrets — refer to the [Repository-to-Agent Distribution Logic](#repository-to-agent-distribution-logic) section above for details.

### Creating Repo-Creds (Autonomous Mode)

For autonomous agents, repo-creds are created and managed locally on the workload cluster. They follow the same steps and patterns described in [Autonomous Agent Mode](`#autonomous-agent-mode`) above.

### Repo-Creds Lifecycle

- **Creation**: Repo-creds created on the principal are sent to matching agents
- **Updates**: Changes are propagated to all agents that received them
- **Deletion**: Deleting repo-creds on the principal removes them from all agents
- **Project Update**: Agents that no longer match updated AppProject patterns have repo-creds removed
- **Project Deletion**: Deleting an AppProject removes all associated repo-creds from agents

## Troubleshooting

### Repository or Repo-Creds Not Distributed to Agents (Managed Mode)

If a repository or repo-creds secret created on the principal doesn't appear on managed agents:

#### Check Label, Namespace and Project Association

Ensure the secret:

- Has the correct label (`argocd.argoproj.io/secret-type=repository` or `argocd.argoproj.io/secret-type=repo-creds`)
- Lives in the Argo CD namespace on the principal (e.g., `argocd`)
- Has a valid, non-empty `project` field:

```bash
# Check if project field exists
kubectl get secret my-repo -n argocd -o jsonpath='{.data.project}' | base64 -d

# If no output, the secret is missing the project field
# Add it using:
kubectl patch secret my-repo -n argocd --type merge -p '{
  "stringData": {
    "project": "my-project-name"
  }
}'
```

#### Verify AppProject Configuration

Check that the AppProject exists and has correct patterns:

```bash
# Check specific AppProject configuration
kubectl get appproject my-project -n argocd -o yaml
```

Verify that:

- The AppProject referenced by the secret exists
- Agent names match patterns in both `.spec.destinations` (either `name` or `server` with `?agentName=`) and `.spec.sourceNamespaces`
- Patterns use correct glob syntax, including any deny patterns (`!pattern`) you intend

#### Check Principal Logs

Look for repository processing messages on the principal:

```bash
kubectl logs -n argocd deployment/argocd-agent-principal
```

#### Verify Agent Connectivity

Ensure agents are connected and processing events:

```bash
kubectl logs -n argocd deployment/argocd-agent
```

#### Stale Repository Data

If agents have outdated repository information:

```bash
# Check source UID annotation on agent
kubectl get secret my-repo -n argocd -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/source-uid}'

# Restart agent to force resync
kubectl rollout restart -n argocd deployment/argocd-agent
```

### Migration and Recovery

#### Recovering from Principal Loss

If the principal cluster is lost, autonomous agents continue working normally. For managed agents:

1. Repositories remain functional on agents until credentials expire
2. New principal cluster needs repository secrets recreated
3. Agents will reconnect and receive repositories based on AppProject patterns

For more information about Argo CD Agent configuration and other features, see:

- [Agent Configuration Reference](../configuration/reference/agent.md)
- [Principal Configuration Reference](../configuration/reference/principal.md)
- [Application Management](applications.md)
- [AppProject Synchronization](appprojects.md)
