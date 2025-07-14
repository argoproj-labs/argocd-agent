#!/bin/sh

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

# This script deploys the principal and agents into the cluster, using the
# container images tagged "latest".
#
set -eo pipefail

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
BASEPATH="$( cd -- "$(dirname "$0")/../.." >/dev/null 2>&1 ; pwd -P )"

AGENTCTL=${BASEPATH}/dist/argocd-agentctl

# CLI must be built and executable
test -x ${AGENTCTL} || (echo "Please build the CLI" && exit 1)

source ${SCRIPTPATH}/utility.sh

ARGOCD_AGENT_PRINCIPAL_CONTEXT=vcluster-control-plane
ARGOCD_AGENT_MANAGED_CONTEXT=vcluster-agent-managed
ARGOCD_AGENT_AUTONOMOUS_CONTEXT=vcluster-agent-autonomous
TMPDIR=$(mktemp -d /tmp/argocd-agent.XXXXXXXX)

cleanup() {
	test "$TMPDIR" != "/" -a "$TMPDIR" != "" || exit 1
	echo "=> Removing temp path ${TMPDIR}"
	test -d ${TMPDIR} && rm -rf ${TMPDIR}	
}

trap cleanup EXIT

cp -a ${BASEPATH}/install/kubernetes/* ${TMPDIR}

deploy_principal() {
	(
		cd ${TMPDIR}/principal && kustomize edit set namespace argocd
		sed -i'' \
			-e "s/  principal.allowed-namespaces:.*/  principal.allowed-namespaces: \"agent-*\"/" \
			principal-params-cm.yaml
		kustomize build . | kubectl --context ${ARGOCD_AGENT_PRINCIPAL_CONTEXT} -n argocd apply -f -
		kubectl --context ${ARGOCD_AGENT_PRINCIPAL_CONTEXT} -n argocd rollout restart deployment argocd-agent-principal
	)
}

deploy_agent_managed() {
	(
		principal_addr=$(getExternalLoadBalancerIP ${ARGOCD_AGENT_PRINCIPAL_CONTEXT} argocd argocd-agent-principal)
		cd ${TMPDIR}/agent && kustomize edit set namespace argocd
		sed -i'' \
		        -e "s/  agent.mode:.*/  agent.mode: \"managed\"/" \
			-e "s/  agent.creds:.*/  agent.creds: \"mtls:any\"/" \
			-e "s/  agent.server.address:.*/  agent.server.address: \"$principal_addr\"/" \
			agent-params-cm.yaml
		kustomize build . | kubectl --context ${ARGOCD_AGENT_MANAGED_CONTEXT} -n argocd apply -f -
	)
}

deploy_agent_autonomous() {
	(
		principal_addr=$(getExternalLoadBalancerIP ${ARGOCD_AGENT_PRINCIPAL_CONTEXT} argocd argocd-agent-principal)
		cd ${TMPDIR}/agent && kustomize edit set namespace argocd
		sed -i'' \
		        -e "s/  agent.mode:.*/  agent.mode: \"autonomous\"/" \
			-e "s/  agent.creds:.*/  agent.creds: \"mtls:any\"/" \
			-e "s/  agent.server.address:.*/  agent.server.address: \"$principal_addr\"/" \
			agent-params-cm.yaml
		kustomize build . | kubectl --context ${ARGOCD_AGENT_AUTONOMOUS_CONTEXT} -n argocd apply -f -
	)
}

undeploy_principal() {
	(
		cd ${TMPDIR}/principal && kustomize edit set namespace argocd
		kustomize build . | kubectl --context ${ARGOCD_AGENT_PRINCIPAL_CONTEXT} -n argocd delete -f -
	)
}

undeploy_agent_managed() {
	(
		cd ${TMPDIR}/agent && kustomize edit set namespace argocd
		kustomize build . | kubectl --context ${ARGOCD_AGENT_MANAGED_CONTEXT} -n argocd delete -f -
	)
}

undeploy_agent_autonomous() {
	(
		cd ${TMPDIR}/agent && kustomize edit set namespace argocd
		kustomize build . | kubectl --context ${ARGOCD_AGENT_AUTONOMOUS_CONTEXT} -n argocd delete -f -
	)
}

case "$1" in
"deploy")
	deploy_principal
	deploy_agent_managed
	deploy_agent_autonomous
	;;
"undeploy")
	undeploy_principal || true
	undeploy_agent_managed || true
	undeploy_agent_autonomous || true
	;;
*)
	echo "USAGE: $0 (deploy|undeploy)" >&2
	exit 1
esac

