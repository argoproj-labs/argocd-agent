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

# Setup Redis TLS for E2E testing environment
# This script:
# 1. Waits for LoadBalancer IPs to be assigned
# 2. Generates Redis TLS certificates
# 3. Configures Redis and ArgoCD components for TLS

set -e

SCRIPTPATH="$(cd "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P)"

echo ""
echo "============================================================"
echo "  Setting up Redis TLS for E2E Environment"
echo "============================================================"
echo ""

# Wait for LoadBalancer IPs to be assigned
echo "Waiting for LoadBalancer IPs to be assigned..."
for context in vcluster-control-plane vcluster-agent-managed vcluster-agent-autonomous; do
    echo "  Checking LoadBalancer in ${context}..."
    FOUND=""
    for i in 1 2 3 4 5 6 7 8 9 10; do
        LB_IP=$(kubectl get svc argocd-redis --context="${context}" -n argocd -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
        LB_HOST=$(kubectl get svc argocd-redis --context="${context}" -n argocd -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || echo "")
        
        if [ -n "${LB_IP}" ] || [ -n "${LB_HOST}" ]; then
            FOUND="yes"
            if [ -n "${LB_IP}" ]; then
                echo "    LoadBalancer IP assigned: ${LB_IP}"
            else
                echo "    LoadBalancer hostname assigned: ${LB_HOST}"
            fi
            break
        fi
        
        echo "    Waiting for LoadBalancer... (attempt ${i}/10)"
        sleep 5
    done
    
    if [ -z "${FOUND}" ]; then
        echo "ERROR: LoadBalancer not assigned for ${context} after 10 attempts (50 seconds)"
        echo ""
        echo "This usually means:"
        echo "  1. MetalLB is not installed or not configured on your cluster"
        echo "  2. Your cluster doesn't support LoadBalancer services"
        echo "  3. The cluster is slow to assign LoadBalancer IPs"
        echo ""
        echo "For local development, see: hack/dev-env/README.md"
        exit 1
    fi
done

echo ""
echo "Configuring Redis TLS (required for E2E)..."
"${SCRIPTPATH}/gen-redis-tls-certs.sh"

echo ""
echo "Configuring each cluster for Redis TLS (Redis + ArgoCD components together)"
echo "Note: Redis and ArgoCD components are configured together per-cluster to avoid"
echo "      connection errors during the transition period."
echo ""

echo "=== Control Plane ==="
"${SCRIPTPATH}/configure-redis-tls.sh" vcluster-control-plane
"${SCRIPTPATH}/configure-argocd-redis-for-tls.sh" vcluster-control-plane

echo ""
echo "=== Agent Managed ==="
"${SCRIPTPATH}/configure-redis-tls.sh" vcluster-agent-managed
"${SCRIPTPATH}/configure-argocd-redis-for-tls.sh" vcluster-agent-managed

echo ""
echo "=== Agent Autonomous ==="
"${SCRIPTPATH}/configure-redis-tls.sh" vcluster-agent-autonomous
"${SCRIPTPATH}/configure-argocd-redis-for-tls.sh" vcluster-agent-autonomous

echo ""
echo "============================================================"
echo "  E2E environment ready with Redis TLS enabled (required)"
echo "============================================================"
echo ""

