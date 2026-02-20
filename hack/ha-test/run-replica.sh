#!/bin/bash
# Run the replica principal for HA testing

SCRIPT_DIR="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"

exec "$SCRIPT_DIR/argocd-agent" principal \
    --listen-port 8444 \
    --metrics-port 8001 \
    --healthz-port 8004 \
    --allowed-namespaces '*' \
    --kubecontext kind-ha-test \
    --log-level debug \
    --namespace argocd \
    --insecure-tls-generate \
    --insecure-jwt-generate \
    --auth "mtls:" \
    --enable-resource-proxy=false \
    --disable-redis-proxy \
    --ha-enabled \
    --ha-preferred-role replica \
    --ha-peer-address "localhost:8003" \
    --ha-failover-timeout 10s
