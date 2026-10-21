#!/bin/bash

SCRIPTPATH="$(
    cd -- "$(dirname "$0")" >/dev/null 2>&1 || exit
    pwd -P
)"

source ${SCRIPTPATH}/../namespaces.sh

DOCKER_BIN=${DOCKER_BIN:-"docker"}

# OpenShift Routes are only available on the *host* cluster - the vcluster-* contexts each proxy
# a limited, virtual API surface that does not include 'route.openshift.io' (this is why routes
# for vcluster-hosted Services must be created against the host cluster, not the vcluster
# itself). By convention (see hack/dev-env/setup-vcluster-env.sh), this dev-env's vclusters are
# created from - and this script is expected to be run from - the host cluster's kubectl
# context, with each vcluster's synced/host-side objects living in a namespace of the same name
# as its context (e.g. context 'vcluster-control-plane' <-> host namespace
# 'vcluster-control-plane'). Override HOST_K8S_CONTEXT if that assumption doesn't hold for you.
HOST_K8S_CONTEXT=${HOST_K8S_CONTEXT:-"--context=$(kubectl config current-context)"}

BASEPATH="$( cd -- "${SCRIPTPATH}/../../.." >/dev/null 2>&1 || exit; pwd -P )"
AGENTCTL=${AGENTCTL:-"${BASEPATH}/dist/argocd-agentctl"}

TEMP_DIR=$(mktemp -d)

echo "Using temporary directory: $TEMP_DIR"

cp -R $SCRIPTPATH/. $TEMP_DIR

# Store an auth token in the client/server configuration
configureClientAndServerToml() {

  AUTH_TOKEN=$(openssl rand -hex 32)

  sed -i.bak "s#AUTHENTICATION_TOKEN#$AUTH_TOKEN#g" $TEMP_DIR/client/frpc.toml
  sed -i.bak "s#AUTHENTICATION_TOKEN#$AUTH_TOKEN#g" $TEMP_DIR/server/frps.toml

}

# getExternalRouteHost will set EXTERNAL_ROUTE_HOST with the hostname assigned to the named
# OpenShift Route, waiting (with retries) until the Route has one.
getExternalRouteHost() {
  ROUTE_NAME=$1
  ROUTE_CONTEXT=$2
  ROUTE_NAMESPACE=$3

  MAX_ATTEMPTS=120

  for ((i=1; i<=MAX_ATTEMPTS; i++)); do

    echo ""
    EXTERNAL_ROUTE_HOST=$(kubectl get route $ROUTE_NAME $ROUTE_CONTEXT -n ${ROUTE_NAMESPACE} -o jsonpath='{.spec.host}')

    if [ -n "$EXTERNAL_ROUTE_HOST" ]; then
      echo "Route host for $ROUTE_NAME is $EXTERNAL_ROUTE_HOST"
      break
    else
      echo "Route host for $ROUTE_NAME not yet available, attempting again in 5 seconds..."
      sleep 5
    fi
  done

  if [ $i -gt $MAX_ATTEMPTS ]; then
    echo "Failed to obtain route host after $MAX_ATTEMPTS attempts."
    exit 1
  fi

}

# getHostSyncedServiceName sets HOST_SERVICE_NAME to the name that vcluster's syncer assigned,
# on the host cluster, to the Service with the given name/namespace inside the vcluster (vcluster
# mangles object names - e.g. 'foo' -> 'foo-x-namespace-x-vcluster' - when syncing them out to
# the host cluster, so the translated name can't just be guessed/constructed). This is what an
# OpenShift Route on the host cluster must target, since it can't reference a Service living
# inside the vcluster directly. Lookup is done by the stable 'vcluster.loft.sh/object-name' /
# 'vcluster.loft.sh/object-namespace' annotations vcluster stamps onto every synced object,
# rather than by name, so it isn't tied to a specific vcluster version's translation scheme.
getHostSyncedServiceName() {
  local VIRTUAL_NAMESPACE=$1
  local VIRTUAL_SERVICE_NAME=$2
  local HOST_NAMESPACE=$3

  MAX_ATTEMPTS=60

  for ((i=1; i<=MAX_ATTEMPTS; i++)); do

    HOST_SERVICE_NAME=$(kubectl $HOST_K8S_CONTEXT -n $HOST_NAMESPACE get svc -o json | \
      jq -r --arg ns "$VIRTUAL_NAMESPACE" --arg name "$VIRTUAL_SERVICE_NAME" \
      '.items[] | select(.metadata.annotations["vcluster.loft.sh/object-name"]==$name and .metadata.annotations["vcluster.loft.sh/object-namespace"]==$ns) | .metadata.name' | head -n1)

    if [ -n "$HOST_SERVICE_NAME" ]; then
      echo "Host-synced Service for $VIRTUAL_NAMESPACE/$VIRTUAL_SERVICE_NAME is $HOST_NAMESPACE/$HOST_SERVICE_NAME"
      break
    else
      echo "Host-synced Service for $VIRTUAL_NAMESPACE/$VIRTUAL_SERVICE_NAME not yet available, attempting again in 5 seconds..."
      sleep 5
    fi
  done

  if [ -z "$HOST_SERVICE_NAME" ]; then
    echo "Failed to find the host-synced Service for $VIRTUAL_NAMESPACE/$VIRTUAL_SERVICE_NAME after $MAX_ATTEMPTS attempts."
    exit 1
  fi

}

# generateSelfSignedCert creates a self-signed cert/key pair with the given hostname in its SAN
# (subjectAltName). TLS hostname verification is based on SAN, not CN, so CN is left as a short
# fixed placeholder: OpenShift Route hostnames are often 90+ characters (e.g. on HyperShift
# clusters), and OpenSSL's CN field is capped at 64 characters ('string too long' / ub-common-name),
# which is too short for those hostnames.
# This is throw-away, single-run dev tooling material: it is regenerated every time setup.sh
# runs, so a self-signed cert (rather than a full CA/PKI) is sufficient.
generateSelfSignedCert() {
  local CERT_HOSTNAME=$1
  local CERT_OUT=$2
  local KEY_OUT=$3

  openssl req -x509 -newkey rsa:2048 -sha256 -days 365 -nodes \
    -keyout "$KEY_OUT" -out "$CERT_OUT" \
    -subj "/CN=reverse-tunnel-dev-cert" \
    -addext "subjectAltName=DNS:${CERT_HOSTNAME}"
}

# exposeRedisViaRoute switches the argocd-redis Service in the given vcluster/namespace from
# LoadBalancer to ClusterIP, adds a TLS-only port (6380) backed by a self-signed cert, and
# exposes that port externally via an OpenShift Route (passthrough) created on the host cluster
# (targeting the vcluster's host-synced Service, since Route isn't available inside the
# vcluster). This allows the e2e test fixture to reach it without provisioning a cloud
# LoadBalancer. Redis's existing plain port (6379) is untouched, so in-cluster consumers of
# redis are unaffected.
exposeRedisViaRoute() {
  local VCLUSTER_NAME=$1
  local CONTEXT="--context=vcluster-$VCLUSTER_NAME"
  local NAMESPACE=$2
  local HOST_NAMESPACE="vcluster-$VCLUSTER_NAME"

  # Switch the Service back to ClusterIP (in case it was previously patched to LoadBalancer)
  # and add the TLS-only port, if it isn't already present.
  kubectl $CONTEXT -n $NAMESPACE patch service argocd-redis -p '{"spec":{"type":"ClusterIP"}}'
  kubectl $CONTEXT -n $NAMESPACE patch service argocd-redis --type=json \
    -p='[{"op":"add","path":"/spec/ports/-","value":{"name":"tls-redis","port":6380,"targetPort":6380,"protocol":"TCP"}}]' \
    2>/dev/null || true

  getHostSyncedServiceName "$NAMESPACE" "argocd-redis" "$HOST_NAMESPACE"
  local REDIS_HOST_SERVICE_NAME=$HOST_SERVICE_NAME

  cat <<EOF | kubectl $HOST_K8S_CONTEXT -n $HOST_NAMESPACE apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: argocd-redis
  annotations:
    # Passthrough routes fall back to the router's default tunnel (idle) timeout, typically 1h.
    # haproxy.router.openshift.io/timeout-tunnel (not the ambiguous 'timeout' annotation) is
    # what controls this for passthrough routes.
    haproxy.router.openshift.io/timeout-tunnel: 24h
spec:
  to:
    kind: Service
    name: ${REDIS_HOST_SERVICE_NAME}
  port:
    targetPort: tls-redis
  tls:
    termination: passthrough
EOF

  getExternalRouteHost "argocd-redis" "$HOST_K8S_CONTEXT" "$HOST_NAMESPACE"
  local ROUTE_HOST=$EXTERNAL_ROUTE_HOST

  # Generate a self-signed cert for redis's TLS port. The Go e2e test fixture validates this
  # cert against the CA stored in the 'argocd-redis-tls' secret. The local argocd-agent dev
  # processes instead skip validation (ARGOCD_AGENT_REDIS_TLS_INSECURE=true, see start-e2e.sh)
  # but still send the correct hostname as SNI, which is what allows the Route to route the
  # connection to the right backend.
  local REDIS_CERT_DIR=$(mktemp -d)
  generateSelfSignedCert "$ROUTE_HOST" "$REDIS_CERT_DIR/tls.crt" "$REDIS_CERT_DIR/tls.key"

  kubectl create secret tls argocd-redis-server-tls $CONTEXT -n $NAMESPACE \
    --cert="$REDIS_CERT_DIR/tls.crt" --key="$REDIS_CERT_DIR/tls.key" \
    --dry-run=client -o yaml | kubectl $CONTEXT -n $NAMESPACE apply -f -

  kubectl create secret generic argocd-redis-tls $CONTEXT -n $NAMESPACE \
    --from-file=ca.crt="$REDIS_CERT_DIR/tls.crt" \
    --dry-run=client -o yaml | kubectl $CONTEXT -n $NAMESPACE apply -f -

  rm -rf "$REDIS_CERT_DIR"

  # Add the TLS listener to the redis Deployment. The volumes/volumeMounts patches are 'add' on
  # the whole array, which is idempotent (replaces on re-run), but the args are appended via the
  # '/-' (end-of-array) index, which is NOT idempotent - so only apply it once, guarded by
  # whether --tls-port is already present, to avoid piling up duplicate args on every re-run of
  # this script.
  EXISTING_ARGS=$(kubectl $CONTEXT -n $NAMESPACE get deployment argocd-redis -o jsonpath='{.spec.template.spec.containers[0].args}')
  if [[ "$EXISTING_ARGS" != *"--tls-port"* ]]; then
    kubectl $CONTEXT -n $NAMESPACE patch deployment argocd-redis --type=json -p='[
      {"op": "add", "path": "/spec/template/spec/volumes", "value": [{"name": "redis-tls", "secret": {"secretName": "argocd-redis-server-tls"}}]},
      {"op": "add", "path": "/spec/template/spec/containers/0/volumeMounts", "value": [{"name": "redis-tls", "mountPath": "/tls", "readOnly": true}]},
      {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--tls-port"},
      {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "6380"},
      {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--tls-cert-file"},
      {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "/tls/tls.crt"},
      {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--tls-key-file"},
      {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "/tls/tls.key"},
      {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--tls-auth-clients"},
      {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "no"}
    ]'
  else
    # Args are already in place from a previous run; the volume/volumeMount secret reference is
    # stable (secretName doesn't change), so there's nothing to re-patch, but the cert data
    # itself was just regenerated above - re-apply the volumes so a changed secret is noticed
    # via a rollout.
    kubectl $CONTEXT -n $NAMESPACE patch deployment argocd-redis --type=json -p='[
      {"op": "add", "path": "/spec/template/spec/volumes", "value": [{"name": "redis-tls", "secret": {"secretName": "argocd-redis-server-tls"}}]},
      {"op": "add", "path": "/spec/template/spec/containers/0/volumeMounts", "value": [{"name": "redis-tls", "mountPath": "/tls", "readOnly": true}]}
    ]'
  fi

  kubectl $CONTEXT -n $NAMESPACE delete pod -l app.kubernetes.io/name=argocd-redis
}

# configureRedisProxyServerTLS generates a self-signed TLS cert/key for the principal's own
# Redis-proxy listener - the one Argo CD's argocd-server/argocd-repo-server (running on
# vcluster-control-plane) reach through the frp tunnel at 'frp-container-internal:6379' - and
# stores it as a Kubernetes Secret named 'argocd-redis-proxy-tls', the default name the
# principal's --redis-proxy-server-tls-secret-name flag looks for. This lets start-e2e.sh turn on
# ARGOCD_PRINCIPAL_REDIS_TLS_ENABLED to TLS-protect this listener too, now that the principal's
# own upstream connection to vcluster-control-plane's argocd-redis also goes over TLS (via the
# Route from exposeRedisViaRoute above) instead of a plaintext 'kubectl port-forward'. Both
# argocd-server and argocd-repo-server are patched with --redis-use-tls and
# --redis-insecure-skip-tls-verify (insecure, since this is throwaway dev/e2e cert material, same
# as elsewhere in this script) so they actually speak TLS to the tunnel; neither flag is exposed
# via the 'argocd-cmd-params-cm' ConfigMap, so they have to be added as container args directly.
configureRedisProxyServerTLS() {
  local CONTEXT="--context=vcluster-control-plane"
  local NAMESPACE="${ARGOCD_PRINCIPAL_NAMESPACE}"

  local REDIS_PROXY_CERT_DIR=$(mktemp -d)
  generateSelfSignedCert "frp-container-internal" "$REDIS_PROXY_CERT_DIR/tls.crt" "$REDIS_PROXY_CERT_DIR/tls.key"

  kubectl create secret tls argocd-redis-proxy-tls $CONTEXT -n $NAMESPACE \
    --cert="$REDIS_PROXY_CERT_DIR/tls.crt" --key="$REDIS_PROXY_CERT_DIR/tls.key" \
    --dry-run=client -o yaml | kubectl $CONTEXT -n $NAMESPACE apply -f -

  rm -rf "$REDIS_PROXY_CERT_DIR"

  for DEPLOYMENT in argocd-server argocd-repo-server; do
    EXISTING_ARGS=$(kubectl $CONTEXT -n $NAMESPACE get deployment $DEPLOYMENT -o jsonpath='{.spec.template.spec.containers[0].args}')
    if [[ "$EXISTING_ARGS" != *"--redis-use-tls"* ]]; then
      kubectl $CONTEXT -n $NAMESPACE patch deployment $DEPLOYMENT --type=json -p='[
        {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--redis-use-tls"},
        {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--redis-insecure-skip-tls-verify"}
      ]'
    fi
  done
}

# exposeArgoServerViaRoute switches the control plane's argocd-server Service from LoadBalancer
# to ClusterIP, and exposes it externally via an OpenShift Route (passthrough) instead.
# argocd-server already serves HTTPS natively, so (unlike redis/repo-server) no TLS-wrapping is
# needed - this is the same situation as the vcluster API server itself (see
# hack/dev-env/setup-vcluster-env.sh). Nothing in-cluster depends on this Service's address, so
# no further config patching/restarting is required: it's only consumed externally, by the e2e
# test fixture (see fixture.GetArgoCDServerEndpoint).
exposeArgoServerViaRoute() {
  local CONTEXT="--context=vcluster-control-plane"
  local NAMESPACE="${ARGOCD_PRINCIPAL_NAMESPACE}"
  local HOST_NAMESPACE="vcluster-control-plane"

  kubectl $CONTEXT -n $NAMESPACE patch service argocd-server -p '{"spec":{"type":"ClusterIP"}}'

  getHostSyncedServiceName "$NAMESPACE" "argocd-server" "$HOST_NAMESPACE"
  local ARGOCD_SERVER_HOST_SERVICE_NAME=$HOST_SERVICE_NAME

  cat <<EOF | kubectl $HOST_K8S_CONTEXT -n $HOST_NAMESPACE apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: argocd-server
  annotations:
    haproxy.router.openshift.io/timeout-tunnel: 24h
spec:
  to:
    kind: Service
    name: ${ARGOCD_SERVER_HOST_SERVICE_NAME}
  port:
    targetPort: https
  tls:
    termination: passthrough
EOF

  getExternalRouteHost "argocd-server" "$HOST_K8S_CONTEXT" "$HOST_NAMESPACE"
}

# patchClusterSecretForTunnel repoints the given Argo CD cluster Secret's '.data.server' field at
# the frp tunnel instead of the local machine's real IP (which setup-vcluster-env.sh/
# create-agent-config.sh point it at by default, via the resource-proxy address) - that real IP
# is typically not routable from inside a remote cluster, which is the whole reason for this
# tunnel. It also relaxes '.data.config.tlsClientConfig' (drops the pinned CA, enables insecure
# mode), since the tunnel presents a different (self-signed, frps-generated) certificate than the
# one the cluster Secret was originally pinned to.
# Arguments: cluster Secret name, agent name (used in the '?agentName=' query param).
patchClusterSecretForTunnel() {
  local SECRET_NAME=$1
  local AGENT_NAME=$2

  local NEW_VALUE_BASE64=$(echo -n "https://frp-container-internal:9090?agentName=${AGENT_NAME}" | base64 -w 0)

  echo "Patching ${SECRET_NAME} secret on vcluster-control-plane"

  # Replace the .data.server field of the Secret
  kubectl $K8S_CONTEXT_CONTROL_PLANE -n ${ARGOCD_PRINCIPAL_NAMESPACE} patch secret ${SECRET_NAME} --type='json' -p='[{"op": "replace", "path": "/data/server", "value":"'"$NEW_VALUE_BASE64"'"}]'

  # Extract .data.config
  local CONFIG_FIELD_VALUE=`kubectl $K8S_CONTEXT_CONTROL_PLANE -n ${ARGOCD_PRINCIPAL_NAMESPACE} get secret ${SECRET_NAME} -o jsonpath='{.data.config}' | base64 --decode`

  # remove .data.config.tlsClientConfig.caData and set '.data.config.tlsClientConfig.insecure = true'
  local TLS_CLIENT_CONFIG=`echo "$CONFIG_FIELD_VALUE" | jq -r '.tlsClientConfig' | jq 'del(.caData)' | jq '.insecure = true'`
  CONFIG_FIELD_VALUE=`echo $CONFIG_FIELD_VALUE | jq ".tlsClientConfig = $TLS_CLIENT_CONFIG"`

  local CONFIG_FIELD_VALUE_BASE64=$(echo -n "$CONFIG_FIELD_VALUE" | base64 -w 0)

  # Patch the secret with the new .data.config value
  kubectl $K8S_CONTEXT_CONTROL_PLANE -n ${ARGOCD_PRINCIPAL_NAMESPACE} patch secret ${SECRET_NAME} --type='json' -p='[{"op": "replace", "path": "/data/config", "value":"'"$CONFIG_FIELD_VALUE_BASE64"'"}]'
}

# addTunnelSANToResourceProxyCert reissues the resource-proxy's TLS certificate (originally
# created by create-agent-config.sh, with SAN covering only 127.0.0.1 and this machine's real IP)
# to also include 'frp-container-internal' as a valid DNS SAN. Argo CD (running on
# vcluster-control-plane) reaches the resource proxy through the tunnel via that hostname (see
# patchClusterSecretForTunnel), and without it in the cert's SAN, TLS hostname verification fails
# with "certificate is not valid for any names, but wanted to match frp-container-internal" -
# regardless of whether the cluster Secret's tlsClientConfig uses insecure mode or full mTLS,
# since that only controls CA trust, not hostname matching.
addTunnelSANToResourceProxyCert() {
  if ! test -x "${AGENTCTL}"; then
    echo "argocd-agentctl not found at ${AGENTCTL}; please run 'make cli' first" >&2
    exit 1
  fi

  local IPADDR=$(ip r show default | sed -e 's,.*\ src\ ,,' | sed -e 's,\ metric.*$,,')

  ARGOCD_AGENT_PRINCIPAL_CONTEXT=vcluster-control-plane \
  ARGOCD_AGENT_PRINCIPAL_NAMESPACE=${ARGOCD_PRINCIPAL_NAMESPACE} \
  "${AGENTCTL}" pki issue resource-proxy --upsert \
    --principal-namespace "${ARGOCD_PRINCIPAL_NAMESPACE}" \
    --ip "127.0.0.1,${IPADDR}" \
    --dns frp-container-internal
}

# -----------------------------------------------------------------------------

configureClientAndServerToml

echo ""
echo "Reissuing resource-proxy TLS certificate with 'frp-container-internal' SAN"
addTunnelSANToResourceProxyCert

# Create frp Deployment and Services in Argo CD principal namespace

echo ""
echo "Installing frp on K8s"

K8S_CONTEXT_CONTROL_PLANE="--context=vcluster-control-plane"
HOST_NAMESPACE_CONTROL_PLANE="vcluster-control-plane"

# Apply the (virtual) Service first, so vcluster can sync it out to a real Service on the host
# cluster - that host-side Service is what the OpenShift Route (created on the host cluster,
# since Route isn't available inside the vcluster) will target.
kubectl apply $K8S_CONTEXT_CONTROL_PLANE -n ${ARGOCD_PRINCIPAL_NAMESPACE} -f $TEMP_DIR/server/service.yaml

getHostSyncedServiceName "${ARGOCD_PRINCIPAL_NAMESPACE}" "frp-container-external" "$HOST_NAMESPACE_CONTROL_PLANE"
FRP_HOST_SERVICE_NAME=$HOST_SERVICE_NAME

cat <<EOF | kubectl $HOST_K8S_CONTEXT -n $HOST_NAMESPACE_CONTROL_PLANE apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: frp-container-external
  annotations:
    # Passthrough routes fall back to the router's default tunnel (idle) timeout, typically 1h,
    # which is too short for a long-running dev tunnel. haproxy.router.openshift.io/timeout-tunnel
    # (not the ambiguous 'timeout' annotation) is what controls this for passthrough routes.
    haproxy.router.openshift.io/timeout-tunnel: 24h
spec:
  to:
    kind: Service
    name: ${FRP_HOST_SERVICE_NAME}
  port:
    targetPort: "2333"
  tls:
    termination: passthrough
EOF

getExternalRouteHost "frp-container-external" "$HOST_K8S_CONTEXT" "$HOST_NAMESPACE_CONTROL_PLANE"

sed -i.bak "s#EXTERNAL-HOSTNAME#$EXTERNAL_ROUTE_HOST#g" $TEMP_DIR/client/frpc.toml

# Generate a self-signed TLS cert for frps, using the Route's hostname (needed so that the
# client's SNI/hostname validation - and thus the Route's SNI-based routing - work).
generateSelfSignedCert "$EXTERNAL_ROUTE_HOST" "$TEMP_DIR/server/server.crt" "$TEMP_DIR/server/server.key"

cp $TEMP_DIR/server/server.crt $TEMP_DIR/client/frp-ca.crt

# Now apply the full kustomization: Deployment, plus the Secret containing frps.toml and the
# now-generated server.crt/server.key.
kubectl apply $K8S_CONTEXT_CONTROL_PLANE -n ${ARGOCD_PRINCIPAL_NAMESPACE} -k $TEMP_DIR/server

echo ""
patchClusterSecretForTunnel "cluster-agent-managed" "agent-managed"

echo ""
patchClusterSecretForTunnel "cluster-agent-autonomous" "agent-autonomous"

echo ""
echo "Patching ConfigMap 'argocd-cmd-params-cm' to redirect redis to tunnel"
kubectl $K8S_CONTEXT_CONTROL_PLANE -n ${ARGOCD_PRINCIPAL_NAMESPACE} patch configmap argocd-cmd-params-cm --type json --patch '[{"op": "add", "path": "/data/redis.server", "value": "frp-container-internal:6379"}]'

echo ""
echo "Exposing 'argocd-redis' via OpenShift Route (TLS) on vcluster-agent-managed"
exposeRedisViaRoute "agent-managed" "${ARGOCD_MANAGED_NAMESPACE}"

echo ""
echo "Exposing 'argocd-redis' via OpenShift Route (TLS) on vcluster-agent-autonomous"
exposeRedisViaRoute "agent-autonomous" "${ARGOCD_AUTONOMOUS_NAMESPACE}"

echo ""
echo "Exposing 'argocd-redis' via OpenShift Route (TLS) on vcluster-control-plane"
exposeRedisViaRoute "control-plane" "${ARGOCD_PRINCIPAL_NAMESPACE}"

# echo ""
# echo "Exposing 'argocd-repo-server' via OpenShift Route (TLS) on vcluster-control-plane"
# exposeRepoServerViaRoute

echo ""
echo "Exposing 'argocd-server' via OpenShift Route on vcluster-control-plane"
exposeArgoServerViaRoute

echo ""
echo "Configuring TLS on the principal's Redis-proxy listener, and enabling it on argocd-server/argocd-repo-server"
configureRedisProxyServerTLS

echo ""
echo "Restarting all pods in argocd NS"
kubectl $K8S_CONTEXT_CONTROL_PLANE -n ${ARGOCD_PRINCIPAL_NAMESPACE} delete pods --all

echo ""
echo "* Starting the frp local client (frpc)."
echo "* - This may initially report an error while waiting for the Route to become reachable, but it will keep retrying until the connection is established."
echo "* - If frpc connects but the 'two' (redis) proxy keeps failing to relay data, this means you need to run 'make start-e2e', or that it is not currently running."
echo ""
echo "CTRL-C / Command-C to terminate frp tunnel."
$TEMP_DIR/client/run.sh
