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

set -ex -o pipefail
ARGS=$*
if ! kubectl config get-contexts | tail -n +2 | awk '{ print $2 }' | grep -qE '^vcluster-control-plane$'; then
    echo "kube context vcluster-control-plane is not configured; missing setup?" >&2
    exit 1
fi

# Export Principal's Redis server address as env variable
if test "${AGENT_E2E_PRINCIPAL_REDIS_SERVER_ADDRESS}" = ""; then
       ipaddr=$(kubectl --context vcluster-control-plane -n argocd get svc argocd-redis -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
       hostname=$(kubectl --context vcluster-control-plane -n argocd get svc argocd-redis -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
       if test "$ipaddr" != ""; then
               AGENT_E2E_PRINCIPAL_REDIS_SERVER_ADDRESS=$ipaddr:6379
       elif test "$hostname" != ""; then
               AGENT_E2E_PRINCIPAL_REDIS_SERVER_ADDRESS=$hostname:6379
       else
               echo "Could not determine Redis server address." >&2
               exit 1
       fi
       export AGENT_E2E_PRINCIPAL_REDIS_SERVER_ADDRESS
fi

# Export Redis server password as env variable
if test "${AGENT_E2E_PRINCIPAL_REDIS_PASSWORD}" = ""; then
       password=$(kubectl --context vcluster-control-plane -n argocd get secret argocd-redis -o jsonpath='{.data.auth}')
       if test "$password" != ""; then
               AGENT_E2E_PRINCIPAL_REDIS_PASSWORD="$(base64 -d <<< $password)"
       else
               echo "Could not determine Redis password." >&2
               exit 1
       fi
       export AGENT_E2E_PRINCIPAL_REDIS_PASSWORD
fi

# Export managed cluster server address as env variable
if test "${AGENT_E2E_MANAGED_CLUSTER_SERVER}" = ""; then
       managedClusterServer=$(kubectl --context vcluster-control-plane -n argocd get secret cluster-agent-managed -o jsonpath='{.data.server}')
       if test "$managedClusterServer" != ""; then
               AGENT_E2E_MANAGED_CLUSTER_SERVER="$(base64 -d <<< $managedClusterServer)"
       else
               echo "Could not determine managed cluster." >&2
               exit 1
       fi
       export AGENT_E2E_MANAGED_CLUSTER_SERVER
fi

# Export autonomous cluster server address as env variable
if test "${AGENT_E2E_AUTONOMOUS_CLUSTER_SERVER}" = ""; then
       autonomousClusterServer=$(kubectl --context vcluster-control-plane -n argocd get secret cluster-agent-autonomous -o jsonpath='{.data.server}')
       if test "$autonomousClusterServer" != ""; then
               AGENT_E2E_AUTONOMOUS_CLUSTER_SERVER="$(base64 -d <<< $autonomousClusterServer)"
       else
               echo "Could not determine autonomous cluster." >&2
               exit 1
       fi
       export AGENT_E2E_AUTONOMOUS_CLUSTER_SERVER
fi

# Export Managed Agent's Redis server address as env variable
if test "${AGENT_E2E_AGENT_MANAGED_REDIS_SERVER_ADDRESS}" = ""; then
       ipaddr=$(kubectl --context vcluster-agent-managed -n argocd get svc argocd-redis -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
       hostname=$(kubectl --context vcluster-agent-managed -n argocd get svc argocd-redis -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
       if test "$ipaddr" != ""; then
               AGENT_E2E_AGENT_MANAGED_REDIS_SERVER_ADDRESS=$ipaddr:6379
       elif test "$hostname" != ""; then
               AGENT_E2E_AGENT_MANAGED_REDIS_SERVER_ADDRESS=$hostname:6379
       else
               echo "Could not determine Redis server address." >&2
               exit 1
       fi
       export AGENT_E2E_AGENT_MANAGED_REDIS_SERVER_ADDRESS
fi

# Export Managed Agent's Redis server password as env variable
if test "${AGENT_E2E_AGENT_MANAGED_REDIS_PASSWORD}" = ""; then
       password=$(kubectl --context vcluster-agent-managed -n argocd get secret argocd-redis -o jsonpath='{.data.auth}')
       if test "$password" != ""; then
               AGENT_E2E_AGENT_MANAGED_REDIS_PASSWORD="$(base64 -d <<< $password)"
       else
               echo "Could not determine Redis password." >&2
               exit 1
       fi
       export AGENT_E2E_AGENT_MANAGED_REDIS_PASSWORD
fi

go test -count=1 -v -race -timeout 30m github.com/argoproj-labs/argocd-agent/test/e2e
