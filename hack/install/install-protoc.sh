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

protoc_version="25.3"
OS=$(go env GOOS)
ARCHITECTURE=$(go env GOARCH)
DOWNLOADS=$(mktemp -d /tmp/downloads.XXXXXXXXX)
mkdir -p ${DIST_PATH}
case $OS in
  darwin)
    # For macOS, the x86_64 binary is used even on Apple Silicon (it is run through rosetta), so
    # we download and install the x86_64 version. See: https://github.com/protocolbuffers/protobuf/pull/8557
    protoc_os=osx
    protoc_arch=x86_64
    ;;
  *)
    protoc_os=linux
    case $ARCHITECTURE in
      arm64|arm)
        protoc_arch=aarch_64
        ;;
      s390x)
        protoc_arch=s390_64
        ;;
      ppc64le)
        protoc_arch=ppcle_64
        ;;
      *)
        protoc_arch=x86_64
        ;;
    esac
    ;;
esac

export TARGET_FILE=protoc_${protoc_version}_${OS}_${ARCHITECTURE}.zip
url=https://github.com/protocolbuffers/protobuf/releases/download/v${protoc_version}/protoc-${protoc_version}-${protoc_os}-${protoc_arch}.zip
[ -e $DOWNLOADS/${TARGET_FILE} ] || curl -sLf --retry 3 -o $DOWNLOADS/${TARGET_FILE} ${url}
#$(dirname $0)/compare-chksum.sh
mkdir -p /tmp/protoc-${protoc_version}
unzip -o $DOWNLOADS/${TARGET_FILE} -d /tmp/protoc-${protoc_version}
mkdir -p ${DIST_PATH}/protoc-include
install -m 0755 /tmp/protoc-${protoc_version}/bin/protoc ${DIST_PATH}/protoc
cp -a /tmp/protoc-${protoc_version}/include/* ${DIST_PATH}/protoc-include
#chmod -R  ${DIST_PATH}/protoc-include
protoc --version
