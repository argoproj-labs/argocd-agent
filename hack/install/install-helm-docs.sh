#!/bin/bash
# Copyright 2024 The argocd-agent Authors
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

set -eux -o pipefail

PROJECT_ROOT=$(cd $(dirname ${BASH_SOURCE})/../..; pwd)
DIST_PATH="${PROJECT_ROOT}/build/bin"
mkdir -p "${DIST_PATH}"
PATH="${DIST_PATH}:${PATH}"

HELM_DOCS_VERSION="${HELM_DOCS_VERSION:-v1.13.1}"
OS=$(go env GOOS)
ARCHITECTURE=$(go env GOARCH)
DOWNLOADS=$(mktemp -d /tmp/downloads.XXXXXXXXX)

# Check if helm-docs already exists and is the correct version
if [ -x "${DIST_PATH}/helm-docs" ]; then
  INSTALLED_VERSION=$("${DIST_PATH}/helm-docs" --version 2>/dev/null | awk '{print $2}' || echo "")
  if [ "${INSTALLED_VERSION}" = "${HELM_DOCS_VERSION#v}" ]; then
    echo "helm-docs version ${HELM_DOCS_VERSION} already installed at ${DIST_PATH}/helm-docs"
    exit 0
  fi
  echo "helm-docs version mismatch (installed: ${INSTALLED_VERSION:-none}, want: ${HELM_DOCS_VERSION#v}), reinstalling..."
fi

if [ "$ARCHITECTURE" = "amd64" ]; then
    ARCHITECTURE="x86_64"
fi

export TARGET_FILE=helm-docs_${HELM_DOCS_VERSION#v}_${OS}_${ARCHITECTURE}.tar.gz
url=https://github.com/norwoodj/helm-docs/releases/download/${HELM_DOCS_VERSION}/helm-docs_${HELM_DOCS_VERSION#v}_${OS}_${ARCHITECTURE}.tar.gz

echo "Downloading helm-docs ${HELM_DOCS_VERSION} from ${url}..."
[ -e $DOWNLOADS/${TARGET_FILE} ] || curl -sLf --retry 3 -o $DOWNLOADS/${TARGET_FILE} ${url}

echo "Extracting helm-docs..."
mkdir -p /tmp/helm-docs-${HELM_DOCS_VERSION#v}
tar -xzf $DOWNLOADS/${TARGET_FILE} -C /tmp/helm-docs-${HELM_DOCS_VERSION#v}

# The binary is extracted directly, move it to the destination
install -m 0755 /tmp/helm-docs-${HELM_DOCS_VERSION#v}/helm-docs ${DIST_PATH}/helm-docs

# Verify installation
"${DIST_PATH}/helm-docs" --version

echo "helm-docs ${HELM_DOCS_VERSION} installed successfully to ${DIST_PATH}/helm-docs"
