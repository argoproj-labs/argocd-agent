# Developing the agent Helm chart

This guide is for contributors changing the `argocd-agent-agent` chart under `install/helm-repo/argocd-agent-agent/`.

## Bump the chart version

**Any change to the chart** (templates, `values.yaml`, `values.schema.json`, `Chart.yaml`, tests, and so on) must include a **strictly higher** `version` in `install/helm-repo/argocd-agent-agent/Chart.yaml`.

- The new version must be **greater than** the value on the PR target branch (for example `0.2.0` → `0.2.1`, `0.3.0`, or `1.0.0`).
- Leaving `version` unchanged or lowering it will fail CI.

`appVersion` tracks the Argo CD Agent release; update it only when you intend to ship a different agent image. It is separate from the chart `version` bump required for chart changes.

### Example

```yaml
# install/helm-repo/argocd-agent-agent/Chart.yaml
version: 0.2.1   # was 0.2.0 on main
```

## Local checks

Before opening a pull request:

```bash
make validate-values-schema
make generate-helm-docs   # updates argocd-agent-agent/README.md from values.yaml
```

CI runs schema validation, helm-docs, and the chart version check on pull requests that touch `install/helm-repo/argocd-agent-agent/**`.

## See also

- [Installation and configuration](install-agent.md) — installing the published chart from GHCR
