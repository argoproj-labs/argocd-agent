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

export ARGOCD_AGENT_PRINCIPAL_CONTEXT=vcluster-control-plane
export ARGOCD_AGENT_PRINCIPAL_NAMESPACE=argocd

IPADDR=$(ip r show default | sed -e 's,.*\ src\ ,,' | sed -e 's,\ metric.*$,,')

if ! test -x ${AGENTCTL}; then
	echo "Please build argocd-agentctl first by running 'make cli'" >&2
	exit 1
fi

if ! ${AGENTCTL} pki inspect >/dev/null 2>&1; then
	${AGENTCTL} pki init
else
	echo "Reusing existing agent CA"
fi

${AGENTCTL} pki issue resource-proxy --upsert \
	--principal-namespace argocd \
	--ip "127.0.0.1,${IPADDR}"

AGENTS="agent-managed agent-autonomous"
for agent in ${AGENTS}; do
	if test "$RECREATE" = "--recreate"; then
		kubectl --context ${ARGOCD_AGENT_PRINCIPAL_CONTEXT} -n ${ARGOCD_AGENT_PRINCIPAL_NAMESPACE} delete --ignore-not-found secret cluster-${agent}
	fi
	if ! ${AGENTCTL} agent inspect ${agent} >/dev/null 2>&1; then
		${AGENTCTL} agent create ${agent} \
			--resource-proxy-username ${agent} \
			--resource-proxy-password ${agent} \
			--resource-proxy-server ${IPADDR}:9090
	else
		echo "Reusing existing agent configuration for ${agent}"
	fi
	${AGENTCTL} pki issue agent ${agent} --agent-context vcluster-${agent} --upsert
done
