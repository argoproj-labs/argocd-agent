# Ignore sync label

The ignore sync label prevents specific `Application`, `AppProject`, and repository (Repository `Secret`) resources from being synchronized between the principal and agents, so those resources can exist on only one side.

Ignore sync is supported by both managed mode and autonomous mode agents, and largely functions the same between the two modes.

## Label details

- **Label key**: `argocd-agent.argoproj-labs.io/ignore-sync`
- **Label value**: `"true"` (must be exactly that string, case-sensitive—`"TRUE"`, `"True"`, `"false"`, or empty values do not enable ignore sync)

## Applications

Use the label on an `Application` when that app should stay on the principal only (managed mode) or on the agent only (autonomous mode).

### Example: managed mode

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: app
  namespace: production-cluster
  labels:
    argocd-agent.argoproj-labs.io/ignore-sync: "true"  # Skip sync to agent
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: guestbook
  destination:
    server: https://kubernetes.default.svc
    namespace: guestbook
```

This Application stays on the principal cluster and is not sent to the `production-cluster` agent, even though it is created in that agent’s namespace. 

When the label is applied on an autonomous agent Application, this Application likewise stays on the autonomous agent cluster and is not synchronized back to the principal.

The label doesn't override the normal mapping mode rules — an Application must still be in an allowed namespace for its target agent to be considered for processing in the first place. If you remove the label from an existing Application, it resumes synchronizing according to the normal rules for its agent mode.

### Use cases (Applications)

- **Principal-only**: Control-plane or infrastructure apps that should not run on agents
- **Agent-only**: Local utilities or monitoring specific to a workload cluster
- **Temporary isolation**: Maintenance or testing without pulling apps across the link
- **Staged rollouts**: Choose which Applications sync during gradual agent rollouts

## AppProjects

Use the label on an `AppProject` when that project should remain on the principal only (managed mode) or on the agent only (autonomous mode).

### Example: managed mode

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: principal-only-project
  namespace: argocd
  labels:
    argocd-agent.argoproj-labs.io/ignore-sync: "true"  # Skip sync to agents
spec:
  destinations:
  - name: "in-cluster"
    namespace: "*"
    server: "https://kubernetes.default.svc"
  sourceNamespaces:
  - argocd
  sourceRepos:
  - "*"
```

This AppProject remains on the principal and is not distributed to agents, even when `sourceNamespaces` and `destinations` would normally match.

If you remove the label from an existing AppProject, it resumes synchronizing according to the normal rules for its agent mode.

### Use cases (AppProjects)

- **Principal-only**: Permissions or projects for control-plane operations
- **Administrative**: Cluster management projects that should not be distributed
- **Agent-only**: Workload-cluster-specific project definitions
- **Temporary isolation**: Maintenance, testing, or phased rollouts

## Repository secrets

The label applies to repository **Secret** resources (Argo CD repo configs). Secrets labeled with `argocd-agent.argoproj-labs.io/ignore-sync: "true"` are not synchronized between principal and agents.

### Example

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: private-repo
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repository
    argocd-agent.argoproj-labs.io/ignore-sync: "true"  # Skip sync
data:
  project: <base64-encoded-project-name>
  url: <base64-encoded-repo-url>
  # ... other repository configuration
```

This is useful for repositories that should only be available on specific clusters, such as:

- **Principal-only repositories**: Internal configuration repositories
- **Agent-only repositories**: Cluster-specific private repositories
- **Environment-specific repositories**: Repositories containing sensitive configuration for specific environments

