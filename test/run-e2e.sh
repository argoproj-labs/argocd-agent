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

echo "=========================================="
echo "Checking Redis TLS Configuration (REQUIRED)"
echo "=========================================="

# Check if required Redis TLS secrets exist (we now use K8s secrets, not local files)
REQUIRED_SECRETS=(
  "vcluster-control-plane:argocd-redis-tls"
  "vcluster-control-plane:argocd-redis-proxy-tls"
  "vcluster-agent-managed:argocd-redis-tls"
  "vcluster-agent-autonomous:argocd-redis-tls"
)

MISSING_SECRETS=()
for secret_ref in "${REQUIRED_SECRETS[@]}"; do
  CONTEXT="${secret_ref%%:*}"
  SECRET="${secret_ref##*:}"
  if ! kubectl --context="${CONTEXT}" -n argocd get secret "${SECRET}" >/dev/null 2>&1; then
    MISSING_SECRETS+=("${CONTEXT}/${SECRET}")
  fi
done

if [ ${#MISSING_SECRETS[@]} -gt 0 ]; then
    echo "ERROR: Required Redis TLS secrets not found:"
    printf '  - %s\n' "${MISSING_SECRETS[@]}"
    echo ""
    echo "Redis TLS is REQUIRED for E2E tests (security requirement)."
    echo ""
    echo "Please run:"
    echo "  make setup-e2e"
    echo ""
    echo "This will generate and configure Redis TLS certificates using argocd-agentctl CLI"
    echo ""
    exit 1
fi

echo "✓ All required Redis TLS secrets found (${#REQUIRED_SECRETS[@]} secrets)"

# Verify Redis TLS is configured in all vclusters
for CONTEXT in vcluster-control-plane vcluster-agent-autonomous vcluster-agent-managed; do
    if kubectl config get-contexts | grep -q "${CONTEXT}"; then
        echo "Checking Redis TLS in ${CONTEXT}..."
        
        # Check if Redis TLS secret exists
        if ! kubectl --context="${CONTEXT}" -n argocd get secret argocd-redis-tls >/dev/null 2>&1; then
            echo "ERROR: Redis TLS secret not found in ${CONTEXT}!"
            echo "Please run: make setup-e2e"
            exit 1
        fi
        
        # Check if Redis is configured with TLS (it's a Deployment, not StatefulSet)
        # Verify both --tls-port argument and redis-tls volume mount
        REDIS_JSON=$(kubectl --context="${CONTEXT}" -n argocd get deployment argocd-redis -o json 2>/dev/null)
        # Check if args contain --tls-port
        REDIS_HAS_TLS_PORT=$(echo "$REDIS_JSON" | jq -e '.spec.template.spec.containers[] | select(.name == "redis") | (.args // []) | join(" ") | contains("--tls-port")' >/dev/null 2>&1 && echo "true" || echo "false")
        REDIS_HAS_TLS_VOLUME=$(echo "$REDIS_JSON" | jq -e '.spec.template.spec.volumes[] | select(.name == "redis-tls")' >/dev/null 2>&1 && echo "true" || echo "false")
        
        if [[ "$REDIS_HAS_TLS_PORT" != "true" ]] || [[ "$REDIS_HAS_TLS_VOLUME" != "true" ]]; then
            echo "ERROR: Redis Deployment in ${CONTEXT} is not configured with TLS!"
            echo "  TLS configuration (--tls-port) present: ${REDIS_HAS_TLS_PORT}"
            echo "  TLS volume (redis-tls) present: ${REDIS_HAS_TLS_VOLUME}"
            echo "Please run: make setup-e2e"
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

go test -count=1 -v -race -timeout 30m github.com/argoproj-labs/argocd-agent/test/e2e
