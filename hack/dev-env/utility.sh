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
  CONTEXT=$1
  NAMESPACE=$2
  SERVICE_NAME=$3

  MAX_ATTEMPTS=120

  for ((i=1; i<=MAX_ATTEMPTS; i++)); do
    external_ip=$(kubectl --context $CONTEXT get svc -n $NAMESPACE $SERVICE_NAME -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
    external_host=$(kubectl --context $CONTEXT get svc -n $NAMESPACE $SERVICE_NAME -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')

    if [ -n "$external_ip" ]; then
      echo $external_ip
      break
    elif [ -n "$external_host" ]; then
      echo $external_host
      break
    else
      sleep 5
    fi
  done

  if [ $i -gt $MAX_ATTEMPTS ]; then
    echo "Failed to obtain external IP after $MAX_ATTEMPTS attempts."
    exit 1
  fi

}

