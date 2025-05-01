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
if ! kubectl config get-contexts | tail -n +2 | awk '{ print $2 }' | grep -qE '^vcluster-agent-autonomous$'; then
    echo "kube context vcluster-agent-autonomous is not configured; missing setup?" >&2
    exit 1
fi
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"

echo $ARGOCD_AGENT_REMOTE_PORT
export ARGOCD_AGENT_REMOTE_PORT=${ARGOCD_AGENT_REMOTE_PORT:-8443}

# Point the agent to the toxiproxy server if it is configured from the e2e tests
E2E_ENV_FILE="/tmp/argocd-agent-e2e"
if [ -f "$E2E_ENV_FILE" ]; then
    source "$E2E_ENV_FILE"
fi

go run github.com/argoproj-labs/argocd-agent/cmd/agent \
    --agent-mode autonomous \
    --creds mtls:any \
    --server-address 127.0.0.1 \
    --insecure-tls \
    --kubecontext vcluster-agent-autonomous \
    --namespace argocd \
    --log-level trace $ARGS \
    --metrics-port 8182 \
    --healthz-port 8002 \
    #--enable-compression true
    #--keep-alive-ping-interval 15m
