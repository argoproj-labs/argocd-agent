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

# Resolve the argo-cd Helm chart version from the Argo CD module version in
# go.mod (github.com/argoproj/argo-cd/v3) and either write it to Chart.yaml or
# verify it matches (--check).

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "$0")" >/dev/null 2>&1 && pwd -P)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GOMOD="${REPO_ROOT}/go.mod"
CHART_YAML="${REPO_ROOT}/install/helm-repo/argocd-agent-agent/Chart.yaml"

usage() {
	echo "Usage: $0 [--check] [--print-chart-version]" >&2
	echo "  (default)     Update argo-cd dependency version in Chart.yaml from go.mod + argo-helm index" >&2
	echo "  --check       Exit 1 if Chart.yaml does not match the resolved chart version" >&2
	echo "  --print-chart-version  Print only the resolved Helm chart version (no writes)" >&2
	exit 1
}

CHECK=false
PRINT_ONLY=false
while [[ $# -gt 0 ]]; do
	case "$1" in
	--check)
		CHECK=true
		shift
		;;
	--print-chart-version)
		PRINT_ONLY=true
		shift
		;;
	-h | --help)
		usage
		;;
	*)
		echo "Unknown option: $1" >&2
		usage
		;;
	esac
done

if ! command -v helm >/dev/null 2>&1; then
	echo "Error: helm is required in PATH" >&2
	exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
	echo "Error: jq is required in PATH" >&2
	exit 1
fi
if ! command -v yq >/dev/null 2>&1; then
	echo "Error: yq is required in PATH" >&2
	exit 1
fi

if [[ ! -f "$GOMOD" ]]; then
	echo "Error: go.mod not found at $GOMOD" >&2
	exit 1
fi
if [[ ! -f "$CHART_YAML" ]]; then
	echo "Error: Chart.yaml not found at $CHART_YAML" >&2
	exit 1
fi

ARGOCD_MODULE_VER="$(awk '/github.com\/argoproj\/argo-cd\/v3 / { print $2; exit }' "$GOMOD")"
if [[ -z "${ARGOCD_MODULE_VER}" ]]; then
	echo "Error: could not find github.com/argoproj/argo-cd/v3 in ${GOMOD}" >&2
	exit 1
fi

helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
helm repo update argo >/dev/null

# Pick the newest chart whose app_version matches the go module tag (e.g. v3.3.6).
RESOLVED_CHART_VER="$(
	helm search repo argo/argo-cd --versions --output json |
		jq -r --arg app "${ARGOCD_MODULE_VER}" '
			first(.[] | select(.app_version == $app)) | .version // empty
		'
)"

if [[ -z "${RESOLVED_CHART_VER}" ]]; then
	echo "Error: no argo/argo-cd chart found for app_version ${ARGOCD_MODULE_VER}" >&2
	exit 1
fi

if [[ "${PRINT_ONLY}" == true ]]; then
	echo "${RESOLVED_CHART_VER}"
	exit 0
fi

CURRENT_VER="$(yq eval '.dependencies[] | select(.name == "argo-cd") | .version' "$CHART_YAML")"
if [[ "${CURRENT_VER}" == "null" || -z "${CURRENT_VER}" ]]; then
	echo "Error: no argo-cd dependency found in ${CHART_YAML}" >&2
	exit 1
fi

if [[ "${CHECK}" == true ]]; then
	if [[ "${CURRENT_VER}" != "${RESOLVED_CHART_VER}" ]]; then
		echo "Error: Chart.yaml pins argo-cd chart version ${CURRENT_VER} but go.mod ${ARGOCD_MODULE_VER} resolves to chart ${RESOLVED_CHART_VER}" >&2
		echo "Run: hack/sync-argo-cd-helm-chart-version.sh && (cd install/helm-repo/argocd-agent-agent && helm dependency update)" >&2
		exit 1
	fi
	ANN="$(yq eval '.annotations."argocd-agent.argoproj-labs.io/argo-cd-module-version" // ""' "$CHART_YAML")"
	if [[ "${ANN}" != "${ARGOCD_MODULE_VER}" ]]; then
		echo "Error: annotation argocd-agent.argoproj-labs.io/argo-cd-module-version is \"${ANN}\", expected \"${ARGOCD_MODULE_VER}\"" >&2
		exit 1
	fi
	CHART_ANN="$(yq eval '.annotations."argocd-agent.argoproj-labs.io/argo-cd-chart-version" // ""' "$CHART_YAML")"
	if [[ "${CHART_ANN}" != "${RESOLVED_CHART_VER}" ]]; then
		echo "Error: annotation argocd-agent.argoproj-labs.io/argo-cd-chart-version is \"${CHART_ANN}\", expected \"${RESOLVED_CHART_VER}\"" >&2
		exit 1
	fi
	echo "✓ argo-cd Helm chart version matches go.mod (${RESOLVED_CHART_VER} for ${ARGOCD_MODULE_VER})"
	exit 0
fi

yq eval -i "(.dependencies[] | select(.name == \"argo-cd\")).version = \"${RESOLVED_CHART_VER}\"" "$CHART_YAML"
yq eval -i ".annotations.\"argocd-agent.argoproj-labs.io/argo-cd-module-version\" = \"${ARGOCD_MODULE_VER}\"" "$CHART_YAML"
yq eval -i ".annotations.\"argocd-agent.argoproj-labs.io/argo-cd-chart-version\" = \"${RESOLVED_CHART_VER}\"" "$CHART_YAML"

echo "Updated Chart.yaml: argo-cd chart ${RESOLVED_CHART_VER} (app ${ARGOCD_MODULE_VER})"
