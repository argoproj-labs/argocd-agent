#!/bin/bash

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


# getExternalLoadBalancerIP will set EXTERNAL_IP with the load balancer hostname from the specified Service
getExternalLoadBalancerIP() {
  SERVICE_NAME=$1

  MAX_ATTEMPTS=120

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
    else
      echo "External IP for $SERVICE_NAME on $K8S_CONTEXT not yet available, attempting again in 5 seconds..."
      sleep 5
    fi
  done

  if [ $i -gt $MAX_ATTEMPTS ]; then
    echo "Failed to obtain external IP after $MAX_ATTEMPTS attempts."
    exit 1
  fi

}

# Get hostname of control-plane redis
K8S_CONTEXT="--context=vcluster-control-plane"
K8S_NAMESPACE="-n argocd"
getExternalLoadBalancerIP "argocd-redis"
export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS="$EXTERNAL_IP:6379"


# Get hostname of agent-managed redis
K8S_CONTEXT="--context=vcluster-agent-managed"
K8S_NAMESPACE="-n argocd"
getExternalLoadBalancerIP "argocd-redis"
export MANAGED_AGENT_REDIS_ADDR="$EXTERNAL_IP:6379"

# Get hostname of agent-autonomous redis
K8S_CONTEXT="--context=vcluster-agent-autonomous"
K8S_NAMESPACE="-n argocd"
getExternalLoadBalancerIP "argocd-redis"
export AUTONOMOUS_AGENT_REDIS_ADDR="$EXTERNAL_IP:6379"


export REDIS_PASSWORD=$(kubectl get secret argocd-redis --context=vcluster-agent-managed $K8S_NAMESPACE -o jsonpath='{.data.auth}' | base64 --decode)

goreman -exit-on-stop=false -f hack/dev-env/Procfile.e2e start

