#!/bin/bash
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

CHART_DIR="${1:?Usage: $0 <chart-dir> <registry>}"
REGISTRY="${2:?Usage: $0 <chart-dir> <registry>}"

if [[ ! -f "${CHART_DIR}/Chart.yaml" ]]; then
	echo "Error: ${CHART_DIR}/Chart.yaml not found" >&2
	exit 1
fi

CHART_NAME=$(grep '^name:' "${CHART_DIR}/Chart.yaml" | awk '{print $2}')
CHART_VERSION=$(grep '^version:' "${CHART_DIR}/Chart.yaml" | awk '{print $2}')

if [[ -z "${CHART_NAME}" || -z "${CHART_VERSION}" ]]; then
	echo "Error: could not extract name or version from ${CHART_DIR}/Chart.yaml" >&2
	exit 1
fi

echo "==> Chart: ${CHART_NAME} v${CHART_VERSION}"
echo "==> Registry: ${REGISTRY}"

echo "==> Linting chart..."
helm lint "${CHART_DIR}"

echo "==> Packaging chart..."
PACKAGE_DIR=$(mktemp -d)
helm package "${CHART_DIR}" --destination "${PACKAGE_DIR}"
PACKAGE_FILE="${PACKAGE_DIR}/${CHART_NAME}-${CHART_VERSION}.tgz"

if [[ ! -f "${PACKAGE_FILE}" ]]; then
	echo "Error: expected package ${PACKAGE_FILE} not found" >&2
	exit 1
fi

echo "==> Checking if ${CHART_NAME}:${CHART_VERSION} already exists in registry..."
SHOW_STDERR=$(mktemp)
if helm show chart "oci://${REGISTRY}/${CHART_NAME}" --version "${CHART_VERSION}" >/dev/null 2>"${SHOW_STDERR}"; then
	echo "Chart ${CHART_NAME}:${CHART_VERSION} already exists in registry, skipping push"
	echo "pushed=false" >> "${GITHUB_OUTPUT:-/dev/null}"
	rm -rf "${PACKAGE_DIR}" "${SHOW_STDERR}"
	exit 0
fi
SHOW_ERR=$(cat "${SHOW_STDERR}")
rm -f "${SHOW_STDERR}"
if echo "${SHOW_ERR}" | grep -qiE 'not found|404|manifest unknown|no such host.*no such chart'; then
	echo "==> Chart not found in registry, proceeding with push"
else
	echo "Error: unexpected failure checking registry for ${CHART_NAME}:${CHART_VERSION}" >&2
	echo "${SHOW_ERR}" >&2
	exit 1
fi

echo "==> Pushing chart to oci://${REGISTRY}..."
OUTPUT=$(helm push "${PACKAGE_FILE}" "oci://${REGISTRY}" 2>&1)
echo "${OUTPUT}"

DIGEST=$(echo "${OUTPUT}" | grep "Digest:" | awk '{print $2}')
if [[ -z "${DIGEST}" ]]; then
	echo "Error: could not extract digest from helm push output" >&2
	exit 1
fi

echo "==> Pushed ${CHART_NAME}:${CHART_VERSION} with digest ${DIGEST}"

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
	{
		echo "chart_name=${CHART_NAME}"
		echo "chart_version=${CHART_VERSION}"
		echo "chart_ref=${REGISTRY}/${CHART_NAME}"
		echo "digest=${DIGEST}"
		echo "package=${PACKAGE_FILE}"
		echo "pushed=true"
	} >> "${GITHUB_OUTPUT}"
fi
