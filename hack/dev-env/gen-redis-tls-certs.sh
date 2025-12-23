#!/bin/bash
# Generate Redis TLS certificates for development and testing
# Uses the existing agent CA (from hack/dev-env/creds/ca.{crt,key})

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AGENT_CA_DIR="${SCRIPT_DIR}/creds"
REDIS_CREDS_DIR="${SCRIPT_DIR}/creds/redis-tls"

# Create directory for Redis TLS credentials
mkdir -p "${REDIS_CREDS_DIR}"

echo "Generating Redis TLS certificates using agent CA..."
echo ""

# Check if agent CA files exist on disk
if [[ ! -f "${AGENT_CA_DIR}/ca.key" ]] || [[ ! -f "${AGENT_CA_DIR}/ca.crt" ]]; then
    echo "⚠ Agent CA files not found on disk, extracting from Kubernetes secret..."
    
    # Try to extract from Kubernetes secret
    if ! kubectl get secret argocd-agent-ca -n argocd --context vcluster-control-plane >/dev/null 2>&1; then
        echo ""
        echo "ERROR: Agent CA not found!"
        echo "Expected:"
        echo "  - Files: ${AGENT_CA_DIR}/ca.{crt,key}"
        echo "  - OR Kubernetes secret: argocd-agent-ca (in vcluster-control-plane/argocd)"
        echo ""
        echo "Please run 'make setup-e2e' to initialize the PKI infrastructure first."
        exit 1
    fi
    
    # Extract CA from secret
    echo "  Extracting ca.crt from argocd-agent-ca secret..."
    kubectl get secret argocd-agent-ca -n argocd --context vcluster-control-plane \
        -o jsonpath='{.data.tls\.crt}' | base64 -d > "${AGENT_CA_DIR}/ca.crt"
    
    echo "  Extracting ca.key from argocd-agent-ca secret..."
    kubectl get secret argocd-agent-ca -n argocd --context vcluster-control-plane \
        -o jsonpath='{.data.tls\.key}' | base64 -d > "${AGENT_CA_DIR}/ca.key"
    
    chmod 600 "${AGENT_CA_DIR}/ca.key"
    echo " Extracted agent CA from Kubernetes secret"
else
    echo " Using existing agent CA from ${AGENT_CA_DIR}"
fi

echo ""

# Copy CA certificate to redis-tls directory for convenience
cp "${AGENT_CA_DIR}/ca.crt" "${REDIS_CREDS_DIR}/ca.crt"
echo "✓ Copied agent CA certificate to ${REDIS_CREDS_DIR}/ca.crt"
echo ""

# Generate Redis server certificate for control-plane
if [[ ! -f "${REDIS_CREDS_DIR}/redis-control-plane.key" ]]; then
    echo "Generating redis-control-plane private key..."
    openssl genrsa -out "${REDIS_CREDS_DIR}/redis-control-plane.key" 4096
fi

# Always regenerate certificate to include LoadBalancer IPs if available
echo "Generating redis-control-plane certificate (signed by agent CA)..."

# Try to get LoadBalancer IP/hostname if vcluster exists
LB_IP=$(kubectl get svc argocd-redis --context="vcluster-control-plane" -n argocd -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
LB_HOSTNAME=$(kubectl get svc argocd-redis --context="vcluster-control-plane" -n argocd -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || echo "")

    # Create extension file for SAN
    cat > "${REDIS_CREDS_DIR}/redis-control-plane.ext" <<EOF
subjectAltName = @alt_names
[alt_names]
DNS.1 = argocd-redis
DNS.2 = argocd-redis.argocd
DNS.3 = argocd-redis.argocd.svc
DNS.4 = argocd-redis.argocd.svc.cluster.local
DNS.5 = localhost
IP.1 = 127.0.0.1
EOF

# Add LoadBalancer address if available
if [ -n "${LB_IP}" ]; then
    echo "  Adding LoadBalancer IP: ${LB_IP}"
    echo "IP.2 = ${LB_IP}" >> "${REDIS_CREDS_DIR}/redis-control-plane.ext"
elif [ -n "${LB_HOSTNAME}" ]; then
    echo "  Adding LoadBalancer hostname: ${LB_HOSTNAME}"
    echo "DNS.6 = ${LB_HOSTNAME}" >> "${REDIS_CREDS_DIR}/redis-control-plane.ext"
else
    echo "  No LoadBalancer address found (OK if vclusters not created yet)"
fi

    openssl req -new -key "${REDIS_CREDS_DIR}/redis-control-plane.key" \
        -out "${REDIS_CREDS_DIR}/redis-control-plane.csr" \
        -subj "/C=US/ST=State/L=City/O=Organization/OU=Unit/CN=argocd-redis"

    openssl x509 -req -in "${REDIS_CREDS_DIR}/redis-control-plane.csr" \
        -CA "${AGENT_CA_DIR}/ca.crt" \
        -CAkey "${AGENT_CA_DIR}/ca.key" \
        -CAcreateserial \
        -out "${REDIS_CREDS_DIR}/redis-control-plane.crt" \
        -days 365 \
        -extfile "${REDIS_CREDS_DIR}/redis-control-plane.ext"

echo " Generated redis-control-plane certificate"
echo ""

# Generate Redis proxy certificate (for principal's Redis proxy)
if [[ ! -f "${REDIS_CREDS_DIR}/redis-proxy.key" ]]; then
    echo "Generating redis-proxy private key..."
    openssl genrsa -out "${REDIS_CREDS_DIR}/redis-proxy.key" 4096
fi

echo "Generating redis-proxy certificate (signed by agent CA)..."

# Get local machine IP for certificate SANs
if [[ "$OSTYPE" == "darwin"* ]]; then
    LOCAL_IP=$(ipconfig getifaddr en0 2>/dev/null || echo "")
else
    LOCAL_IP=$(ip r show default 2>/dev/null | sed -e 's,.*\ src\ ,,' | sed -e 's,\ metric.*$,,' | head -n 1 || echo "")
fi

cat > "${REDIS_CREDS_DIR}/redis-proxy.ext" <<EOF
subjectAltName = @alt_names
[alt_names]
DNS.1 = argocd-redis-proxy
DNS.2 = argocd-redis-proxy.argocd
DNS.3 = argocd-redis-proxy.argocd.svc
DNS.4 = argocd-redis-proxy.argocd.svc.cluster.local
DNS.5 = localhost
DNS.6 = rathole-container-internal
IP.1 = 127.0.0.1
IP.2 = 127.0.0.2
EOF

# Only add local IP if detected
if [ -n "${LOCAL_IP}" ]; then
    echo "  Adding local IP: ${LOCAL_IP}"
    echo "IP.3 = ${LOCAL_IP}" >> "${REDIS_CREDS_DIR}/redis-proxy.ext"
fi

openssl req -new -key "${REDIS_CREDS_DIR}/redis-proxy.key" \
    -out "${REDIS_CREDS_DIR}/redis-proxy.csr" \
    -subj "/C=US/ST=State/L=City/O=Organization/OU=Unit/CN=argocd-redis-proxy"

openssl x509 -req -in "${REDIS_CREDS_DIR}/redis-proxy.csr" \
    -CA "${AGENT_CA_DIR}/ca.crt" \
    -CAkey "${AGENT_CA_DIR}/ca.key" \
    -CAcreateserial \
    -out "${REDIS_CREDS_DIR}/redis-proxy.crt" \
    -days 365 \
    -extfile "${REDIS_CREDS_DIR}/redis-proxy.ext"

echo " Generated redis-proxy certificate"
echo ""

# Generate Redis certificates for agent vclusters
for agent in autonomous managed; do
    if [[ ! -f "${REDIS_CREDS_DIR}/redis-${agent}.key" ]]; then
        echo "Generating redis-${agent} private key..."
        openssl genrsa -out "${REDIS_CREDS_DIR}/redis-${agent}.key" 4096
    fi

    # Always regenerate certificate to include LoadBalancer IPs if available
    echo "Generating redis-${agent} certificate (signed by agent CA)..."
    
    # Try to get LoadBalancer IP/hostname if vcluster exists
    CONTEXT="vcluster-agent-${agent}"
    LB_IP=$(kubectl get svc argocd-redis --context="${CONTEXT}" -n argocd -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
    LB_HOSTNAME=$(kubectl get svc argocd-redis --context="${CONTEXT}" -n argocd -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || echo "")
    
        cat > "${REDIS_CREDS_DIR}/redis-${agent}.ext" <<EOF
subjectAltName = @alt_names
[alt_names]
DNS.1 = argocd-redis
DNS.2 = argocd-redis.argocd
DNS.3 = argocd-redis.argocd.svc
DNS.4 = argocd-redis.argocd.svc.cluster.local
DNS.5 = localhost
IP.1 = 127.0.0.1
EOF

    # Add LoadBalancer address if available
    if [ -n "${LB_IP}" ]; then
        echo "  Adding LoadBalancer IP: ${LB_IP}"
        echo "IP.2 = ${LB_IP}" >> "${REDIS_CREDS_DIR}/redis-${agent}.ext"
    elif [ -n "${LB_HOSTNAME}" ]; then
        echo "  Adding LoadBalancer hostname: ${LB_HOSTNAME}"
        echo "DNS.6 = ${LB_HOSTNAME}" >> "${REDIS_CREDS_DIR}/redis-${agent}.ext"
    else
        echo "  No LoadBalancer address found (OK if vclusters not created yet)"
    fi

        openssl req -new -key "${REDIS_CREDS_DIR}/redis-${agent}.key" \
            -out "${REDIS_CREDS_DIR}/redis-${agent}.csr" \
            -subj "/C=US/ST=State/L=City/O=Organization/OU=Unit/CN=argocd-redis-${agent}"

        openssl x509 -req -in "${REDIS_CREDS_DIR}/redis-${agent}.csr" \
            -CA "${AGENT_CA_DIR}/ca.crt" \
            -CAkey "${AGENT_CA_DIR}/ca.key" \
            -CAcreateserial \
            -out "${REDIS_CREDS_DIR}/redis-${agent}.crt" \
            -days 365 \
            -extfile "${REDIS_CREDS_DIR}/redis-${agent}.ext"
    
    echo " Generated redis-${agent} certificate"
    echo ""
done

echo "Cleaning up temporary files..."
rm -f "${REDIS_CREDS_DIR}"/*.csr "${REDIS_CREDS_DIR}"/*.ext

echo ""
echo "═══════════════════════════════════════════════════════════"
echo " Redis TLS certificates generated successfully!"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "All certificates signed by agent CA: ${AGENT_CA_DIR}/ca.{crt,key}"
echo ""
echo "Generated files in ${REDIS_CREDS_DIR}:"
echo "  - ca.crt"
echo "  - redis-control-plane.{crt,key}"
echo "  - redis-proxy.{crt,key}"
echo "  - redis-autonomous.{crt,key}"
echo "  - redis-managed.{crt,key}"
echo ""
