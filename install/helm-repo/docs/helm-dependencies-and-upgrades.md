# Helm chart dependencies and upgrading the Argo CD subchart

This page is for **maintainers** who change the `argocd-agent-agent` chart or bump the optional **argo-cd** (argo-helm) dependency. End users who only install the chart from a registry do not need these steps.

## How the Argo CD subchart is pinned

- The chart declares an optional dependency on [argo-helm](https://github.com/argoproj/argo-helm) `argo-cd`, with alias `argocd`, gated by `argoCD.enabled` in `values.yaml`.
- The **Helm chart version** (for example `9.5.0`) is chosen to match the **Argo CD application version** in `go.mod` (`github.com/argoproj/argo-cd/v3`). That mapping comes from the public argo-helm index (same `app_version` as the Go module tag).
- `Chart.yaml` annotations record both the module tag and the resolved chart version for quick review.

## Scripts and Makefile targets

| Command | Purpose |
|--------|---------|
| `hack/sync-argo-cd-helm-chart-version.sh` | Reads `go.mod`, queries argo-helm, updates `Chart.yaml` dependency `version` and related annotations (default). Requires `helm`, `jq`, and `yq`. |
| `hack/sync-argo-cd-helm-chart-version.sh --check` | Fails if `Chart.yaml` is out of sync with `go.mod` (used in CI via `validate-helm-chart`). |
| `hack/sync-argo-cd-helm-chart-version.sh --print-chart-version` | Prints only the resolved argo-helm chart version (no file writes). |
| `make helm-dependency-update` | Runs `hack/helm-dependency-update.sh`: registers/updates the `argo` repo, then `helm dependency update` in `install/helm-repo/argocd-agent-agent`. Prefer this over raw `helm dependency update` when the argo-helm index is not cached yet. |
| `make validate-helm-chart` | Runs `hack/validate-helm-chart.sh`: sync `--check`, `helm dependency build`, `helm lint`, and `helm template` smoke renders (`argoCD.enabled` true and false). Requires network for the sync check unless indexes are cached. |
| `make validate-values-schema` | Ensures `values.yaml` keys are reflected in `values.schema.json` (unrelated to argo-cd version, but required for chart PRs). |

## Upgrade workflow after changing Argo CD in `go.mod`

When you bump `github.com/argoproj/argo-cd/v3` in `go.mod` (or otherwise need a new argo-helm chart):

1. **Sync `Chart.yaml`** from the module version:

   ```bash
   ./hack/sync-argo-cd-helm-chart-version.sh
   ```

2. **Refresh vendored dependency artifacts** (from repo root):

   ```bash
   make helm-dependency-update
   ```

   This updates `Chart.lock` and `install/helm-repo/argocd-agent-agent/charts/argo-cd-*.tgz`.

3. **Validate**:

   ```bash
   make validate-helm-chart
   make validate-values-schema
   ```

4. **Commit** the changed `Chart.yaml`, `Chart.lock`, and `charts/*.tgz` (if your release process vendors the tarball).

5. **Review** subchart defaults: if upstream `argo-cd` values changed, adjust the optional `argocd:` defaults in `values.yaml` or document new required values for consumers who set `argoCD.enabled=true`.

## Notes

- **Helm version**: Use a current Helm 3.7+ or Helm 4.x CLI. Older clients may struggle with URL-only dependencies; `make helm-dependency-update` mitigates that by always adding the `argo` repo.
- **Air-gapped builds**: You need the argo-helm chart `.tgz` available; commit `charts/` and `Chart.lock`, and use `helm dependency build` instead of `update` where network is unavailable.
- **Chart app version vs dependency**: Bumping the agent’s own `appVersion` or `version` in `Chart.yaml` is independent of the argo-cd subchart; only the `go.mod` Argo CD line drives the argo-helm pin via the sync script.
