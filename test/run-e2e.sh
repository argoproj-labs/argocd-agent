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

# Check if all required Redis TLS certificates exist
REQUIRED_CERTS=(
  "ca.crt"
  "ca.key"
  "redis-control-plane.crt"
  "redis-control-plane.key"
  "redis-proxy.crt"
  "redis-proxy.key"
  "redis-managed.crt"
  "redis-managed.key"
  "redis-autonomous.crt"
  "redis-autonomous.key"
)

MISSING_CERTS=()
for cert in "${REQUIRED_CERTS[@]}"; do
  if [ ! -f "${REDIS_TLS_DIR}/$cert" ]; then
    MISSING_CERTS+=("$cert")
  fi
done

if [ ${#MISSING_CERTS[@]} -gt 0 ]; then
    echo "ERROR: Required Redis TLS certificates not found in ${REDIS_TLS_DIR}:"
    printf '  - %s\n' "${MISSING_CERTS[@]}"
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

echo "✓ All required Redis TLS certificates found (${#REQUIRED_CERTS[@]} files)"

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

# Dual-mode test setup: Port-forwards (local) vs LoadBalancer IPs (CI)
# Auto-detect or use E2E_USE_PORT_FORWARD env var
if [[ "${E2E_USE_PORT_FORWARD:-auto}" == "auto" ]]; then
    if [[ "$OSTYPE" == "darwin"* ]]; then
        E2E_USE_PORT_FORWARD=true
    else
        E2E_USE_PORT_FORWARD=false
    fi
fi

if [[ "$E2E_USE_PORT_FORWARD" == "true" ]]; then
    echo "=========================================="
    echo "Test Mode: LOCAL (Port-Forwards)"
    echo "=========================================="
    
    # Check if port-forwards are running
    PORT_FORWARD_DETECTED=false
    DETECTION_METHOD=""
    
    # Try multiple port detection methods in order of preference
    if command -v lsof &>/dev/null; then
        DETECTION_METHOD="lsof"
        if lsof -i :6380 -i :6381 -i :6382 >/dev/null 2>&1; then
            PORT_FORWARD_DETECTED=true
        fi
    elif command -v ss &>/dev/null; then
        DETECTION_METHOD="ss"
        if ss -tln | grep -qE ':(6380|6381|6382)\s'; then
            PORT_FORWARD_DETECTED=true
        fi
    elif command -v netstat &>/dev/null; then
        DETECTION_METHOD="netstat"
        if netstat -tln 2>/dev/null | grep -qE ':(6380|6381|6382)\s'; then
            PORT_FORWARD_DETECTED=true
        fi
    fi
    
    if [[ -z "$DETECTION_METHOD" ]]; then
        # No port detection tool available, skip check
        echo "Port detection tools not available; skipping port-forward check"
        echo "   (Install lsof, ss, or netstat for better validation)"
        echo ""
    elif [[ "$PORT_FORWARD_DETECTED" == "false" ]]; then
        # Detection tool available but ports not found
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
        echo "Continuing anyway (tests may fail if port-forwards aren't running)..."
        echo ""
        sleep 3
    else
        # Port-forwards detected
        echo "✓ Port-forwards detected on localhost:6380, 6381, 6382 (via $DETECTION_METHOD)"
        echo ""
    fi
    
    # Set Redis addresses for local development (via port-forwards)
    export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS="localhost:6380"
    export MANAGED_AGENT_REDIS_ADDR="localhost:6381"
    export AUTONOMOUS_AGENT_REDIS_ADDR="localhost:6382"
    export ARGOCD_SERVER_ADDRESS="localhost:8444"
else
    echo "=========================================="
    echo "Test Mode: Linux/CI (Direct LoadBalancer IPs)"
    echo "=========================================="
    echo "Addresses will be inherited from running processes"
    echo ""
fi

echo "Test Configuration:"
echo "  ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS=${ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS:-<from env>}"
echo "  MANAGED_AGENT_REDIS_ADDR=${MANAGED_AGENT_REDIS_ADDR:-<from env>}"
echo "  AUTONOMOUS_AGENT_REDIS_ADDR=${AUTONOMOUS_AGENT_REDIS_ADDR:-<from env>}"
echo "  ARGOCD_SERVER_ADDRESS=${ARGOCD_SERVER_ADDRESS:-<from env>}"
echo ""

go test -count=1 -v -race -timeout 60m github.com/argoproj-labs/argocd-agent/test/e2e