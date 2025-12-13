#!/bin/bash
# Configure Argo CD components to use Redis TLS (for E2E tests and local development)
#
# PURPOSE: Required for E2E tests and local development environments.
# This script configures the upstream Argo CD installation (server, repo-server,
# application-controller) to connect to Redis using TLS with CA certificate validation.
#
# SCOPE:
# - Used by: make setup-e2e (E2E test environment setup)
# - Used by: Local development with Redis TLS
# - NOT for production: Users configure their own Argo CD installation
#
# NOTE: For control-plane, preserves redis.server (points to principal's proxy).
# For agent clusters, sets redis.server to local argocd-redis.

set -e

CONTEXT="${1:-vcluster-control-plane}"
NAMESPACE="argocd"

echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Configure Argo CD Components for Redis TLS             ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
echo "  Note: This is for E2E tests and local development only"
echo "  TLS encryption enabled with CA certificate validation"
echo ""

# Switch context
echo "Switching to context: ${CONTEXT}"
kubectl config use-context ${CONTEXT} || { 
    echo "ERROR: Failed to switch to context ${CONTEXT}"
    echo "Please verify the context exists: kubectl config get-contexts"
    exit 1
}

# Configure Redis server address via ConfigMap for agent clusters
# Note: For control-plane, redis.server is set by setup-vcluster-env.sh and should NOT be changed
#       It points to the principal's Redis proxy, which forwards to agent Redis instances
#       For agent clusters, we set redis.server to local argocd-redis
if [[ "$CONTEXT" != "vcluster-control-plane" ]]; then
    echo "Setting redis.server for agent cluster in argocd-cmd-params-cm..."
    kubectl -n ${NAMESPACE} patch configmap argocd-cmd-params-cm --type=merge -p '{
      "data": {
        "redis.server": "argocd-redis:6379"
      }
    }' 2>/dev/null || {
      echo "  Warning: ConfigMap not found, creating it"
      kubectl -n ${NAMESPACE} create configmap argocd-cmd-params-cm \
        --from-literal=redis.server="argocd-redis:6379" \
        --dry-run=client -o yaml | kubectl apply -f -
    }
    echo "  redis.server configured for agent cluster: argocd-redis:6379"
    echo ""
else
    echo "Skipping redis.server configuration for control-plane (uses Redis proxy)"
    # Display current redis.server setting for debugging
    CURRENT_REDIS_SERVER=$(kubectl -n ${NAMESPACE} get configmap argocd-cmd-params-cm -o jsonpath='{.data.redis\.server}' 2>/dev/null || echo "not set")
    echo "  Current redis.server: ${CURRENT_REDIS_SERVER}"
    echo ""
fi

# Configure argocd-server for Redis TLS (if it exists in this cluster)
if kubectl get deployment argocd-server -n ${NAMESPACE} &>/dev/null; then
    echo "Configuring argocd-server for Redis TLS..."
    
    # Check if volume already exists
    if ! kubectl get deployment argocd-server -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.volumes[?(@.name=="redis-tls-ca")]}' | grep -q "redis-tls-ca"; then
        echo "  Adding redis-tls-ca volume..."
        
        # Check if volumes array exists
        VOLUMES_EXIST=$(kubectl get deployment argocd-server -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.volumes}' 2>/dev/null || echo "")
        
        if [ -z "$VOLUMES_EXIST" ] || [ "$VOLUMES_EXIST" = "null" ]; then
            # Create volumes array with first element
            if ! kubectl -n ${NAMESPACE} patch deployment argocd-server --type=json -p '[
              {
                "op": "add",
                "path": "/spec/template/spec/volumes",
                "value": [{
                  "name": "redis-tls-ca",
                  "secret": {
                    "secretName": "argocd-redis-tls",
                    "items": [{"key": "ca.crt", "path": "ca.crt"}]
                  }
                }]
              }
            ]'; then
                echo "  ERROR: Failed to create volumes array and add redis-tls-ca volume to argocd-server"
                echo "  This is required for Redis TLS."
                exit 1
            fi
        else
            # Append to existing volumes array
            if ! kubectl -n ${NAMESPACE} patch deployment argocd-server --type=json -p '[
              {
                "op": "add",
                "path": "/spec/template/spec/volumes/-",
                "value": {
                  "name": "redis-tls-ca",
                  "secret": {
                    "secretName": "argocd-redis-tls",
                    "items": [{"key": "ca.crt", "path": "ca.crt"}]
                  }
                }
              }
            ]'; then
                echo "  ERROR: Failed to add redis-tls-ca volume to argocd-server"
                echo "  This is required for Redis TLS. Please check deployment structure."
                exit 1
            fi
        fi
    else
        echo "  redis-tls-ca volume already exists"
    fi
    
    # Check if volumeMount already exists
    if ! kubectl get deployment argocd-server -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.containers[0].volumeMounts[?(@.name=="redis-tls-ca")]}' | grep -q "redis-tls-ca"; then
        echo "  Adding redis-tls-ca volumeMount..."
        if ! kubectl -n ${NAMESPACE} patch deployment argocd-server --type=json -p '[
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/volumeMounts/-",
            "value": {
              "name": "redis-tls-ca",
              "mountPath": "/app/config/redis/tls",
              "readOnly": true
            }
          }
        ]'; then
            echo "  ERROR: Failed to add redis-tls-ca volumeMount to argocd-server"
            exit 1
        fi
    else
        echo "  redis-tls-ca volumeMount already exists"
    fi
    
    # Add Redis TLS args (append to existing args)
    # Check if args already contain redis-use-tls
    if ! kubectl get deployment argocd-server -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q "redis-use-tls"; then
        echo "  Adding Redis TLS args..."
        if ! kubectl -n ${NAMESPACE} patch deployment argocd-server --type=json -p '[
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/args/-",
            "value": "--redis-use-tls"
          },
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/args/-",
            "value": "--redis-ca-certificate=/app/config/redis/tls/ca.crt"
          }
        ]'; then
            echo "  ERROR: Failed to add Redis TLS args to argocd-server"
            exit 1
        fi
    else
        echo "  Redis TLS args already configured"
    fi
    
    echo "  argocd-server configured"
fi

# Configure argocd-repo-server for Redis TLS (if it exists in this cluster)
if kubectl get deployment argocd-repo-server -n ${NAMESPACE} &>/dev/null; then
    echo "Configuring argocd-repo-server for Redis TLS..."
    
    # Check if volume already exists
    if ! kubectl get deployment argocd-repo-server -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.volumes[?(@.name=="redis-tls-ca")]}' | grep -q "redis-tls-ca"; then
        echo "  Adding redis-tls-ca volume..."
        if ! kubectl -n ${NAMESPACE} patch deployment argocd-repo-server --type=json -p '[
          {
            "op": "add",
            "path": "/spec/template/spec/volumes/-",
            "value": {
              "name": "redis-tls-ca",
              "secret": {
                "secretName": "argocd-redis-tls",
                "items": [{"key": "ca.crt", "path": "ca.crt"}]
              }
            }
          }
        ]'; then
            echo "  ERROR: Failed to add redis-tls-ca volume to argocd-repo-server"
            exit 1
        fi
    else
        echo "  redis-tls-ca volume already exists"
    fi
    
    # Check if volumeMount already exists
    if ! kubectl get deployment argocd-repo-server -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.containers[0].volumeMounts[?(@.name=="redis-tls-ca")]}' | grep -q "redis-tls-ca"; then
        echo "  Adding redis-tls-ca volumeMount..."
        if ! kubectl -n ${NAMESPACE} patch deployment argocd-repo-server --type=json -p '[
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/volumeMounts/-",
            "value": {
              "name": "redis-tls-ca",
              "mountPath": "/app/config/redis/tls",
              "readOnly": true
            }
          }
        ]'; then
            echo "  ERROR: Failed to add redis-tls-ca volumeMount to argocd-repo-server"
            exit 1
        fi
    else
        echo "  redis-tls-ca volumeMount already exists"
    fi
    
    # Add Redis TLS args (append to existing args)
    if ! kubectl get deployment argocd-repo-server -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q "redis-use-tls"; then
        echo "  Adding Redis TLS args..."
        if ! kubectl -n ${NAMESPACE} patch deployment argocd-repo-server --type=json -p '[
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/args/-",
            "value": "--redis-use-tls"
          },
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/args/-",
            "value": "--redis-ca-certificate=/app/config/redis/tls/ca.crt"
          }
        ]'; then
            echo "  ERROR: Failed to add Redis TLS args to argocd-repo-server"
            exit 1
        fi
    else
        echo "  Redis TLS args already configured"
    fi
    
    echo "  argocd-repo-server configured"
fi

# Configure argocd-application-controller for Redis TLS (if it exists in this cluster)
if kubectl get statefulset argocd-application-controller -n ${NAMESPACE} &>/dev/null; then
    echo "Configuring argocd-application-controller for Redis TLS..."
    
    # Check if volume already exists
    if ! kubectl get statefulset argocd-application-controller -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.volumes[?(@.name=="redis-tls-ca")]}' | grep -q "redis-tls-ca"; then
        echo "  Adding redis-tls-ca volume..."
        
        # Check if volumes array exists
        VOLUMES_EXIST=$(kubectl get statefulset argocd-application-controller -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.volumes}' 2>/dev/null || echo "")
        if [ "$VOLUMES_EXIST" = "" ] || [ "$VOLUMES_EXIST" = "null" ]; then
            # Create volumes array with first element
            if ! kubectl -n ${NAMESPACE} patch statefulset argocd-application-controller --type=json -p '[
              {
                "op": "add",
                "path": "/spec/template/spec/volumes",
                "value": [{
                  "name": "redis-tls-ca",
                  "secret": {
                    "secretName": "argocd-redis-tls",
                    "items": [{"key": "ca.crt", "path": "ca.crt"}]
                  }
                }]
              }
            ]'; then
                echo "  ERROR: Failed to add redis-tls-ca volume to argocd-application-controller"
                exit 1
            fi
        else
            # Append to existing volumes array
            if ! kubectl -n ${NAMESPACE} patch statefulset argocd-application-controller --type=json -p '[
              {
                "op": "add",
                "path": "/spec/template/spec/volumes/-",
                "value": {
                  "name": "redis-tls-ca",
                  "secret": {
                    "secretName": "argocd-redis-tls",
                    "items": [{"key": "ca.crt", "path": "ca.crt"}]
                  }
                }
              }
            ]'; then
                echo "  ERROR: Failed to add redis-tls-ca volume to argocd-application-controller"
                exit 1
            fi
        fi
    else
        echo "  redis-tls-ca volume already exists"
    fi
    
    # Check if volumeMount already exists
    if ! kubectl get statefulset argocd-application-controller -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.containers[0].volumeMounts[?(@.name=="redis-tls-ca")]}' | grep -q "redis-tls-ca"; then
        echo "  Adding redis-tls-ca volumeMount..."
        
        # Check if volumeMounts array exists
        MOUNTS_EXIST=$(kubectl get statefulset argocd-application-controller -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.containers[0].volumeMounts}' 2>/dev/null || echo "")
        if [ "$MOUNTS_EXIST" = "" ] || [ "$MOUNTS_EXIST" = "null" ]; then
            # Create volumeMounts array with first element
            if ! kubectl -n ${NAMESPACE} patch statefulset argocd-application-controller --type=json -p '[
              {
                "op": "add",
                "path": "/spec/template/spec/containers/0/volumeMounts",
                "value": [{
                  "name": "redis-tls-ca",
                  "mountPath": "/app/config/redis/tls",
                  "readOnly": true
                }]
              }
            ]'; then
                echo "  ERROR: Failed to add redis-tls-ca volumeMount to argocd-application-controller"
                exit 1
            fi
        else
            # Append to existing volumeMounts array
            if ! kubectl -n ${NAMESPACE} patch statefulset argocd-application-controller --type=json -p '[
              {
                "op": "add",
                "path": "/spec/template/spec/containers/0/volumeMounts/-",
                "value": {
                  "name": "redis-tls-ca",
                  "mountPath": "/app/config/redis/tls",
                  "readOnly": true
                }
              }
            ]'; then
                echo "  ERROR: Failed to add redis-tls-ca volumeMount to argocd-application-controller"
                exit 1
            fi
        fi
    else
        echo "  redis-tls-ca volumeMount already exists"
    fi
    
    # Add Redis TLS args (append to existing args)
    if ! kubectl get statefulset argocd-application-controller -n ${NAMESPACE} -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q "redis-use-tls"; then
        echo "  Adding Redis TLS args..."
        if ! kubectl -n ${NAMESPACE} patch statefulset argocd-application-controller --type=json -p '[
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/args/-",
            "value": "--redis-use-tls"
          },
          {
            "op": "add",
            "path": "/spec/template/spec/containers/0/args/-",
            "value": "--redis-ca-certificate=/app/config/redis/tls/ca.crt"
          }
        ]'; then
            echo "  ERROR: Failed to add Redis TLS args to argocd-application-controller"
            exit 1
        fi
    else
        echo "  Redis TLS args already configured"
    fi
    
    echo "  argocd-application-controller configured"
fi

echo ""
echo "Scaling up Argo CD components with Redis TLS configuration..."
echo ""

# Read replica counts from ConfigMap (created by configure-redis-tls.sh)
# If ConfigMap doesn't exist, use default values
REPO_SERVER_REPLICAS=$(kubectl get configmap argocd-redis-tls-replicas -n ${NAMESPACE} -o jsonpath='{.data.repo-server}' 2>/dev/null || echo "1")
CONTROLLER_REPLICAS=$(kubectl get configmap argocd-redis-tls-replicas -n ${NAMESPACE} -o jsonpath='{.data.application-controller}' 2>/dev/null || echo "1")
SERVER_REPLICAS=$(kubectl get configmap argocd-redis-tls-replicas -n ${NAMESPACE} -o jsonpath='{.data.server}' 2>/dev/null || echo "1")

# Ensure we have at least 1 replica
if [ -z "$REPO_SERVER_REPLICAS" ] || [ "$REPO_SERVER_REPLICAS" = "0" ]; then
    REPO_SERVER_REPLICAS="1"
fi
if [ -z "$CONTROLLER_REPLICAS" ] || [ "$CONTROLLER_REPLICAS" = "0" ]; then
    CONTROLLER_REPLICAS="1"
fi
if [ -z "$SERVER_REPLICAS" ] || [ "$SERVER_REPLICAS" = "0" ]; then
    SERVER_REPLICAS="1"
fi

# Scale up components (they will start with the new TLS configuration)
if kubectl get deployment argocd-server -n ${NAMESPACE} &>/dev/null; then
    echo "Scaling up argocd-server to ${SERVER_REPLICAS} replicas..."
    kubectl scale deployment argocd-server -n ${NAMESPACE} --replicas=${SERVER_REPLICAS}
    kubectl rollout status deployment argocd-server -n ${NAMESPACE} --timeout=120s
fi

if kubectl get deployment argocd-repo-server -n ${NAMESPACE} &>/dev/null; then
    echo "Scaling up argocd-repo-server to ${REPO_SERVER_REPLICAS} replicas..."
    kubectl scale deployment argocd-repo-server -n ${NAMESPACE} --replicas=${REPO_SERVER_REPLICAS}
    kubectl rollout status deployment argocd-repo-server -n ${NAMESPACE} --timeout=120s
fi

if kubectl get statefulset argocd-application-controller -n ${NAMESPACE} &>/dev/null; then
    echo "Scaling up argocd-application-controller to ${CONTROLLER_REPLICAS} replicas..."
    kubectl scale statefulset argocd-application-controller -n ${NAMESPACE} --replicas=${CONTROLLER_REPLICAS}
    kubectl rollout status statefulset argocd-application-controller -n ${NAMESPACE} --timeout=120s
fi

# Clean up the temporary ConfigMap
kubectl delete configmap argocd-redis-tls-replicas -n ${NAMESPACE} 2>/dev/null || true

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Argo CD Redis TLS Configuration Complete!              ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
echo "Argo CD components will now connect to Redis using TLS with CA certificate validation."
echo ""
