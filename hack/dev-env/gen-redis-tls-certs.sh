#!/bin/bash
# Generate Redis TLS certificates for development and testing

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CREDS_DIR="${SCRIPT_DIR}/creds/redis-tls"

# Create directory for Redis TLS credentials
mkdir -p "${CREDS_DIR}"

echo "Generating Redis TLS certificates in ${CREDS_DIR}..."

# Generate CA private key and certificate
if [[ ! -f "${CREDS_DIR}/ca.key" ]]; then
    echo "Generating CA key and certificate..."
    openssl genrsa -out "${CREDS_DIR}/ca.key" 4096 2>/dev/null
    openssl req -new -x509 -days 3650 -key "${CREDS_DIR}/ca.key" \
        -out "${CREDS_DIR}/ca.crt" \
        -subj "/C=US/ST=State/L=City/O=Organization/OU=Unit/CN=Redis CA" 2>/dev/null
elif [[ ! -f "${CREDS_DIR}/ca.crt" ]]; then
    echo "Generating CA certificate..."
    openssl req -new -x509 -days 3650 -key "${CREDS_DIR}/ca.key" \
        -out "${CREDS_DIR}/ca.crt" \
        -subj "/C=US/ST=State/L=City/O=Organization/OU=Unit/CN=Redis CA" 2>/dev/null
fi

# Generate Redis server certificate for control-plane
if [[ ! -f "${CREDS_DIR}/redis-control-plane.key" ]]; then
    echo "Generating redis-control-plane certificate..."
    openssl genrsa -out "${CREDS_DIR}/redis-control-plane.key" 4096 2>/dev/null
fi

if [[ ! -f "${CREDS_DIR}/redis-control-plane.crt" ]]; then
    # Create extension file for SAN
    cat > "${CREDS_DIR}/redis-control-plane.ext" <<EOF
subjectAltName = @alt_names
[alt_names]
DNS.1 = argocd-redis
DNS.2 = argocd-redis.argocd
DNS.3 = argocd-redis.argocd.svc
DNS.4 = argocd-redis.argocd.svc.cluster.local
DNS.5 = localhost
IP.1 = 127.0.0.1
EOF

    openssl req -new -key "${CREDS_DIR}/redis-control-plane.key" \
        -out "${CREDS_DIR}/redis-control-plane.csr" \
        -subj "/C=US/ST=State/L=City/O=Organization/OU=Unit/CN=argocd-redis" 2>/dev/null

    openssl x509 -req -in "${CREDS_DIR}/redis-control-plane.csr" \
        -CA "${CREDS_DIR}/ca.crt" \
        -CAkey "${CREDS_DIR}/ca.key" \
        -CAcreateserial \
        -out "${CREDS_DIR}/redis-control-plane.crt" \
        -days 365 \
        -extfile "${CREDS_DIR}/redis-control-plane.ext" 2>/dev/null
fi

# Generate Redis proxy certificate (for principal's Redis proxy)
if [[ ! -f "${CREDS_DIR}/redis-proxy.key" ]]; then
    echo "Generating redis-proxy certificate..."
    openssl genrsa -out "${CREDS_DIR}/redis-proxy.key" 4096 2>/dev/null
fi

if [[ ! -f "${CREDS_DIR}/redis-proxy.crt" ]]; then
    cat > "${CREDS_DIR}/redis-proxy.ext" <<EOF
subjectAltName = @alt_names
[alt_names]
DNS.1 = argocd-redis-proxy
DNS.2 = argocd-redis-proxy.argocd
DNS.3 = argocd-redis-proxy.argocd.svc
DNS.4 = argocd-redis-proxy.argocd.svc.cluster.local
DNS.5 = localhost
IP.1 = 127.0.0.1
IP.2 = 127.0.0.2
EOF

    openssl req -new -key "${CREDS_DIR}/redis-proxy.key" \
        -out "${CREDS_DIR}/redis-proxy.csr" \
        -subj "/C=US/ST=State/L=City/O=Organization/OU=Unit/CN=argocd-redis-proxy" 2>/dev/null

    openssl x509 -req -in "${CREDS_DIR}/redis-proxy.csr" \
        -CA "${CREDS_DIR}/ca.crt" \
        -CAkey "${CREDS_DIR}/ca.key" \
        -CAcreateserial \
        -out "${CREDS_DIR}/redis-proxy.crt" \
        -days 365 \
        -extfile "${CREDS_DIR}/redis-proxy.ext" 2>/dev/null
fi

# Generate Redis certificates for agent vclusters
for agent in autonomous managed; do
    if [[ ! -f "${CREDS_DIR}/redis-${agent}.key" ]]; then
        echo "Generating redis-${agent} certificate..."
        openssl genrsa -out "${CREDS_DIR}/redis-${agent}.key" 4096 2>/dev/null
    fi

    if [[ ! -f "${CREDS_DIR}/redis-${agent}.crt" ]]; then
        cat > "${CREDS_DIR}/redis-${agent}.ext" <<EOF
subjectAltName = @alt_names
[alt_names]
DNS.1 = argocd-redis
DNS.2 = argocd-redis.argocd
DNS.3 = argocd-redis.argocd.svc
DNS.4 = argocd-redis.argocd.svc.cluster.local
DNS.5 = localhost
IP.1 = 127.0.0.1
EOF

        openssl req -new -key "${CREDS_DIR}/redis-${agent}.key" \
            -out "${CREDS_DIR}/redis-${agent}.csr" \
            -subj "/C=US/ST=State/L=City/O=Organization/OU=Unit/CN=argocd-redis-${agent}" 2>/dev/null

        openssl x509 -req -in "${CREDS_DIR}/redis-${agent}.csr" \
            -CA "${CREDS_DIR}/ca.crt" \
            -CAkey "${CREDS_DIR}/ca.key" \
            -CAcreateserial \
            -out "${CREDS_DIR}/redis-${agent}.crt" \
            -days 365 \
            -extfile "${CREDS_DIR}/redis-${agent}.ext" 2>/dev/null
    fi
done

echo ""
echo "Cleaning up temporary files..."
rm -f "${CREDS_DIR}"/*.csr "${CREDS_DIR}"/*.ext "${CREDS_DIR}"/*.srl

echo ""
echo "Redis TLS certificates generated successfully!"
echo ""
echo "Generated files in ${CREDS_DIR}:"
echo "  - ca.crt, ca.key (CA)"
echo "  - redis-control-plane.{crt,key}"
echo "  - redis-proxy.{crt,key}"
echo "  - redis-autonomous.{crt,key}"
echo "  - redis-managed.{crt,key}"
