#!/bin/bash
# Simple HA test setup using kind clusters
# This creates a minimal environment to test principal HA failover

set -e

SCRIPT_DIR="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
PROJECT_ROOT="$SCRIPT_DIR/../.."

# Check dependencies
for cmd in kind kubectl go; do
    if ! command -v $cmd &> /dev/null; then
        echo "ERROR: $cmd is required but not installed."
        exit 1
    fi
done

echo "=== Building argocd-agent ==="
cd "$PROJECT_ROOT"
go build -o "$SCRIPT_DIR/argocd-agent" ./cmd/argocd-agent

echo "=== Creating kind cluster: ha-test ==="
kind create cluster --name ha-test 2>/dev/null || echo "Cluster already exists"

# Wait for cluster to be ready
kubectl --context kind-ha-test wait --for=condition=Ready nodes --all --timeout=60s

echo "=== Creating namespace and installing Argo CD CRDs ==="
kubectl --context kind-ha-test create namespace argocd 2>/dev/null || true

# Install Argo CD CRDs
kubectl --context kind-ha-test apply --server-side -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/crds/application-crd.yaml
kubectl --context kind-ha-test apply --server-side -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/crds/appproject-crd.yaml
kubectl --context kind-ha-test apply --server-side -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/crds/applicationset-crd.yaml

# Create a dummy Redis (just for the principal to start - won't actually use it for HA test)
kubectl --context kind-ha-test apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: redis
  namespace: argocd
  labels:
    app: redis
spec:
  containers:
  - name: redis
    image: redis:7-alpine
    ports:
    - containerPort: 6379
---
apiVersion: v1
kind: Service
metadata:
  name: argocd-redis
  namespace: argocd
spec:
  selector:
    app: redis
  ports:
  - port: 6379
    targetPort: 6379
---
apiVersion: v1
kind: Secret
metadata:
  name: argocd-redis
  namespace: argocd
type: Opaque
data:
  auth: ""
EOF

echo "=== Waiting for Redis to be ready ==="
kubectl --context kind-ha-test -n argocd wait --for=condition=Ready pod/redis --timeout=60s

echo "=== Creating self-signed CA for testing ==="
# Generate a temporary self-signed CA for mTLS authentication
TMPDIR=$(mktemp -d)
openssl genrsa -out "$TMPDIR/ca.key" 2048 2>/dev/null
openssl req -x509 -new -nodes -key "$TMPDIR/ca.key" -sha256 -days 1 \
    -out "$TMPDIR/ca.crt" -subj "/CN=test-ca" 2>/dev/null

# Create the CA secret (recreate if it already exists)
kubectl --context kind-ha-test -n argocd create secret tls argocd-agent-ca \
    --cert="$TMPDIR/ca.crt" \
    --key="$TMPDIR/ca.key" \
    2>/dev/null || {
    kubectl --context kind-ha-test -n argocd delete secret argocd-agent-ca
    kubectl --context kind-ha-test -n argocd create secret tls argocd-agent-ca \
        --cert="$TMPDIR/ca.crt" \
        --key="$TMPDIR/ca.key"
}

echo "=== Generating server certificates signed by the CA ==="
CERTS_DIR="$SCRIPT_DIR/certs"
mkdir -p "$CERTS_DIR"
cp "$TMPDIR/ca.crt" "$CERTS_DIR/ca.crt"

for name in primary replica; do
    openssl genrsa -out "$CERTS_DIR/$name.key" 2048 2>/dev/null
    openssl req -new -key "$CERTS_DIR/$name.key" \
        -out "$TMPDIR/$name.csr" -subj "/CN=$name" 2>/dev/null
    openssl x509 -req -in "$TMPDIR/$name.csr" \
        -CA "$TMPDIR/ca.crt" -CAkey "$TMPDIR/ca.key" -CAcreateserial \
        -out "$CERTS_DIR/$name.crt" -days 1 -sha256 \
        -extfile <(printf "subjectAltName=DNS:localhost,IP:127.0.0.1") 2>/dev/null
done

rm -rf "$TMPDIR"

echo ""
echo "=== Setup Complete ==="
echo ""
echo "To test HA, run in separate terminals:"
echo ""
echo "  Terminal 1 (Primary):"
echo "    $SCRIPT_DIR/run-primary.sh"
echo ""
echo "  Terminal 2 (Replica):"
echo "    $SCRIPT_DIR/run-replica.sh"
echo ""
echo "To cleanup:"
echo "    kind delete cluster --name ha-test"
