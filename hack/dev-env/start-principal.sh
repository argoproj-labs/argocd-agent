#!/bin/bash
# Copyright 2024 The argocd-agent Authors
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

set -ex -o pipefail
ARGS=$*
if ! kubectl config get-contexts | tail -n +2 | awk '{ print $2 }' | grep -qE '^vcluster-control-plane$'; then
    echo "kube context vcluster-control-plane is not configured; missing setup?" >&2
    exit 1
fi

if test "${ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS}" = ""; then
       ipaddr=$(kubectl --context vcluster-control-plane -n argocd get svc argocd-redis -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
       hostname=$(kubectl --context vcluster-control-plane -n argocd get svc argocd-redis -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
       if test "$ipaddr" != ""; then
               ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS=$ipaddr:6379
       elif test "$hostname" != ""; then
               ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS=$hostname:6379
       else
               echo "Could not determine Redis server address." >&2
               echo "Please set ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS manually" >&2
               exit 1
       fi
       export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS
fi

if test "${REDIS_PASSWORD}" = ""; then
    export REDIS_PASSWORD=$(kubectl get secret argocd-redis --context=vcluster-control-plane -n argocd -o jsonpath='{.data.auth}' | base64 --decode)
fi

# Point the principal to the e2e test configuration if it exists
E2E_ENV_FILE="/tmp/argocd-agent-e2e"
if [ -f "$E2E_ENV_FILE" ]; then
    source "$E2E_ENV_FILE"
    export ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET=${ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET:-false}
    export ARGOCD_PRINCIPAL_DESTINATION_BASED_MAPPING=${ARGOCD_PRINCIPAL_DESTINATION_BASED_MAPPING:-false}
fi

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
go run github.com/argoproj-labs/argocd-agent/cmd/argocd-agent principal \
	--allowed-namespaces '*' \
	--kubecontext vcluster-control-plane \
	--log-level ${ARGOCD_AGENT_LOG_LEVEL:-trace} \
	--namespace argocd \
	--auth "mtls:CN=([^,]+)" \
	$ARGS
