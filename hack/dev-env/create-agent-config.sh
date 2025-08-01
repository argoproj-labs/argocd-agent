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

set -eo pipefail

RECREATE="$1"

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
BASEPATH="$( cd -- "$(dirname "$0")/../.." >/dev/null 2>&1 ; pwd -P )"
AGENTCTL=${BASEPATH}/dist/argocd-agentctl
KUBECTL=$(which kubectl)
OPENSSL=$(which openssl)

source ${SCRIPTPATH}/utility.sh

export ARGOCD_AGENT_PRINCIPAL_CONTEXT=vcluster-control-plane
export ARGOCD_AGENT_PRINCIPAL_NAMESPACE=argocd

if test "${ARGOCD_AGENT_IN_CLUSTER}" = ""; then
	IPADDR=$(ip r show default | sed -e 's,.*\ src\ ,,' | sed -e 's,\ metric.*$,,')
	ARGOCD_AGENT_GRPC_SVC=$IPADDR
	ARGOCD_AGENT_GRPC_SAN="--ip 127.0.0.1,${IPADDR}"
	ARGOCD_AGENT_RESOURCE_PROXY=${IPADDR}
	ARGOCD_AGENT_RESOURCE_PROXY_SAN="--ip 127.0.0.1,${IPADDR}"
else
	ARGOCD_AGENT_GRPC_SVC=$(getExternalLoadBalancerIP ${ARGOCD_AGENT_PRINCIPAL_CONTEXT} argocd argocd-agent-principal)
	ARGOCD_AGENT_GRPC_SAN="--ip 127.0.0.1,$ARGOCD_AGENT_GRPC_SVC"
	ARGOCD_AGENT_RESOURCE_PROXY=argocd-agent-resource-proxy
	ARGOCD_AGENT_RESOURCE_PROXY_SAN="--dns ${ARGOCD_AGENT_RESOURCE_PROXY}"
fi

if ! test -x ${AGENTCTL}; then
	echo "Please build argocd-agentctl first by running 'make cli'" >&2
	exit 1
fi

echo "[*] Initializing PKI"
if ! ${AGENTCTL} pki inspect >/dev/null 2>&1; then
	${AGENTCTL} pki init
	echo "  -> PKI initialized."
else
	echo "  -> Reusing existing agent PKI."
fi

echo "[*] Creating principal TLS configuration"
${AGENTCTL} pki issue principal --upsert \
	--principal-namespace argocd \
	${ARGOCD_AGENT_GRPC_SAN}
echo "  -> Principal TLS config created."

echo "[*] Creating resource proxy TLS configuration"
${AGENTCTL} pki issue resource-proxy --upsert \
	--principal-namespace argocd \
	${ARGOCD_AGENT_RESOURCE_PROXY_SAN}
echo "  -> Resource proxy TLS config created."

echo "[*] Creating JWT signing key and secret"
${AGENTCTL} jwt create-key --principal-context ${ARGOCD_AGENT_PRINCIPAL_CONTEXT} --upsert

AGENTS="agent-managed agent-autonomous"
for agent in ${AGENTS}; do
	echo "[*] Creating configuration for agent ${agent}"
	if test "$RECREATE" = "--recreate"; then
		echo "  -> Deleting existing cluster secret, if it exists"
		kubectl --context ${ARGOCD_AGENT_PRINCIPAL_CONTEXT} -n ${ARGOCD_AGENT_PRINCIPAL_NAMESPACE} delete --ignore-not-found secret cluster-${agent}
	fi
	if ! ${AGENTCTL} agent inspect ${agent} >/dev/null 2>&1; then
		echo "  -> Creating cluster secret for agent configuration"
		${AGENTCTL} agent create ${agent} \
			--resource-proxy-username ${agent} \
			--resource-proxy-password ${agent} \
			--resource-proxy-server ${ARGOCD_AGENT_RESOURCE_PROXY}:9090
	else
		echo "  -> Reusing existing cluster secret for agent configuration"
	fi
	echo "  -> Creating mTLS client certificate and key"
	${AGENTCTL} pki propagate --agent-context vcluster-${agent} --agent-namespace argocd -f
	${AGENTCTL} pki issue agent ${agent} --agent-context vcluster-${agent} --agent-namespace argocd --upsert
done
