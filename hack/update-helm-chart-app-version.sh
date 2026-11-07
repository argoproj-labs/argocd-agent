#!/bin/bash
# Copyright 2026 The argocd-agent Authors
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

# Sets the appVersion of the Helm charts to the given release and bumps the
# patch level of their chart version, which CI requires for every chart change.
# Charts that already have this appVersion are left untouched.
# This script is intended to run in the root of the project.
# Usage:
#   ./update-helm-chart-app-version.sh <version>
#     version - the released version, formatted as X.Y.Z without the v prefix

set -euo pipefail

TARGET_VERSION="${1:-}"

if ! echo "${TARGET_VERSION}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "Usage: $0 <version>" >&2
	echo "Version must be in format X.Y.Z without any prefix or suffix" >&2
	exit 1
fi

shopt -s nullglob
CHARTS=(install/helm-repo/argocd-agent-*/Chart.yaml)
if [[ ${#CHARTS[@]} -eq 0 ]]; then
	echo "Error: no Helm charts found, you should be in repository root." >&2
	exit 1
fi

for CHART in "${CHARTS[@]}"; do
	APP_VERSION=$(awk '/^appVersion:/ {print $2}' "${CHART}" | tr -d '"')
	if [[ "${APP_VERSION}" == "v${TARGET_VERSION}" ]]; then
		echo "${CHART} already has appVersion v${TARGET_VERSION}"
		continue
	fi

	CHART_VERSION=$(awk '/^version:/ {print $2}' "${CHART}" | tr -d '"')
	if ! echo "${CHART_VERSION}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
		echo "Error: cannot bump chart version '${CHART_VERSION}' in ${CHART}, expected X.Y.Z" >&2
		exit 1
	fi
	NEW_CHART_VERSION="${CHART_VERSION%.*}.$((${CHART_VERSION##*.} + 1))"

	echo "${CHART}: appVersion ${APP_VERSION} -> v${TARGET_VERSION}, version ${CHART_VERSION} -> ${NEW_CHART_VERSION}"
	sed -E \
		-e "s/^appVersion:.*/appVersion: \"v${TARGET_VERSION}\"/" \
		-e "s/^version:.*/version: ${NEW_CHART_VERSION}/" \
		"${CHART}" > "${CHART}.tmp"
	mv "${CHART}.tmp" "${CHART}"
done
