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

set -ex -o pipefail
ARGS=$*
if ! kubectl config get-contexts | tail -n +2 | awk '{ print $2 }' | grep -qE '^vcluster-agent-managed$'; then
    echo "kube context vcluster-agent-autonomous is not configured; missing setup?" >&2
    exit 1
fi
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
go run github.com/argoproj-labs/argocd-agent/cmd/agent \
    --agent-mode managed \
    --creds userpass:${SCRIPTPATH}/creds/creds.agent-managed \
    --server-address 127.0.0.1 \
    --server-port 8443 \
    --insecure-tls \
    --kubecontext vcluster-agent-managed \
    --namespace agent-managed \
    --log-level trace $ARGS \
    #--keep-alive-ping-interval 15m
