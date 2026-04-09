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

# Run `helm dependency update` for install/helm-repo/argocd-agent-agent.
#
# Ensures the argo-helm index is available (helm repo add/update). This avoids
# failures on Helm versions or environments where an unmanaged repository URL
# is not fetched until the index is cached.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "$0")" >/dev/null 2>&1 && pwd -P)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CHART_DIR="${REPO_ROOT}/install/helm-repo/argocd-agent-agent"
ARGO_HELM_REPO_NAME="argo"
ARGO_HELM_REPO_URL="https://argoproj.github.io/argo-helm"

if ! command -v helm >/dev/null 2>&1; then
	echo "Error: helm is not installed or not in PATH" >&2
	exit 1
fi

helm version

helm repo add "${ARGO_HELM_REPO_NAME}" "${ARGO_HELM_REPO_URL}" --force-update
helm repo update "${ARGO_HELM_REPO_NAME}"

cd "${CHART_DIR}"
exec helm dependency update "$@"
