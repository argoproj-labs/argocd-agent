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
    echo "kube context vcluster-agent-managed is not configured; missing setup?" >&2
    exit 1
fi
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"

export ARGOCD_AGENT_REMOTE_PORT=${ARGOCD_AGENT_REMOTE_PORT:-8443}

if test "${REDIS_PASSWORD}" = ""; then
    export REDIS_PASSWORD=$(kubectl get secret argocd-redis --context=vcluster-agent-managed -n argocd -o jsonpath='{.data.auth}' | base64 --decode)
fi

# Point the agent to the toxiproxy server if it is configured from the e2e tests
E2E_ENV_FILE="/tmp/argocd-agent-e2e"
if [ -f "$E2E_ENV_FILE" ]; then
    source "$E2E_ENV_FILE"
    export ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET=${ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET:-false}
fi

# Check if Redis TLS certificates exist
REDIS_TLS_ARGS=""
if [ -f "${SCRIPTPATH}/creds/redis-tls/ca.crt" ]; then
    echo "Redis TLS certificates found, enabling TLS for Redis connections"
    REDIS_TLS_ARGS="--redis-tls-enabled=true \
        --redis-tls-ca-path=${SCRIPTPATH}/creds/redis-tls/ca.crt"
else
    echo "Redis TLS certificates not found, running without TLS"
    echo "Run './hack/dev-env/gen-redis-tls-certs.sh' to generate certificates"
fi

# Set Redis address for local development
# When running locally, we need to use localhost via port-forward
# since we can't access in-cluster DNS
if [ -z "${ARGOCD_AGENT_REDIS_ADDRESS}" ]; then
    # Default to localhost:6381 for local E2E testing (requires port-forward)
    # This allows TLS certificate validation to work (localhost is in cert SANs)
    ARGOCD_AGENT_REDIS_ADDRESS="localhost:6381"
    echo "Using default Redis address for local development: ${ARGOCD_AGENT_REDIS_ADDRESS}"
    echo "NOTE: Requires port-forward to Redis (automatic when using 'make start-e2e', otherwise run manually):"
    echo "  kubectl port-forward svc/argocd-redis -n argocd 6381:6379 --context vcluster-agent-managed"
else
    echo "Using Redis address: ${ARGOCD_AGENT_REDIS_ADDRESS}"
fi
REDIS_ADDRESS_ARG="--redis-addr=${ARGOCD_AGENT_REDIS_ADDRESS}"

go run github.com/argoproj-labs/argocd-agent/cmd/argocd-agent agent \
    --agent-mode managed \
    --creds "mtls:any" \
    $REDIS_TLS_ARGS \
    $REDIS_ADDRESS_ARG \
    --server-address 127.0.0.1 \
    --insecure-tls \
    --kubecontext vcluster-agent-managed \
    --namespace argocd \
    --log-level ${ARGOCD_AGENT_LOG_LEVEL:-trace} $ARGS \
    --healthz-port 8001 \
    #--enable-compression true
    #--keep-alive-ping-interval 15m
