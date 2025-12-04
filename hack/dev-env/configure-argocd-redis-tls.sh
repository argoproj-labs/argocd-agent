#!/bin/bash
# Configure Argo CD components to use Redis TLS (for E2E tests and local development)
#
# PURPOSE: Required for E2E tests and local development environments.
# This script configures the upstream Argo CD installation (server, repo-server,
# application-controller) to connect to the principal's Redis proxy using TLS.
#
# SCOPE:
# - Used by: make setup-e2e (E2E test environment setup)
# - Used by: Local development with Redis TLS
# - NOT for production: Users configure their own Argo CD installation
#
# NOTE: Uses insecure-skip-tls-verify because local IPs are not in certificate SANs.
# TLS encryption is still enabled, only hostname verification is skipped.

set -e

CONTEXT="${1:-vcluster-control-plane}"
NAMESPACE="argocd"

echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Configure Argo CD Components for Redis TLS             ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
echo "  Note: This is for E2E tests and local development only"
echo "  TLS encryption enabled, hostname verification skipped"
echo ""

# Switch context
echo "Switching to context: ${CONTEXT}"
kubectl config use-context ${CONTEXT}

# Configure Redis TLS via ConfigMap (shared by all Argo CD components)
# Note: For control-plane, preserve redis.server address set by setup-vcluster-env.sh (principal's proxy)
#       For agent clusters, set redis.server to local argocd-redis
# For E2E tests: Use insecure-skip-tls-verify because connections may use dynamic IPs
echo "Configuring Redis TLS settings in argocd-cmd-params-cm..."

# Check if this is an agent cluster or control-plane
if [[ "$CONTEXT" == "vcluster-control-plane" ]]; then
    # Control-plane: Only set TLS flags, preserve redis.server (points to principal's proxy)
    kubectl -n ${NAMESPACE} patch configmap argocd-cmd-params-cm --type=merge -p '{
      "data": {
        "redis.use.tls": "true",
        "redis.insecure.skip.tls.verify": "true"
      }
    }' 2>/dev/null || {
      echo "  Warning: ConfigMap not found, creating with default redis.server"
      kubectl -n ${NAMESPACE} create configmap argocd-cmd-params-cm \
        --from-literal=redis.server="argocd-redis:6379" \
        --from-literal=redis.use.tls="true" \
        --from-literal=redis.insecure.skip.tls.verify="true" \
        --dry-run=client -o yaml | kubectl apply -f -
    }
else
    # Agent clusters: Set both redis.server (local Redis) and TLS flags
    kubectl -n ${NAMESPACE} patch configmap argocd-cmd-params-cm --type=merge -p '{
      "data": {
        "redis.server": "argocd-redis:6379",
        "redis.use.tls": "true",
        "redis.insecure.skip.tls.verify": "true"
      }
    }' 2>/dev/null || {
      echo "  Warning: ConfigMap not found, creating it"
      kubectl -n ${NAMESPACE} create configmap argocd-cmd-params-cm \
        --from-literal=redis.server="argocd-redis:6379" \
        --from-literal=redis.use.tls="true" \
        --from-literal=redis.insecure.skip.tls.verify="true" \
        --dry-run=client -o yaml | kubectl apply -f -
    }
fi
echo "  Redis TLS configured (encryption enabled, hostname verification skipped for E2E)"
echo ""

# Note: No volume mounts needed for argocd-server since we're using insecure-skip-tls-verify
if kubectl get deployment argocd-server -n ${NAMESPACE} &>/dev/null; then
    echo "  argocd-server will use ConfigMap settings (TLS without certificate validation)"
fi

# Note: No volume mounts needed for argocd-repo-server since we're using insecure-skip-tls-verify
if kubectl get deployment argocd-repo-server -n ${NAMESPACE} &>/dev/null; then
    echo "  argocd-repo-server will use ConfigMap settings (TLS without certificate validation)"
fi

# Note: No volume mounts needed for argocd-application-controller since we're using insecure-skip-tls-verify
if kubectl get statefulset argocd-application-controller -n ${NAMESPACE} &>/dev/null; then
    echo "  argocd-application-controller will use ConfigMap settings (TLS without certificate validation)"
fi

echo ""
echo " Restarting Argo CD components to apply Redis TLS configuration..."
echo ""

# Restart deployments to pick up ConfigMap changes
if kubectl get deployment argocd-server -n ${NAMESPACE} &>/dev/null; then
    kubectl rollout restart deployment argocd-server -n ${NAMESPACE}
    kubectl rollout status deployment argocd-server -n ${NAMESPACE} --timeout=120s
fi

if kubectl get deployment argocd-repo-server -n ${NAMESPACE} &>/dev/null; then
    kubectl rollout restart deployment argocd-repo-server -n ${NAMESPACE}
    kubectl rollout status deployment argocd-repo-server -n ${NAMESPACE} --timeout=120s
fi

if kubectl get statefulset argocd-application-controller -n ${NAMESPACE} &>/dev/null; then
    kubectl rollout restart statefulset argocd-application-controller -n ${NAMESPACE}
    kubectl rollout status statefulset argocd-application-controller -n ${NAMESPACE} --timeout=120s
fi

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Argo CD Redis TLS Configuration Complete!              ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
echo "Argo CD components will now connect to Redis using TLS (encryption enabled)."
echo "Note: Certificate validation is skipped for E2E tests (dynamic IPs)."
echo ""
