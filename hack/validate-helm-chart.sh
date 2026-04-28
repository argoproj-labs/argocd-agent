#!/usr/bin/env bash
# Copyright 2025 The argocd-agent Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "$0")" >/dev/null 2>&1 && pwd -P)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CHART_DIR="${REPO_ROOT}/install/helm-repo/argocd-agent-agent"
SYNC_SCRIPT="${SCRIPT_DIR}/sync-argo-cd-helm-chart-version.sh"

# Fail if the argo-cd Helm dependency in Chart.yaml does not match
# github.com/argoproj/argo-cd/v3 in go.mod (resolved via argo-helm index).
# When the check passes, we continue; on mismatch the sync script exits 1 with a fix hint.
if ! command -v helm >/dev/null 2>&1; then
	echo "Error: helm is required in PATH" >&2
	exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
	echo "Error: jq is required in PATH (argo-cd chart vs go.mod check)" >&2
	exit 1
fi
if ! command -v yq >/dev/null 2>&1; then
	echo "Error: yq is required in PATH (argo-cd chart vs go.mod check)" >&2
	exit 1
fi
if [[ ! -f "${SYNC_SCRIPT}" ]]; then
	echo "Error: missing ${SYNC_SCRIPT}" >&2
	exit 1
fi

"${SYNC_SCRIPT}" --check

cd "$CHART_DIR"
helm dependency build
helm lint .
helm template test-release . --namespace argocd >/dev/null
helm template test-release . --namespace argocd --set argoCD.enabled=true >/dev/null

echo "✓ Helm chart lint and template checks passed"
