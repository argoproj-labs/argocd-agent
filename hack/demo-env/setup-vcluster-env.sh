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

set -e
set -o pipefail

# enable for debugging:
# set -x

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
VCLUSTERS="control-plane:argocd agent-managed:argocd agent-autonomous:argocd"
VCLUSTERS_AGENTS="agent-managed:argocd agent-autonomous:argocd"
action="$1"

# Kubectl context to restore
initial_context=$(kubectl config current-context)

cleanup() {
	kubectl config use-context ${initial_context}
}

on_error() {
	echo "ERROR: Error occurred, terminating." >&2
	cleanup
}

cluster() {
	IFS=":" s=($1); echo ${s[0]}
}

namespace() {
	IFS=":" s=($1); echo ${s[1]}
}

trap cleanup EXIT
trap on_error ERR

# check_for_openshift looks for cluster OpenShift API Resources, and if found, sets OPENSHIFT=true
check_for_openshift() {

	OPENSHIFT=
	if (kubectl api-resources || true) | grep -q "openshift.io"; then
		OPENSHIFT=true
	fi

}


# wait_for_pods looks all Pods running in k8s context $1, and keeps waiting until running count == $2. 
wait_for_pods() {

	set +e

    while [ true ]
    do

        echo "  -> Waiting for $1 pods to be running. Expecting $2 running pods."

        kubectl get pods --context="$1" -A
        RUNNING_PODS=`kubectl get pods --context="$1" -A | grep "Running" | wc -l | tr -d '[:space:]'`

        if [[ "$RUNNING_PODS" == "$2" ]]; then
            break
        fi

        sleep 5
    done

    echo "  -> Done waiting for $1 pods."

	set -e
}


apply() {

	TMP_DIR=`mktemp -d`

	echo "-> TMP_DIR is $TMP_DIR"
	cp -r ${SCRIPTPATH}/* $TMP_DIR

  # Comment out 'loadBalancerIP:' lines on OpenShift
	if [[ "$OPENSHIFT" != "" ]]; then
		sed -i.bak -e '/loadBalancerIP/s/^/#/' $TMP_DIR/control-plane/redis-service.yaml
		sed -i.bak -e '/loadBalancerIP/s/^/#/' $TMP_DIR/control-plane/repo-server-service.yaml
		sed -i.bak -e '/loadBalancerIP/s/^/#/' $TMP_DIR/control-plane/server-service.yaml
	fi

	echo "-> Create Argo CD on control plane"

	cluster=control-plane
	namespace=argocd
	echo "  --> Creating instance in vcluster $cluster"
	kubectl --context vcluster-$cluster create ns $namespace || true

	# Run 'kubectl apply' twice, to avoid the following error that occurs during the first invocation:
	# - 'error: resource mapping not found for name: "default" namespace: "" from "(...)": no matches for kind "AppProject" in version "argoproj.io/v1alpha1"'
	kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster} || true
	kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster}

	if [[ "$OPENSHIFT" != "" ]]; then

		echo "-> Waiting for Redis load balancer on control plane Argo CD"

		while [ true ]
		do
			REDIS_ADDR=`kubectl --context vcluster-control-plane -n argocd   get service/argocd-redis -o json | jq -r '.status.loadBalancer.ingress[0].hostname'`

			if [[ "$REDIS_ADDR" != "" ]] && [[ "$REDIS_ADDR" != "null" ]]; then
				break
			fi

			sleep 2
		done

		echo "-> Waiting for repo-server load balancer on control plane Argo CD"

		while [ true ]
		do
			REPO_SERVER_ADDR=`kubectl --context vcluster-control-plane -n argocd   get service/argocd-repo-server -o json | jq -r '.status.loadBalancer.ingress[0].hostname'`

			if [[ "$REPO_SERVER_ADDR" != "" ]] && [[ "$REPO_SERVER_ADDR" != "null" ]]; then
				break
			fi
			sleep 2

		done

	else
		# For all other cases, use hardcoded values
		REPO_SERVER_ADDR="192.168.56.222"
		REDIS_ADDR="192.168.56.221"
	fi

	echo "Redis on control plane: $REDIS_ADDR"
	echo "Repo server URL on control plane: $REPO_SERVER_ADDR"

	# Update the Argo CD repo-server/redis addresses that agent-managed Argo CD instance connects to
	sed -i.bak "s/repo-server-address/$REPO_SERVER_ADDR/g" "$TMP_DIR/agent-managed/argocd-cmd-params-cm.yaml"
	sed -i.bak "s/redis-server-address/$REDIS_ADDR/g" "$TMP_DIR/agent-managed/argocd-cmd-params-cm.yaml"

	echo "-> Creating Argo CD instances in vclusters"
	for c in $VCLUSTERS_AGENTS; do
		cluster=$(cluster $c)
		namespace=$(namespace $c)
		echo "  --> Creating instance in vcluster $cluster"
		kubectl --context vcluster-$cluster create ns $namespace || true

		# Run 'kubectl apply' twice, to avoid error that occurs during the first invocation (see above for error)
		kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster} || true
		kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster}
	done

	kubectl --context vcluster-control-plane create ns agent-autonomous || true
	kubectl --context vcluster-control-plane create ns agent-managed || true
	kubectl --context vcluster-agent-managed create ns agent-managed || true

	echo "-> Waiting for all the Argo CD/vCluster pods to be running on vclusters"
	wait_for_pods vcluster-control-plane 5
	wait_for_pods vcluster-agent-autonomous 4
	wait_for_pods vcluster-agent-managed 4

}

check_for_openshift


case "$action" in
create)


	kubectl create ns vcluster-agent-managed --context=${initial_context} || true
	kubectl create ns vcluster-control-plane --context=${initial_context} || true
	kubectl create ns vcluster-agent-autonomous --context=${initial_context} || true

	EXTRA_VCLUSTER_PARAMS=""

	if [[ "$OPENSHIFT" != "" ]]; then

		# Ensure that the namespaces we are using for our vclusters use our custom SCC (see SCC yaml for details)
		kubectl apply -f ${SCRIPTPATH}/resources/scc-anyuid-seccomp-netbind.yaml

		oc adm policy add-scc-to-group anyuid-seccomp-netbind system:serviceaccounts:vcluster-agent-managed --context=${initial_context}

		oc adm policy add-scc-to-group anyuid-seccomp-netbind system:serviceaccounts:vcluster-control-plane --context=${initial_context}

		oc adm policy add-scc-to-group anyuid-seccomp-netbind system:serviceaccounts:vcluster-agent-autonomous --context=${initial_context}

		EXTRA_VCLUSTER_PARAMS="-f ${SCRIPTPATH}/resources/vcluster.yaml"
	fi

	echo "-> Creating required vclusters"
	for c in $VCLUSTERS; do
		cluster=$(cluster $c)
 		echo "  --> Creating vcluster $cluster"
		vcluster create --context=${initial_context} ${EXTRA_VCLUSTER_PARAMS} --switch-context=false -n vcluster-${cluster} --expose --kube-config-context-name vcluster-${cluster} vcluster-${cluster}

		# I found a sleep statement here was beneficial to allow time for the load balancer to become available. If we find this is not required, these commented out lines should be removed.
		# if [[ "$OPENSHIFT" != "" ]]; then
		# 	sleep 60
		# fi
	done
	sleep 2
	apply
	;;
apply)
	apply
	;;
delete)
	echo "-> Deleting vclusters"
	for c in $VCLUSTERS; do
		cluster=$(cluster $c)
		echo "  --> Deleting vcluster $cluster"
		vcluster delete --context=${initial_context} vcluster-${cluster} || true
	done
	kubectl delete --context=${initial_context} ns vcluster-control-plane || true
	kubectl delete --context=${initial_context} ns vcluster-agent-managed || true
	kubectl delete --context=${initial_context} ns vcluster-agent-autonomous || true

	kubectl config delete-context vcluster-control-plane || true
	kubectl config delete-context vcluster-agent-managed || true
	kubectl config delete-context vcluster-agent-autonomous || true

	;;
*)
	echo "$0 (create|delete)" >&2
	exit 1
esac
