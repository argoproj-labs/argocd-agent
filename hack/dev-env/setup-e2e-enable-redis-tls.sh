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
#
# 1. Redis Servers (3x): Each vcluster has its own Redis instance with TLS enabled
#    - vcluster-control-plane:  argocd-redis (with secret: argocd-redis-tls)
#    - vcluster-agent-managed:  argocd-redis (with secret: argocd-redis-tls)
#    - vcluster-agent-autonomous: argocd-redis (with secret: argocd-redis-tls)
#
# 2. Redis Proxy (1x): Principal runs a Redis proxy that routes commands
#    - Runs in vcluster-control-plane
#    - Uses separate certificate (secret: argocd-redis-proxy-tls)
#    - Needs special SANs for localhost, reverse tunnel, local IP
#
# 3. Certificate Usage:
#    - Principal: Loads proxy cert + CA from K8s secrets (default: argocd-redis-proxy-tls)
#    - Agents: Load CA from K8s secrets (default: argocd-redis-tls)
#    - E2E Tests: Load CA from K8s secrets via Kubernetes API
#    - Argo CD Components: Use CA from secret mounted as volume
#
# All certificates are signed by the same agent CA for consistency.

set -e

SCRIPTPATH="$(cd "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P)"

echo ""
echo "============================================================"
echo "  Setting up Redis TLS for E2E Environment"
echo "============================================================"
echo ""

# Wait for LoadBalancer IPs to be assigned and store them for certificate generation
echo "Waiting for LoadBalancer IPs to be assigned..."

# Arrays to store LoadBalancer addresses for each cluster
declare -a CONTROL_PLANE_ADDRS=()
declare -a MANAGED_ADDRS=()
declare -a AUTONOMOUS_ADDRS=()

for context in vcluster-control-plane vcluster-agent-managed vcluster-agent-autonomous; do
    echo "  Checking LoadBalancer in ${context}..."
    FOUND=""
    for i in 1 2 3 4 5 6 7 8 9 10; do
        LB_IP=$(kubectl get svc argocd-redis --context="${context}" -n argocd -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
        LB_HOST=$(kubectl get svc argocd-redis --context="${context}" -n argocd -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || echo "")
        
        if [ -n "${LB_IP}" ] || [ -n "${LB_HOST}" ]; then
            FOUND="yes"
            
            # Also get ClusterIP
            CLUSTER_IP=$(kubectl get svc argocd-redis --context="${context}" -n argocd -o jsonpath='{.spec.clusterIP}' 2>/dev/null || echo "")
            
            # Store the LoadBalancer address and ClusterIP for certificate generation
            case "${context}" in
                vcluster-control-plane)
                    [ -n "${LB_IP}" ] && CONTROL_PLANE_ADDRS+=("${LB_IP}")
                    [ -n "${LB_HOST}" ] && CONTROL_PLANE_ADDRS+=("${LB_HOST}")
                    [ -n "${CLUSTER_IP}" ] && CONTROL_PLANE_ADDRS+=("${CLUSTER_IP}")
                    ;;
                vcluster-agent-managed)
                    [ -n "${LB_IP}" ] && MANAGED_ADDRS+=("${LB_IP}")
                    [ -n "${LB_HOST}" ] && MANAGED_ADDRS+=("${LB_HOST}")
                    [ -n "${CLUSTER_IP}" ] && MANAGED_ADDRS+=("${CLUSTER_IP}")
                    ;;
                vcluster-agent-autonomous)
                    [ -n "${LB_IP}" ] && AUTONOMOUS_ADDRS+=("${LB_IP}")
                    [ -n "${LB_HOST}" ] && AUTONOMOUS_ADDRS+=("${LB_HOST}")
                    [ -n "${CLUSTER_IP}" ] && AUTONOMOUS_ADDRS+=("${CLUSTER_IP}")
                    ;;
            esac
            
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
        echo "ERROR: LoadBalancer not assigned for ${context} after 50 seconds"
        echo "Ensure MetalLB is installed or cluster supports LoadBalancer services"
        echo "See: hack/dev-env/README.md"
        exit 1
    fi
done

echo ""
echo "Configuring Redis TLS using argocd-agentctl (required for E2E)..."
echo ""

# Check if argocd-agentctl is available
AGENTCTL=""
if [ -f "${SCRIPTPATH}/../../dist/argocd-agentctl" ]; then
    AGENTCTL="${SCRIPTPATH}/../../dist/argocd-agentctl"
elif command -v argocd-agentctl &> /dev/null; then
    AGENTCTL="argocd-agentctl"
else
    echo "ERROR: argocd-agentctl not found. Run: make cli"
    exit 1
fi
echo "Using argocd-agentctl: ${AGENTCTL}"

echo "Cleaning up existing Redis TLS secrets..."
kubectl delete secret argocd-redis-tls -n argocd --context=vcluster-control-plane --ignore-not-found
kubectl delete secret argocd-redis-tls -n argocd --context=vcluster-agent-managed --ignore-not-found
kubectl delete secret argocd-redis-tls -n argocd --context=vcluster-agent-autonomous --ignore-not-found
echo ""

echo "Copying agent CA to agent clusters (for Redis TLS certificate generation)..."
# The CA is created by create-agent-config.sh in control-plane
# We need to copy it to agent clusters so redis generate-certs can read it
for context in vcluster-agent-managed vcluster-agent-autonomous; do
    echo "  Copying CA to ${context}..."
    # Delete existing secret first to avoid conflicts
    kubectl delete secret argocd-agent-ca -n argocd --context="${context}" --ignore-not-found >/dev/null 2>&1
    # Get fresh copy and create it (using replace --force handles any edge cases)
    kubectl get secret argocd-agent-ca -n argocd --context=vcluster-control-plane -o json | \
        jq 'del(.metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp, .metadata.managedFields)' | \
        kubectl create --context="${context}" -f - >/dev/null
done
echo ""

echo "=== Control Plane ==="
echo "Generating Redis TLS certificates..."
# Build cert generation command with LoadBalancer addresses in SAN
# Include default DNS names so Argo CD components can connect using service name
CERT_CMD="${AGENTCTL} redis generate-certs --principal-context vcluster-control-plane --principal-namespace argocd"
CERT_CMD="${CERT_CMD} --dns localhost --dns argocd-redis --dns argocd-redis.argocd.svc.cluster.local"
CERT_CMD="${CERT_CMD} --ip 127.0.0.1"

for addr in "${CONTROL_PLANE_ADDRS[@]}"; do
    if [[ "${addr}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        CERT_CMD="${CERT_CMD} --ip ${addr}"
    else
        CERT_CMD="${CERT_CMD} --dns ${addr}"
    fi
done

eval "${CERT_CMD}"

echo "Configuring Redis for TLS..."
${AGENTCTL} redis configure-tls \
  --principal-context vcluster-control-plane \
  --principal-namespace argocd

echo "Configuring Argo CD components for Redis TLS..."
${AGENTCTL} redis configure-argocd \
  --principal-context vcluster-control-plane \
  --principal-namespace argocd

echo ""
echo "=== Agent Managed ==="
echo "Generating Redis TLS certificates..."
# Build cert generation command with LoadBalancer addresses in SAN
# Include default DNS names so Argo CD components can connect using service name
CERT_CMD="${AGENTCTL} redis generate-certs --principal-context vcluster-agent-managed --principal-namespace argocd"
CERT_CMD="${CERT_CMD} --dns localhost --dns argocd-redis --dns argocd-redis.argocd.svc.cluster.local"
CERT_CMD="${CERT_CMD} --ip 127.0.0.1"

for addr in "${MANAGED_ADDRS[@]}"; do
    if [[ "${addr}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        CERT_CMD="${CERT_CMD} --ip ${addr}"
    else
        CERT_CMD="${CERT_CMD} --dns ${addr}"
    fi
done

eval "${CERT_CMD}"

echo "Configuring Redis for TLS..."
${AGENTCTL} redis configure-tls \
  --principal-context vcluster-agent-managed \
  --principal-namespace argocd


echo "Configuring Argo CD components for Redis TLS..."
${AGENTCTL} redis configure-argocd \
  --principal-context vcluster-agent-managed \
  --principal-namespace argocd

echo ""
echo "=== Agent Autonomous ==="
echo "Generating Redis TLS certificates..."
# Build cert generation command with LoadBalancer addresses in SAN
# Include default DNS names so Argo CD components can connect using service name
CERT_CMD="${AGENTCTL} redis generate-certs --principal-context vcluster-agent-autonomous --principal-namespace argocd"
CERT_CMD="${CERT_CMD} --dns localhost --dns argocd-redis --dns argocd-redis.argocd.svc.cluster.local"
CERT_CMD="${CERT_CMD} --ip 127.0.0.1"

for addr in "${AUTONOMOUS_ADDRS[@]}"; do
    if [[ "${addr}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        CERT_CMD="${CERT_CMD} --ip ${addr}"
    else
        CERT_CMD="${CERT_CMD} --dns ${addr}"
    fi
done

eval "${CERT_CMD}"

echo "Configuring Redis for TLS..."
${AGENTCTL} redis configure-tls \
  --principal-context vcluster-agent-autonomous \
  --principal-namespace argocd

echo "Configuring Argo CD components for Redis TLS..."
${AGENTCTL} redis configure-argocd \
  --principal-context vcluster-agent-autonomous \
  --principal-namespace argocd

echo ""
echo "=== Redis Proxy (Principal) ==="
echo "Generating Redis Proxy TLS certificate..."

LOCAL_IP=$(ip r show default 2>/dev/null | sed -e 's,.*\ src\ ,,' | sed -e 's,\ metric.*$,,' || echo "")
if [ -z "$LOCAL_IP" ]; then
    LOCAL_IP=$(ifconfig 2>/dev/null | grep "inet " | grep -v 127.0.0.1 | head -1 | awk '{print $2}' || echo "")
fi

${AGENTCTL} redis generate-certs \
  --principal-context vcluster-control-plane \
  --principal-namespace argocd \
  --secret-name argocd-redis-proxy-tls \
  --upsert \
  --dns localhost \
  --dns argocd-redis-proxy \
  --dns rathole-container-internal \
  --ip 127.0.0.1 \
  ${LOCAL_IP:+--ip ${LOCAL_IP}}

echo ""
echo "Scaling up Argo CD components..."

# Scale up Argo CD components that were scaled down during Redis TLS configuration
SCALE_FAILED=0
for context in vcluster-control-plane vcluster-agent-managed vcluster-agent-autonomous; do
    echo "  Scaling components in ${context}..."
    
    # Scale repo-server
    if ! kubectl scale deployment argocd-repo-server -n argocd --replicas=1 --context="${context}" 2>/dev/null; then
        echo "    WARNING: Failed to scale argocd-repo-server in ${context}"
        SCALE_FAILED=1
    fi
    
    # Scale application-controller
    if [ "${context}" != "vcluster-control-plane" ]; then
        if ! kubectl scale statefulset argocd-application-controller -n argocd --replicas=1 --context="${context}" 2>/dev/null; then
            echo "    WARNING: Failed to scale argocd-application-controller in ${context}"
            SCALE_FAILED=1
        fi
    fi
    
    # Scale argocd-server
    if [ "${context}" = "vcluster-control-plane" ]; then
        if ! kubectl scale deployment argocd-server -n argocd --replicas=1 --context="${context}" 2>/dev/null; then
            echo "    WARNING: Failed to scale argocd-server in ${context}"
            SCALE_FAILED=1
        fi
    fi
done

echo ""
echo "Waiting for Argo CD components to be ready..."
ROLLOUT_FAILED=0
for context in vcluster-control-plane vcluster-agent-managed vcluster-agent-autonomous; do
    echo "  Checking rollout status in ${context}..."
    
    # Wait for repo-server
    if ! kubectl rollout status deployment/argocd-repo-server -n argocd --context="${context}" --timeout=120s; then
        echo "    WARNING: argocd-repo-server failed to become ready in ${context}"
        ROLLOUT_FAILED=1
    fi
    
    # Wait for application-controller
    if [ "${context}" != "vcluster-control-plane" ]; then
        if ! kubectl rollout status statefulset/argocd-application-controller -n argocd --context="${context}" --timeout=120s; then
            echo "    WARNING: argocd-application-controller failed to become ready in ${context}"
            ROLLOUT_FAILED=1
        fi
    fi
    
    # Wait for argocd-server
    if [ "${context}" = "vcluster-control-plane" ]; then
        if ! kubectl rollout status deployment/argocd-server -n argocd --context="${context}" --timeout=120s; then
            echo "    WARNING: argocd-server failed to become ready in ${context}"
            ROLLOUT_FAILED=1
        fi
    fi
done

if [ ${SCALE_FAILED} -eq 1 ] || [ ${ROLLOUT_FAILED} -eq 1 ]; then
    echo ""
    echo "WARNING: Some Argo CD components may not be ready. E2E tests may fail."
    echo "Check component status with: kubectl get pods -n argocd --context=<context>"
fi

echo ""
echo "E2E environment ready with Redis TLS enabled"
echo ""
