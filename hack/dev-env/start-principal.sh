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
if ! kubectl config get-contexts | tail -n +2 | awk '{ print $2 }' | grep -qE '^vcluster-control-plane$'; then
    echo "kube context vcluster-control-plane is not configured; missing setup?" >&2
    exit 1
fi

if test "${ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS}" = ""; then
       # Fallback for manual runs (when ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS is not set)
       # Normally, start-e2e.sh sets this env var based on E2E_USE_PORT_FORWARD:
       # - E2E_USE_PORT_FORWARD=true: localhost:6380 (local development with port-forward)
       # - E2E_USE_PORT_FORWARD not set: <LoadBalancer-IP>:6379 (CI/Linux - direct LoadBalancer)
       # This fallback defaults to localhost for convenience in manual testing.
       ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS="localhost:6380"
       export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS
       echo "ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS not set; defaulting to localhost:6380"
       echo "Ensure a port-forward is running: kubectl port-forward --context=vcluster-control-plane -n argocd svc/argocd-redis 6380:6379"
fi

if test "${REDIS_PASSWORD}" = ""; then
    export REDIS_PASSWORD=$(kubectl get secret argocd-redis --context=vcluster-control-plane -n argocd -o jsonpath='{.data.auth}' | base64 --decode)
fi

# Point the principal to the e2e test configuration if it exists
E2E_ENV_FILE="/tmp/argocd-agent-e2e"
if [ -f "$E2E_ENV_FILE" ]; then
    source "$E2E_ENV_FILE"
    export ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET=${ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET:-false}
fi

# Set a longer informer sync timeout for E2E tests (default is 60s, use 120s for CI)
export ARGOCD_PRINCIPAL_INFORMER_SYNC_TIMEOUT=${ARGOCD_PRINCIPAL_INFORMER_SYNC_TIMEOUT:-120s}

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"

# Check if Redis TLS certificates exist
# Note: Redis TLS uses the same agent CA (ca.crt is a copy from creds/ca.crt)
REDIS_TLS_ARGS=""
if [ -f "${SCRIPTPATH}/creds/redis-tls/redis-proxy.crt" ] && \
   [ -f "${SCRIPTPATH}/creds/redis-tls/redis-proxy.key" ] && \
   [ -f "${SCRIPTPATH}/creds/redis-tls/ca.crt" ]; then
    echo "Redis TLS certificates found (using agent CA), enabling TLS for Redis connections"
    # Certificate includes SANs for:
    # - localhost (for port-forward connections)
    # - rathole-container-internal (for reverse tunnel from remote Argo CD)
    # - local IP (for direct connections when on same network)
    REDIS_TLS_ARGS="--redis-tls-enabled=true \
        --redis-proxy-server-tls-cert=${SCRIPTPATH}/creds/redis-tls/redis-proxy.crt \
        --redis-proxy-server-tls-key=${SCRIPTPATH}/creds/redis-tls/redis-proxy.key \
        --redis-ca-path=${SCRIPTPATH}/creds/redis-tls/ca.crt"
    echo "✓ Redis TLS enabled (certificates signed by agent CA)"
else
    echo "Redis TLS certificates not found, running without TLS"
    echo "Run './hack/dev-env/gen-redis-tls-certs.sh' to generate certificates"
fi

go run github.com/argoproj-labs/argocd-agent/cmd/argocd-agent principal \
	--allowed-namespaces '*' \
	--kubecontext vcluster-control-plane \
	--log-level ${ARGOCD_AGENT_LOG_LEVEL:-trace} \
	--namespace argocd \
	--auth "mtls:CN=([^,]+)" \
	$REDIS_TLS_ARGS \
	$ARGS