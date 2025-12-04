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
        if ! kubectl --context="${CONTEXT}" -n argocd get deployment argocd-redis -o json 2>/dev/null | grep -q "tls-port"; then
            echo "ERROR: Redis Deployment in ${CONTEXT} is not configured with TLS!"
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

go test -count=1 -v -race -timeout 30m github.com/argoproj-labs/argocd-agent/test/e2e
