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
       # For TLS to work with proper certificate validation, we use port-forward
       # instead of LoadBalancer, since 'localhost' is in the certificate SANs
       # but the dynamic LoadBalancer hostname is not.
       echo "Starting port-forward to Redis on localhost:6380..."
       kubectl port-forward svc/argocd-redis -n argocd 6380:6379 --context vcluster-control-plane >/dev/null 2>&1 &
       PORT_FORWARD_PID=$!
       sleep 2  # Give port-forward time to establish
       
       if ! kill -0 $PORT_FORWARD_PID 2>/dev/null; then
           echo "Failed to establish port-forward to Redis" >&2
               exit 1
       fi
       
       ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS="localhost:6380"
       export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS
       echo "Connected to Redis via port-forward at ${ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS}"
       
       # Cleanup function to kill port-forward on exit
       trap "kill $PORT_FORWARD_PID 2>/dev/null || true" EXIT
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

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"

# Check if Redis TLS certificates exist
REDIS_TLS_ARGS=""
if [ -f "${SCRIPTPATH}/creds/redis-tls/redis-proxy.crt" ] && \
   [ -f "${SCRIPTPATH}/creds/redis-tls/redis-proxy.key" ] && \
   [ -f "${SCRIPTPATH}/creds/redis-tls/ca.crt" ]; then
    echo "Redis TLS certificates found, enabling TLS for Redis connections"
    # Use proper certificate validation with CA path
    # This works because we connect via localhost (port-forward), which is in the cert SANs
    REDIS_TLS_ARGS="--redis-tls-enabled=true \
        --redis-server-tls-cert=${SCRIPTPATH}/creds/redis-tls/redis-proxy.crt \
        --redis-server-tls-key=${SCRIPTPATH}/creds/redis-tls/redis-proxy.key \
        --redis-upstream-ca-path=${SCRIPTPATH}/creds/redis-tls/ca.crt"
    echo "✅ Redis TLS enabled with proper certificate validation"
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
