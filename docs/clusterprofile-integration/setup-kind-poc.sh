#!/usr/bin/env bash
# Reproduces the full declarative ClusterProfile + argocd-agent PoC on two
# local Kind clusters (podman provider). See ../README.md for the
# architecture and a step-by-step narrative of what this script does and why.
#
# Usage: ./setup-kind-poc.sh 2>&1 | tee /tmp/setup-kind-poc.log
#
# Idempotent-ish: safe to re-run most steps; cluster creation is skipped if
# the clusters already exist.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CPI_REPO="${CPI_REPO:-$(cd "$REPO_ROOT/../clusterprofile-integration-for-argocd" && pwd)}"

PRINCIPAL_CLUSTER_NAME="argocd-hub"
AGENT_CLUSTER_NAME="argocd-agent1"
AGENT_APP_NAME="agent-a"
NAMESPACE_NAME="argocd"
AGENT_MODE="managed"

echo "== [1/14] Kind clusters =="
if ! kind get clusters 2>/dev/null | grep -qx "$PRINCIPAL_CLUSTER_NAME"; then
  cat <<EOF | kind create cluster --name "$PRINCIPAL_CLUSTER_NAME" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: $PRINCIPAL_CLUSTER_NAME
networking:
  podSubnet: "10.245.0.0/16"
  serviceSubnet: "10.97.0.0/12"
nodes:
  - role: control-plane
EOF
fi
if ! kind get clusters 2>/dev/null | grep -qx "$AGENT_CLUSTER_NAME"; then
  cat <<EOF | kind create cluster --name "$AGENT_CLUSTER_NAME" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: $AGENT_CLUSTER_NAME
networking:
  podSubnet: "10.246.0.0/16"
  serviceSubnet: "10.98.0.0/12"
nodes:
  - role: control-plane
EOF
fi

echo "== [2/14] Work around rootless-podman NodePort DNAT (switch to iptables-nft, restart kube-proxy) =="
for c in "${PRINCIPAL_CLUSTER_NAME}-control-plane" "${AGENT_CLUSTER_NAME}-control-plane"; do
  podman exec "$c" update-alternatives --set iptables /usr/sbin/iptables-nft >/dev/null
  podman exec "$c" update-alternatives --set ip6tables /usr/sbin/ip6tables-nft >/dev/null
done
kubectl delete pod -n kube-system -l k8s-app=kube-proxy --context "kind-$PRINCIPAL_CLUSTER_NAME" >/dev/null
kubectl delete pod -n kube-system -l k8s-app=kube-proxy --context "kind-$AGENT_CLUSTER_NAME" >/dev/null
sleep 8

echo "== [3/14] Load local images into both clusters =="
for img in localhost/argocd-agent:dev localhost/argocd-agent-clusterprofile-driver:dev; do
  rm -f /tmp/kindload.tar
  podman save "$img" -o /tmp/kindload.tar
  kind load image-archive /tmp/kindload.tar --name "$PRINCIPAL_CLUSTER_NAME"
  kind load image-archive /tmp/kindload.tar --name "$AGENT_CLUSTER_NAME"
done
rm -f /tmp/kindload.tar
podman save localhost/clusterprofile-integration-for-argocd:dev -o /tmp/kindload.tar
kind load image-archive /tmp/kindload.tar --name "$PRINCIPAL_CLUSTER_NAME"
rm -f /tmp/kindload.tar

echo "== [4/14] Argo CD control plane + argocd-agent principal on the hub =="
kubectl create namespace "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" --dry-run=client -o yaml | kubectl apply --context "kind-$PRINCIPAL_CLUSTER_NAME" -f -
kubectl apply -n "$NAMESPACE_NAME" --server-side \
  -k "$REPO_ROOT/install/kubernetes/argo-cd/principal" \
  --context "kind-$PRINCIPAL_CLUSTER_NAME"
kubectl patch configmap argocd-cmd-params-cm -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  --patch '{"data":{"application.namespaces":"*","redis.server":"argocd-agent-redis-proxy:6379"}}'
kubectl apply -n "$NAMESPACE_NAME" -k "$REPO_ROOT/hack/dev-kind-poc/principal" --context "kind-$PRINCIPAL_CLUSTER_NAME"

echo "== [5/14] Principal PKI + listen-address + NetworkPolicy fixes =="
argocd-agentctl pki init --principal-context "kind-$PRINCIPAL_CLUSTER_NAME" --principal-namespace "$NAMESPACE_NAME" --force
kubectl patch configmap argocd-agent-params -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  --patch "{\"data\":{\"principal.allowed-namespaces\":\"$AGENT_APP_NAME\",\"principal.listen.host\":\"\"}}"
kubectl patch svc argocd-agent-principal -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  --patch '{"spec":{"type":"NodePort"}}'

PRINCIPAL_EXTERNAL_IP=$(podman inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${PRINCIPAL_CLUSTER_NAME}-control-plane")
PRINCIPAL_NODE_PORT=$(kubectl get svc argocd-agent-principal -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" -o jsonpath='{.spec.ports[0].nodePort}')
PRINCIPAL_DNS_NAME=$(kubectl get svc argocd-agent-principal -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" -o jsonpath='{.metadata.name}.{.metadata.namespace}.svc.cluster.local')
RESOURCE_PROXY_INTERNAL_IP=$(kubectl get svc argocd-agent-resource-proxy -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" -o jsonpath='{.spec.clusterIP}')
RESOURCE_PROXY_DNS_NAME=$(kubectl get svc argocd-agent-resource-proxy -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" -o jsonpath='{.metadata.name}.{.metadata.namespace}.svc.cluster.local')

argocd-agentctl pki issue principal --principal-context "kind-$PRINCIPAL_CLUSTER_NAME" --principal-namespace "$NAMESPACE_NAME" \
  --ip "127.0.0.1,$PRINCIPAL_EXTERNAL_IP" --dns "localhost,$PRINCIPAL_DNS_NAME" --upsert
argocd-agentctl pki issue resource-proxy --principal-context "kind-$PRINCIPAL_CLUSTER_NAME" --principal-namespace "$NAMESPACE_NAME" \
  --ip "127.0.0.1,$RESOURCE_PROXY_INTERNAL_IP" --dns "localhost,$RESOURCE_PROXY_DNS_NAME" --upsert
argocd-agentctl jwt create-key --principal-context "kind-$PRINCIPAL_CLUSTER_NAME" --principal-namespace "$NAMESPACE_NAME" --upsert

# Shared client certificate for argocd-agent-creds to present to the resource
# proxy's TLS listener, which is hardcoded (in argocd-agent's own, unmodified
# code) to tls.RequireAndVerifyClientCert - a bearer token alone cannot
# complete that handshake. --same-context issues it straight into this same
# hub cluster/namespace instead of a real remote agent. The tool then also
# tries to (re-)upsert the CA secret it just read from into the same
# namespace, which fails ("field is immutable") since that secret already
# exists with a different type than the tool's upsert path expects; that is
# harmless and only affects the CA write, not the leaf cert we actually need,
# which is created first and succeeds - hence `|| true`.
argocd-agentctl pki issue agent argocd-server-shared-client \
  --principal-context "kind-$PRINCIPAL_CLUSTER_NAME" --principal-namespace "$NAMESPACE_NAME" \
  --agent-context "kind-$PRINCIPAL_CLUSTER_NAME" --agent-namespace "$NAMESPACE_NAME" \
  --same-context --upsert || true
kubectl get secret argocd-agent-client-tls -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" >/dev/null

# kindnet (recent kind versions) enforces NetworkPolicy. The shipped
# argocd-agent-redis-proxy NetworkPolicy only allows port 8443 from agent
# pods *inside the same cluster*, which blocks legitimate cross-cluster
# agent connections arriving via NodePort from a genuinely different Kind
# cluster; and it doesn't cover the resource-proxy port (9090) or the Redis
# proxy port (6379) from argocd-server at all. Widen it for this PoC.
kubectl patch networkpolicy argocd-agent-redis-proxy -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  --type='json' -p='[{"op":"add","path":"/spec/ingress/-","value":{"from":[{"ipBlock":{"cidr":"0.0.0.0/0"}}],"ports":[{"port":8443,"protocol":"TCP"}]}},{"op":"add","path":"/spec/ingress/-","value":{"from":[{"podSelector":{"matchLabels":{"app.kubernetes.io/part-of":"argocd"}}}],"ports":[{"port":9090,"protocol":"TCP"}]}}]' || true

kubectl rollout restart deployment argocd-agent-principal -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME"
kubectl rollout status deployment argocd-agent-principal -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" --timeout=90s

echo "== [6/14] Workload cluster: Argo CD (agent components) + argocd-agent agent =="
kubectl create namespace "$NAMESPACE_NAME" --context "kind-$AGENT_CLUSTER_NAME" --dry-run=client -o yaml | kubectl apply --context "kind-$AGENT_CLUSTER_NAME" -f -
kubectl apply -n "$NAMESPACE_NAME" --server-side \
  -k "$REPO_ROOT/install/kubernetes/argo-cd/agent-$AGENT_MODE" \
  --context "kind-$AGENT_CLUSTER_NAME"

echo "== [7/14] Agent mTLS bootstrap (orthogonal to ClusterProfile: this is gRPC transport trust, not the Argo CD cluster secret) =="
argocd-agentctl pki issue agent "$AGENT_APP_NAME" --principal-context "kind-$PRINCIPAL_CLUSTER_NAME" \
  --agent-context "kind-$AGENT_CLUSTER_NAME" --agent-namespace "$NAMESPACE_NAME" --upsert

kubectl create namespace "$AGENT_APP_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" --dry-run=client -o yaml | kubectl apply --context "kind-$PRINCIPAL_CLUSTER_NAME" -f -
kubectl apply -n "$NAMESPACE_NAME" -k "$REPO_ROOT/hack/dev-kind-poc/agent" --context "kind-$AGENT_CLUSTER_NAME"
kubectl patch configmap argocd-agent-params -n "$NAMESPACE_NAME" --context "kind-$AGENT_CLUSTER_NAME" \
  --patch "{\"data\":{\"agent.server.address\":\"$PRINCIPAL_EXTERNAL_IP\",\"agent.server.port\":\"$PRINCIPAL_NODE_PORT\",\"agent.mode\":\"$AGENT_MODE\",\"agent.creds\":\"mtls:any\"}}"
kubectl rollout restart deployment argocd-agent-agent -n "$NAMESPACE_NAME" --context "kind-$AGENT_CLUSTER_NAME"
kubectl rollout status deployment argocd-agent-agent -n "$NAMESPACE_NAME" --context "kind-$AGENT_CLUSTER_NAME" --timeout=90s

# Upstream argocd-agent's shipped agent-clusterrole.yaml only grants RBAC on
# namespaces + applications.argoproj.io - not enough for the principal's
# resource-proxy (Argo CD UI live manifest/logs/exec) to actually read
# Pods/Deployments/etc. on this spoke. See the comment in
# manifests/agent-resource-proxy-view-binding.yaml for the full explanation;
# this is purely additive RBAC and does not modify any shipped manifest.
kubectl apply --context "kind-$AGENT_CLUSTER_NAME" \
  -f "$REPO_ROOT/docs/clusterprofile-integration/manifests/agent-resource-proxy-view-binding.yaml"

echo "== [8/14] ClusterProfile CRD =="
kubectl apply --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  -f "$REPO_ROOT/../ocm/manifests/cluster-manager/hub/crds/0000_00_multicluster.x-k8s.io_clusterprofiles.crd.yaml"

echo "== [9/14] clusterprofile-agent-driver =="
kubectl apply -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  -f "$REPO_ROOT/docs/clusterprofile-integration/manifests/clusterprofile-agent-driver.yaml"
kubectl rollout status deployment clusterprofile-agent-driver -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" --timeout=90s

echo "== [10/14] clusterprofile-integration-for-argocd (Helm, main branch, unmodified) =="
kubectl apply -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  -f "$REPO_ROOT/docs/clusterprofile-integration/manifests/clusterprofile-provider-file-cm.yaml"
helm upgrade --install argocd-clusterprofile-controller \
  "$CPI_REPO/charts/argocd-clusterprofile-controller" \
  -n "$NAMESPACE_NAME" --kube-context "kind-$PRINCIPAL_CLUSTER_NAME" \
  -f "$REPO_ROOT/docs/clusterprofile-integration/manifests/clusterprofile-integration-values.yaml"
kubectl rollout status deployment argocd-clusterprofile-controller-clusterprofile-controller -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" --timeout=90s

echo "== [11/14] Inject argocd-agent-creds exec plugin into argocd-server =="
kubectl patch deployment argocd-server -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" --type strategic \
  --patch-file "$REPO_ROOT/docs/clusterprofile-integration/manifests/argocd-server-creds-patch.yaml"
kubectl rollout status deployment argocd-server -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" --timeout=90s

echo "== [12/14] Propagate the default AppProject from principal to agent (managed-mode namespaced-Application prerequisite, unrelated to ClusterProfile) =="
kubectl patch appproject default -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" --type='merge' \
  --patch='{"spec":{"sourceNamespaces":["*"],"destinations":[{"name":"*","namespace":"*","server":"*"}]}}'

echo "== [13/14] Restart principal+agent to clear any stale gRPC stream from the multi-minute setup, then declaratively create the ClusterProfile for agent-a =="
# During iterative setup we repeatedly observed the principal's outbound
# event-writer stream to a long-lived agent connection go stale ("transport
# is closing" repeated forever) after many kubectl/helm operations against
# the argocd namespace. A clean restart forces both sides to re-handshake.
# This is a robustness workaround for this PoC environment, unrelated to the
# declarative ClusterProfile design itself.
kubectl rollout restart deployment argocd-agent-principal -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME"
kubectl rollout status deployment argocd-agent-principal -n "$NAMESPACE_NAME" --context "kind-$PRINCIPAL_CLUSTER_NAME" --timeout=90s
kubectl rollout restart deployment argocd-agent-agent -n "$NAMESPACE_NAME" --context "kind-$AGENT_CLUSTER_NAME"
kubectl rollout status deployment argocd-agent-agent -n "$NAMESPACE_NAME" --context "kind-$AGENT_CLUSTER_NAME" --timeout=90s
sleep 5

kubectl apply --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  -f "$REPO_ROOT/docs/clusterprofile-integration/manifests/sample-clusterprofile.yaml"

echo "== [14/14] Deploy a test Application against the declaratively-registered agent-a destination =="
kubectl apply --context "kind-$PRINCIPAL_CLUSTER_NAME" \
  -f "$REPO_ROOT/docs/clusterprofile-integration/manifests/sample-app.yaml"

echo "Done. See ../README.md 'Verifying the PoC' for how to confirm everything is wired up."
