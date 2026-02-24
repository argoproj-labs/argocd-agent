#!/bin/bash
# Run the primary principal for HA testing

SCRIPT_DIR="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"

exec "$SCRIPT_DIR/argocd-agent" principal \
    --listen-port 8443 \
    --metrics-port 8000 \
    --healthz-port 8003 \
    --allowed-namespaces '*' \
    --kubecontext kind-ha-test \
    --log-level debug \
    --namespace argocd \
    --insecure-tls-generate \
    --insecure-jwt-generate \
    --auth "mtls:" \
    --enable-resource-proxy=false \
    --ha-enabled \
    --ha-preferred-role primary \
    --ha-peer-address "localhost:8004" \
    --ha-failover-timeout 10s
