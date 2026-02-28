#!/bin/bash
# Copyright 2024 The argocd-agent Authors
#
# Test script to run two principals in HA mode locally without Kubernetes
# This is a minimal test to verify HA state transitions work.

set -e

SCRIPT_DIR="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
PROJECT_ROOT="$SCRIPT_DIR/../.."

# Build the binary first
echo "Building argocd-agent..."
cd "$PROJECT_ROOT"
go build -o /tmp/argocd-agent ./cmd/argocd-agent

# Configuration
PRIMARY_PORT=8443
PRIMARY_METRICS=8000
PRIMARY_HEALTH=8003

REPLICA_PORT=8444
REPLICA_METRICS=8001
REPLICA_HEALTH=8004

# Cleanup function
cleanup() {
    local rc=$?
    echo "Cleaning up..."
    kill $PRIMARY_PID 2>/dev/null || true
    kill $REPLICA_PID 2>/dev/null || true
    exit $rc
}
trap cleanup SIGINT SIGTERM EXIT

echo "=============================================="
echo "Starting HA Test Environment"
echo "=============================================="
echo ""
echo "This will start two principals:"
echo "  Primary:  localhost:$PRIMARY_PORT (metrics: $PRIMARY_METRICS, health: $PRIMARY_HEALTH)"
echo "  Replica:  localhost:$REPLICA_PORT (metrics: $REPLICA_METRICS, health: $REPLICA_HEALTH)"
echo ""
echo "Press Ctrl+C to stop both principals"
echo "=============================================="
echo ""

# Note: This requires the vcluster setup for the kubecontext
# Check if context exists
if ! kubectl config get-contexts vcluster-control-plane &>/dev/null; then
    echo "ERROR: vcluster-control-plane context not found."
    echo ""
    echo "The HA test requires the vcluster dev environment to be set up."
    echo "Run: ./hack/dev-env/setup-vcluster-env.sh create"
    echo ""
    echo "Alternatively, to just verify the code compiles and tests pass:"
    echo "  go test ./pkg/ha/... ./pkg/replication/... ./principal/..."
    exit 1
fi

# Get Redis address
if test "${ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS}" = ""; then
    ipaddr=$(kubectl --context vcluster-control-plane -n argocd get svc argocd-redis -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
    hostname=$(kubectl --context vcluster-control-plane -n argocd get svc argocd-redis -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)
    if test "$ipaddr" != ""; then
        export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS=$ipaddr:6379
    elif test "$hostname" != ""; then
        export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS=$hostname:6379
    else
        echo "Could not determine Redis server address." >&2
        exit 1
    fi
fi

if test "${REDIS_PASSWORD}" = ""; then
    _rp=$(kubectl get secret argocd-redis --context=vcluster-control-plane -n argocd -o jsonpath='{.data.auth}' | base64 --decode) || {
        echo "Failed to retrieve Redis password" >&2
        exit 1
    }
    if test -z "$_rp"; then
        echo "Redis password is empty" >&2
        exit 1
    fi
    export REDIS_PASSWORD="$_rp"
fi

echo "Starting PRIMARY principal (preferred role: primary)..."
/tmp/argocd-agent principal \
    --listen-port $PRIMARY_PORT \
    --metrics-port $PRIMARY_METRICS \
    --healthz-port $PRIMARY_HEALTH \
    --allowed-namespaces '*' \
    --kubecontext vcluster-control-plane \
    --log-level debug \
    --namespace argocd \
    --auth "mtls:CN=([^,]+)" \
    --ha-enabled \
    --ha-preferred-role primary \
    --ha-peer-address "localhost:$REPLICA_PORT" \
    --ha-replication-auth "mtls:CN=([^,]+)" \
    --ha-failover-timeout 10s \
    2>&1 | sed 's/^/[PRIMARY] /' &
PRIMARY_PID=$!

sleep 2

echo "Starting REPLICA principal (preferred role: replica)..."
/tmp/argocd-agent principal \
    --listen-port $REPLICA_PORT \
    --metrics-port $REPLICA_METRICS \
    --healthz-port $REPLICA_HEALTH \
    --allowed-namespaces '*' \
    --kubecontext vcluster-control-plane \
    --log-level debug \
    --namespace argocd \
    --auth "mtls:CN=([^,]+)" \
    --ha-enabled \
    --ha-preferred-role replica \
    --ha-peer-address "localhost:$PRIMARY_PORT" \
    --ha-replication-auth "mtls:CN=([^,]+)" \
    --ha-admin-port 8406 \
    --ha-failover-timeout 10s \
    2>&1 | sed 's/^/[REPLICA] /' &
REPLICA_PID=$!

echo ""
echo "Both principals started. Watching logs..."
echo "You can test failover by killing the PRIMARY process (PID: $PRIMARY_PID)"
echo ""

# Wait for both processes
wait
