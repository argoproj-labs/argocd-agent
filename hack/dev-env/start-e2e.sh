#!/bin/bash
set -o pipefail

# Copyright 2025 The argocd-agent Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
source ${SCRIPTPATH}/namespaces.sh

# Override HOST_K8S_CONTEXT if your current context isn't the host.
HOST_K8S_CONTEXT=${HOST_K8S_CONTEXT:-"--context=$(kubectl config current-context)"}

# Optional: when 'hack/dev-env/reverse-tunnels/openshift-routes/setup.sh' has been run, Argo CD's resource-proxy requests reach
# the locally-running principal via the frp tunnel not via this machine's real IP.
# We detect that setup here by checking for frp Service.
if kubectl --context=vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} get svc frp-container-internal >/dev/null 2>&1; then
  echo "Detected reverse-tunnel setup; pointing resource-proxy at frp-container-internal"
  export ARGOCD_AGENT_RESOURCE_PROXY="frp-container-internal"
fi

# getExternalLoadBalancerIP will set EXTERNAL_IP/EXTERNAL_PORT with the external address of the
# specified Service, using its LoadBalancer ingress if present. 
# 
# However, if an (optional) ROUTE_HOST_NAMESPACE is given, it instead falls back to an 
# OpenShift Route of the same name in that namespace on the host cluster.
getExternalLoadBalancerIP() {
  SERVICE_NAME=$1
  ROUTE_HOST_NAMESPACE=$2

  MAX_ATTEMPTS=120
  EXTERNAL_PORT=6379
  EXTERNAL_TLS=false

  for ((i=1; i<=MAX_ATTEMPTS; i++)); do

    echo ""
    EXTERNAL_IP=$(kubectl get svc $SERVICE_NAME $K8S_CONTEXT $K8S_NAMESPACE -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
    EXTERNAL_HOST=$(kubectl get svc $SERVICE_NAME $K8S_CONTEXT $K8S_NAMESPACE -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')

    if [ -n "$EXTERNAL_IP" ]; then
      echo "External IP for $SERVICE_NAME on $K8S_CONTEXT is $EXTERNAL_IP"
      break
    elif [ -n "$EXTERNAL_HOST" ]; then
      echo "External host for $SERVICE_NAME on $K8S_CONTEXT is $EXTERNAL_HOST"
      EXTERNAL_IP=$EXTERNAL_HOST
      break
    fi

    if [ -n "$ROUTE_HOST_NAMESPACE" ]; then
      ROUTE_HOST=$(kubectl get route $SERVICE_NAME "$HOST_K8S_CONTEXT" -n $ROUTE_HOST_NAMESPACE -o jsonpath='{.spec.host}' 2>/dev/null)
      if [ -n "$ROUTE_HOST" ]; then
        echo "Route host for $SERVICE_NAME on $HOST_K8S_CONTEXT/$ROUTE_HOST_NAMESPACE is $ROUTE_HOST"
        EXTERNAL_IP=$ROUTE_HOST
        EXTERNAL_PORT=443
        EXTERNAL_TLS=true
        break
      fi
    fi

    echo "External address for $SERVICE_NAME on $K8S_CONTEXT not yet available, attempting again in 5 seconds..."
    sleep 5
  done

  if [ $i -gt $MAX_ATTEMPTS ]; then
    echo "Failed to obtain external IP after $MAX_ATTEMPTS attempts."
    exit 1
  fi

}

# Get hostname of control-plane redis
K8S_CONTEXT="--context=vcluster-control-plane"
K8S_NAMESPACE="-n ${ARGOCD_PRINCIPAL_NAMESPACE}"
getExternalLoadBalancerIP "argocd-redis" "vcluster-control-plane"
export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS="$EXTERNAL_IP:$EXTERNAL_PORT"

# When hack/dev-env/reverse-tunnels/openshift-routes/setup.sh has been run,
# argocd-redis in vcluster-control-plane is exposed via an OpenShift Route (TLS) rather than a
# LoadBalancer. In that case, the local principal process needs TLS enabled. 
# INSECURE is used since the server certs involved are self-signed dev/e2e material
if [ "$EXTERNAL_TLS" = "true" ]; then
  export ARGOCD_PRINCIPAL_REDIS_TLS_ENABLED=true
  export ARGOCD_PRINCIPAL_REDIS_TLS_INSECURE=true
fi

# Get hostname of agent-managed redis
K8S_CONTEXT="--context=vcluster-agent-managed"
K8S_NAMESPACE="-n ${ARGOCD_MANAGED_NAMESPACE}"
getExternalLoadBalancerIP "argocd-redis" "vcluster-agent-managed"
export MANAGED_AGENT_REDIS_ADDR="$EXTERNAL_IP:$EXTERNAL_PORT"

# As above: if redis is exposed via a Route, use TLS.
if [ "$EXTERNAL_TLS" = "true" ]; then
  export ARGOCD_AGENT_REDIS_TLS_ENABLED=true
  export ARGOCD_AGENT_REDIS_TLS_INSECURE=true
fi

# Get hostname of agent-autonomous redis
K8S_CONTEXT="--context=vcluster-agent-autonomous"
K8S_NAMESPACE="-n ${ARGOCD_AUTONOMOUS_NAMESPACE}"
getExternalLoadBalancerIP "argocd-redis" "vcluster-agent-autonomous"
export AUTONOMOUS_AGENT_REDIS_ADDR="$EXTERNAL_IP:$EXTERNAL_PORT"


export REDIS_PASSWORD=$(kubectl get secret argocd-redis --context=vcluster-agent-managed -n ${ARGOCD_MANAGED_NAMESPACE} -o jsonpath='{.data.auth}' | base64 --decode)

# Route output to a temporary file, to allow us to process the text in 'run-e2e.sh'
rm -f /tmp/argocd-agent-e2e-process-output.log

goreman -exit-on-stop=false -f hack/dev-env/Procfile.e2e start 2>&1 | tee /tmp/argocd-agent-e2e-process-output.log

