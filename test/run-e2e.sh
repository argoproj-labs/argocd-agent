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

set -ex -o pipefail

# Check if vcluster context exists
if ! kubectl config get-contexts | tail -n +2 | awk '{ print $2 }' | grep -qE '^vcluster-control-plane$'; then
    echo "kube context vcluster-control-plane is not configured; missing setup?" >&2
    exit 1
fi

# Wait until an agent reports itself as connected to the principal. The
# agent's /healthz only returns 200 once its event stream with the principal is
# up, i.e. after authentication. The processes started by 'make start-e2e' may
# still be compiling (with -race, from a cold cache this takes minutes), and the
# principal only sends events to agents that have already authenticated, so
# running the tests before that makes the first CleanUp miss the agents.
waitForAgent() {
    AGENT_NAME=$1
    HEALTHZ_PORT=$2
    MAX_ATTEMPTS=${E2E_WAIT_FOR_AGENTS_ATTEMPTS:-120}
    for ((i=1; i<=MAX_ATTEMPTS; i++)); do
        if curl -sf --max-time 5 "http://localhost:${HEALTHZ_PORT}/healthz" >/dev/null; then
            echo "Agent ${AGENT_NAME} is connected to the principal"
            return 0
        fi
        echo "Agent ${AGENT_NAME} is not connected yet, attempting again in 5 seconds..."
        sleep 5
    done
    echo "Agent ${AGENT_NAME} did not connect after ${MAX_ATTEMPTS} attempts." >&2
    return 1
}

# The healthz ports are only reachable when the agents run locally (goreman);
# see hack/dev-env/start-agent-*.sh for the port assignments.
if test "${ARGOCD_AGENT_IN_CLUSTER}" = ""; then
    waitForAgent agent-managed 8001
    waitForAgent agent-autonomous 8002
fi

E2E_DESTINATION_BASED_MAPPING="${E2E_DESTINATION_BASED_MAPPING:-}" \
go test -count=1 -v -race -timeout 60m github.com/argoproj-labs/argocd-agent/test/e2e

# If we detect a data race in the 'make start-e2e' output, then fail here to cause the overall test job to fail.
E2E_LOG="/tmp/argocd-agent-e2e-process-output.log"
if [ -f "$E2E_LOG" ] && grep -q "WARNING: DATA RACE" "$E2E_LOG"; then
    echo "ERROR: Data race detected in e2e process output. See logs for details." >&2
    exit 1
fi
