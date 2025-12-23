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

if test "${REDIS_PASSWORD}" = ""; then
    export REDIS_PASSWORD=$(kubectl get secret argocd-redis --context=vcluster-agent-autonomous -n argocd -o jsonpath='{.data.auth}' | base64 --decode)
fi

# Point the agent to the toxiproxy server if it is configured from the e2e tests
E2E_ENV_FILE="/tmp/argocd-agent-e2e"
if [ -f "$E2E_ENV_FILE" ]; then
    source "$E2E_ENV_FILE"
fi

# Check if Redis TLS certificates exist
# Note: Redis TLS uses the same agent CA (ca.crt is a copy from creds/ca.crt)
REDIS_TLS_ARGS=""
if [ -f "${SCRIPTPATH}/creds/redis-tls/ca.crt" ]; then
    echo "Redis TLS certificates found (using agent CA), enabling TLS for Redis connections"
    REDIS_TLS_ARGS="--redis-tls-enabled=true \
        --redis-tls-ca-path=${SCRIPTPATH}/creds/redis-tls/ca.crt"
else
    echo "Redis TLS certificates not found, running without TLS"
    echo "Run './hack/dev-env/gen-redis-tls-certs.sh' to generate certificates"
fi

# Set Redis address for local development
if [ -z "${ARGOCD_AGENT_REDIS_ADDRESS}" ]; then
    # Fallback for manual runs (when ARGOCD_AGENT_REDIS_ADDRESS is not set)
    # Normally, start-e2e.sh sets this env var based on E2E_USE_PORT_FORWARD:
    # - E2E_USE_PORT_FORWARD=true: localhost:6382 (local development with port-forward)
    # - E2E_USE_PORT_FORWARD not set: <LoadBalancer-IP>:6379 (CI/Linux - direct LoadBalancer)
    # This fallback defaults to localhost for convenience in manual testing.
    # Note: Agents running on host (via 'go run') cannot access in-cluster DNS.
    ARGOCD_AGENT_REDIS_ADDRESS="localhost:6382"
    echo "ARGOCD_AGENT_REDIS_ADDRESS not set; defaulting to localhost:6382"
    echo "Ensure port-forward is running:"
    echo "  kubectl port-forward svc/argocd-redis -n argocd 6382:6379 --context vcluster-agent-autonomous"
else
    echo "Using Redis address: ${ARGOCD_AGENT_REDIS_ADDRESS}"
fi
REDIS_ADDRESS_ARG="--redis-addr=${ARGOCD_AGENT_REDIS_ADDRESS}"

go run github.com/argoproj-labs/argocd-agent/cmd/argocd-agent agent \
    --agent-mode autonomous \
    --creds mtls:any \
    $REDIS_TLS_ARGS \
    $REDIS_ADDRESS_ARG \
    --server-address 127.0.0.1 \
    --kubecontext vcluster-agent-autonomous \
    --namespace argocd \
    --log-level ${ARGOCD_AGENT_LOG_LEVEL:-trace} $ARGS \
    --metrics-port 8182 \
    --healthz-port 8002 \
    #--enable-compression true
    #--keep-alive-ping-interval 15m