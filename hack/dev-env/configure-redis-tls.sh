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

# Configure Redis Deployment for TLS

set -e

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
cd "${SCRIPTPATH}"

CONTEXT="${1:-vcluster-control-plane}"
NAMESPACE="argocd"

# Determine which certificate to use based on context
case "${CONTEXT}" in
  *control-plane*)
    REDIS_CERT_PREFIX="redis-control-plane"
    ;;
  *agent-managed*)
    REDIS_CERT_PREFIX="redis-managed"
    ;;
  *agent-autonomous*)
    REDIS_CERT_PREFIX="redis-autonomous"
    ;;
  *)
    echo "Error: Unknown context '${CONTEXT}'"
    echo "   Expected one of: vcluster-control-plane, vcluster-agent-managed, vcluster-agent-autonomous"
    exit 1
    ;;
esac

echo "Using certificates: ${REDIS_CERT_PREFIX}.{crt,key}"
echo ""

# Save initial context for cleanup
initial_context=$(kubectl config current-context)

cleanup() { 
    kubectl config use-context ${initial_context} 2>/dev/null || true
}

trap cleanup EXIT

echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Configure Redis Deployment for TLS                     ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# Check certificates exist
if [ ! -f "creds/redis-tls/${REDIS_CERT_PREFIX}.crt" ] || [ ! -f "creds/redis-tls/${REDIS_CERT_PREFIX}.key" ] || [ ! -f "creds/redis-tls/ca.crt" ]; then
    echo "Error: Redis TLS certificates not found (${REDIS_CERT_PREFIX}.crt, ${REDIS_CERT_PREFIX}.key, or ca.crt)"
    echo "Please run: ./gen-redis-tls-certs.sh"
    exit 1
fi

# Switch context
echo "Switching to context: ${CONTEXT}"
kubectl config use-context ${CONTEXT} || { 
    echo "ERROR: Failed to switch to context ${CONTEXT}"
    echo "Please verify the context exists: kubectl config get-contexts"
    exit 1
}

# Check Redis Deployment exists
if ! kubectl get deployment argocd-redis -n ${NAMESPACE} &>/dev/null; then
    echo "Error: argocd-redis Deployment not found in namespace ${NAMESPACE}"
    exit 1
fi

echo "Found Redis Deployment"
echo ""

# Scale down ArgoCD components that connect to Redis BEFORE enabling TLS
# This prevents SSL errors during the transition (old pods trying to connect without TLS)
echo "Scaling down ArgoCD components to prevent SSL errors during transition..."

# Save current replica counts for restoration by configure-argocd-redis-tls.sh
REPO_SERVER_REPLICAS=$(kubectl get deployment argocd-repo-server -n ${NAMESPACE} -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
CONTROLLER_REPLICAS=$(kubectl get statefulset argocd-application-controller -n ${NAMESPACE} -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
SERVER_REPLICAS=$(kubectl get deployment argocd-server -n ${NAMESPACE} -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")

# Store replica counts in a ConfigMap for the argocd-redis-tls script to use
kubectl create configmap argocd-redis-tls-replicas \
  --from-literal=repo-server=${REPO_SERVER_REPLICAS} \
  --from-literal=application-controller=${CONTROLLER_REPLICAS} \
  --from-literal=server=${SERVER_REPLICAS} \
  -n ${NAMESPACE} \
  --dry-run=client -o yaml | kubectl apply -f -

# Scale down components
if kubectl get deployment argocd-repo-server -n ${NAMESPACE} &>/dev/null; then
    kubectl scale deployment argocd-repo-server -n ${NAMESPACE} --replicas=0
    echo "  Scaled down argocd-repo-server"
fi

if kubectl get statefulset argocd-application-controller -n ${NAMESPACE} &>/dev/null; then
    kubectl scale statefulset argocd-application-controller -n ${NAMESPACE} --replicas=0
    echo "  Scaled down argocd-application-controller"
fi

if kubectl get deployment argocd-server -n ${NAMESPACE} &>/dev/null; then
    kubectl scale deployment argocd-server -n ${NAMESPACE} --replicas=0
    echo "  Scaled down argocd-server"
fi

# Wait for pods to terminate
echo "Waiting for ArgoCD pods to terminate..."
kubectl wait --for=delete pod -l app.kubernetes.io/name=argocd-repo-server -n ${NAMESPACE} --timeout=60s 2>/dev/null || true
kubectl wait --for=delete pod -l app.kubernetes.io/name=argocd-application-controller -n ${NAMESPACE} --timeout=60s 2>/dev/null || true
kubectl wait --for=delete pod -l app.kubernetes.io/name=argocd-server -n ${NAMESPACE} --timeout=60s 2>/dev/null || true

echo "ArgoCD components scaled down"
echo ""

# Create secret
echo "Creating TLS secret..."
kubectl create secret generic argocd-redis-tls \
  --from-file=tls.crt=creds/redis-tls/${REDIS_CERT_PREFIX}.crt \
  --from-file=tls.key=creds/redis-tls/${REDIS_CERT_PREFIX}.key \
  --from-file=ca.crt=creds/redis-tls/ca.crt \
  -n ${NAMESPACE} \
  --dry-run=client -o yaml | kubectl apply -f -

echo "Secret created"
echo ""

# Patch deployment for TLS
echo "Patching Redis deployment for TLS..."

# Check if redis-tls volume already exists
VOLUME_EXISTS=$(kubectl get deployment argocd-redis -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.volumes[?(@.name=="redis-tls")].name}' 2>/dev/null || echo "")

if [ -z "$VOLUME_EXISTS" ]; then
    echo "Adding redis-tls volume..."
    # Check if volumes array exists
    VOLUMES_EXIST=$(kubectl get deployment argocd-redis -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.volumes}' 2>/dev/null || echo "")
    if [ "$VOLUMES_EXIST" = "" ] || [ "$VOLUMES_EXIST" = "null" ]; then
        # Create volumes array with first element
        kubectl patch deployment argocd-redis -n ${NAMESPACE} --type='json' -p='[
          {
            "op": "add",
            "path": "/spec/template/spec/volumes",
            "value": [{"name": "redis-tls", "secret": {"secretName": "argocd-redis-tls"}}]
          }
        ]' || { echo "Failed to add volume"; exit 1; }
    else
        # Append to existing volumes array
        kubectl patch deployment argocd-redis -n ${NAMESPACE} --type='json' -p='[
          {
            "op": "add",
            "path": "/spec/template/spec/volumes/-",
            "value": {"name": "redis-tls", "secret": {"secretName": "argocd-redis-tls"}}
          }
        ]' || { echo "Failed to add volume"; exit 1; }
    fi
else
    echo "redis-tls volume already exists, skipping..."
fi

# Check if redis-tls volumeMount already exists
MOUNT_EXISTS=$(kubectl get deployment argocd-redis -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.containers[0].volumeMounts[?(@.name=="redis-tls")].name}' 2>/dev/null || echo "")

if [ -z "$MOUNT_EXISTS" ]; then
    echo "Adding redis-tls volumeMount..."
    # Check if volumeMounts array exists
    MOUNTS_EXIST=$(kubectl get deployment argocd-redis -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.containers[0].volumeMounts}' 2>/dev/null || echo "")
    if [ "$MOUNTS_EXIST" = "" ] || [ "$MOUNTS_EXIST" = "null" ]; then
        # Create volumeMounts array with first element
        kubectl patch deployment argocd-redis -n ${NAMESPACE} --type='json' -p='[
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/volumeMounts",
            "value": [{"name": "redis-tls", "mountPath": "/app/tls"}]
          }
        ]' || { echo "Failed to add volumeMount"; exit 1; }
    else
        # Append to existing volumeMounts array
        kubectl patch deployment argocd-redis -n ${NAMESPACE} --type='json' -p='[
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/volumeMounts/-",
            "value": {"name": "redis-tls", "mountPath": "/app/tls"}
          }
        ]' || { echo "Failed to add volumeMount"; exit 1; }
    fi
else
    echo "redis-tls volumeMount already exists, skipping..."
fi

# Update Redis args for TLS
# Get the Redis password from the secret
REDIS_PASSWORD=$(kubectl -n ${NAMESPACE} get secret argocd-redis -o jsonpath='{.data.auth}' | base64 --decode 2>/dev/null || echo "")

if [ -z "$REDIS_PASSWORD" ]; then
    echo "ERROR: Redis password not found in secret argocd-redis"
    echo ""
    echo "The argocd-redis secret is required for E2E tests and should have been"
    echo "created during setup (via hack/dev-env/common/redis-secret.yaml)."
    echo ""
    echo "Please run: make setup-e2e"
    echo ""
    exit 1
fi

kubectl patch deployment argocd-redis -n ${NAMESPACE} --type='json' -p='[
  {
    "op": "replace",
    "path": "/spec/template/spec/containers/0/args",
    "value": [
      "--save", "",
      "--appendonly", "no",
      "--requirepass", "'"${REDIS_PASSWORD}"'",
      "--tls-port", "6379",
      "--port", "0",
      "--tls-cert-file", "/app/tls/tls.crt",
      "--tls-key-file", "/app/tls/tls.key",
      "--tls-ca-cert-file", "/app/tls/ca.crt",
      "--tls-auth-clients", "no"
    ]
  }
]' || { echo "ERROR: Failed to update Redis args for TLS"; exit 1; }

echo "Deployment patched"
echo ""

# Wait for rollout
echo "Waiting for deployment rollout..."
kubectl rollout status --watch deployment/argocd-redis -n ${NAMESPACE} --timeout=120s

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Redis TLS Configuration Complete!                   ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# Verify
REDIS_POD=$(kubectl get pod -n ${NAMESPACE} -l app.kubernetes.io/name=argocd-redis -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

if [ -n "$REDIS_POD" ]; then
    POD_STATUS=$(kubectl get pod ${REDIS_POD} -n ${NAMESPACE} -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
    if [ "$POD_STATUS" = "Running" ]; then
        echo " Redis pod ${REDIS_POD} is running with TLS"
    elif [ "$POD_STATUS" = "Unknown" ]; then
        echo " Could not verify pod status (pod may have restarted during rollout)"
    else
        echo " Redis pod ${REDIS_POD} status: ${POD_STATUS}"
    fi
else
    echo " Could not find Redis pod (may still be starting)"
fi

echo ""
