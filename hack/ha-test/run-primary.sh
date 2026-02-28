#!/bin/bash
# Run the primary principal for HA testing

SCRIPT_DIR="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 || exit 1; pwd -P )"

exec "$SCRIPT_DIR/argocd-agent" principal \
    --listen-port 8443 \
    --metrics-port 8000 \
    --healthz-port 8003 \
    --allowed-namespaces '*' \
    --kubecontext kind-ha-test \
    --log-level debug \
    --namespace argocd \
    --tls-cert "$SCRIPT_DIR/certs/primary.crt" \
    --tls-key "$SCRIPT_DIR/certs/primary.key" \
    --root-ca-path "$SCRIPT_DIR/certs/ca.crt" \
    --insecure-jwt-generate \
    --auth "mtls:" \
    --enable-resource-proxy=false \
    --disable-redis-proxy \
    --ha-enabled \
    --ha-preferred-role primary \
    --ha-peer-address "localhost:8444" \
    --ha-replication-auth "mtls:subject:CN=([^,]+)" \
    --ha-failover-timeout 10s
