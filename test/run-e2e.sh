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

# Enforce Redis TLS for E2E tests
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REDIS_TLS_DIR="${SCRIPT_DIR}/../hack/dev-env/creds/redis-tls"

echo "=========================================="
echo "Checking Redis TLS Configuration (REQUIRED)"
echo "=========================================="

# Check if Redis TLS certificates exist
if [ ! -f "${REDIS_TLS_DIR}/ca.crt" ]; then
    echo "ERROR: Redis TLS certificates not found!"
    echo ""
    echo "Redis TLS is REQUIRED for E2E tests (security requirement)."
    echo ""
    echo "Please run the following commands:"
    echo "  ./hack/dev-env/gen-redis-tls-certs.sh"
    echo "  ./hack/dev-env/configure-redis-tls.sh vcluster-control-plane"
    echo "  ./hack/dev-env/configure-redis-tls.sh vcluster-agent-managed"
    echo "  ./hack/dev-env/configure-redis-tls.sh vcluster-agent-autonomous"
    echo ""
    exit 1
fi

echo "✓ Redis TLS certificates found"

# Verify Redis TLS is configured in all vclusters
for CONTEXT in vcluster-control-plane vcluster-agent-autonomous vcluster-agent-managed; do
    if kubectl config get-contexts | grep -q "${CONTEXT}"; then
        echo "Checking Redis TLS in ${CONTEXT}..."
        
        # Check if Redis TLS secret exists
        if ! kubectl --context="${CONTEXT}" -n argocd get secret argocd-redis-tls >/dev/null 2>&1; then
            echo "ERROR: Redis TLS secret not found in ${CONTEXT}!"
            echo "Please run: ./hack/dev-env/configure-redis-tls.sh ${CONTEXT}"
            exit 1
        fi
        
        # Check if Redis is configured with TLS (it's a Deployment, not StatefulSet)
        # Verify both --tls-port argument and redis-tls volume mount
        REDIS_JSON=$(kubectl --context="${CONTEXT}" -n argocd get deployment argocd-redis -o json 2>/dev/null)
        REDIS_HAS_TLS_PORT=$(echo "$REDIS_JSON" | jq -e '.spec.template.spec.containers[0].args | any(. == "--tls-port")' >/dev/null 2>&1 && echo "true" || echo "false")
        REDIS_HAS_TLS_VOLUME=$(echo "$REDIS_JSON" | jq -e '.spec.template.spec.volumes[] | select(.name == "redis-tls")' >/dev/null 2>&1 && echo "true" || echo "false")
        
        if [[ "$REDIS_HAS_TLS_PORT" != "true" ]] || [[ "$REDIS_HAS_TLS_VOLUME" != "true" ]]; then
            echo "ERROR: Redis Deployment in ${CONTEXT} is not configured with TLS!"
            echo "  TLS port arg (--tls-port) present: ${REDIS_HAS_TLS_PORT}"
            echo "  TLS volume (redis-tls) present: ${REDIS_HAS_TLS_VOLUME}"
            echo "Please run: ./hack/dev-env/configure-redis-tls.sh ${CONTEXT}"
            exit 1
        fi
        
        echo "✓ Redis TLS configured in ${CONTEXT}"
    fi
done

echo ""
echo "=========================================="
echo " Redis TLS Configuration Verified"
echo "=========================================="
echo ""

echo "Running E2E tests with Redis TLS enabled..."
echo ""

# Check if port-forwards are running (required for local macOS development)
if [[ "$OSTYPE" == "darwin"* ]]; then
    if ! lsof -i :6380 -i :6381 -i :6382 >/dev/null 2>&1; then
        echo ""
        echo " WARNING: Port-forwards not detected!"
        echo ""
        echo "For local macOS development, you must have 'make start-e2e' running"
        echo "in another terminal before running tests."
        echo ""
        echo "In Terminal 1:"
        echo "  make start-e2e"
        echo ""
        echo "In Terminal 2:"
        echo "  make test-e2e"
        echo ""
        echo "Continuing anyway (will work in CI with MetalLB)..."
        echo ""
        sleep 3
    else
        echo "✓ Port-forwards detected (localhost:6380, 6381, 6382)"
        echo ""
    fi
    
    # Set Redis addresses for local macOS development (via port-forwards)
    echo "Setting addresses for local development..."
    export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS="localhost:6380"
    export MANAGED_AGENT_REDIS_ADDR="localhost:6381"
    export AUTONOMOUS_AGENT_REDIS_ADDR="localhost:6382"
    export ARGOCD_SERVER_ADDRESS="localhost:8444"
    echo "  ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS=${ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS}"
    echo "  MANAGED_AGENT_REDIS_ADDR=${MANAGED_AGENT_REDIS_ADDR}"
    echo "  AUTONOMOUS_AGENT_REDIS_ADDR=${AUTONOMOUS_AGENT_REDIS_ADDR}"
    echo "  ARGOCD_SERVER_ADDRESS=${ARGOCD_SERVER_ADDRESS}"
    echo ""
fi

go test -count=1 -v -race -timeout 60m github.com/argoproj-labs/argocd-agent/test/e2e