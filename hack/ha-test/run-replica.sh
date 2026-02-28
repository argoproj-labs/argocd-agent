#!/bin/bash
# Run the replica principal for HA testing

SCRIPT_DIR="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 || exit 1; pwd -P )"

exec "$SCRIPT_DIR/argocd-agent" principal \
    --listen-port 8444 \
    --metrics-port 8001 \
    --healthz-port 8004 \
    --allowed-namespaces '*' \
    --kubecontext kind-ha-test \
    --log-level debug \
    --namespace argocd \
    --tls-cert "$SCRIPT_DIR/certs/replica.crt" \
    --tls-key "$SCRIPT_DIR/certs/replica.key" \
    --root-ca-path "$SCRIPT_DIR/certs/ca.crt" \
    --insecure-jwt-generate \
    --auth "mtls:" \
    --enable-resource-proxy=false \
    --disable-redis-proxy \
    --ha-enabled \
    --ha-preferred-role replica \
    --ha-peer-address "localhost:8443" \
    --ha-replication-auth "mtls:subject:CN=([^,]+)" \
    --ha-admin-port 8406 \
    --ha-failover-timeout 10s
