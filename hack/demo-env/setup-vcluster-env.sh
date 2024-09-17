#!/bin/sh
set -e
set -o pipefail
set -x
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
VCLUSTERS="control-plane:argocd agent-managed:argocd agent-autonomous:argocd"
VCLUSTERS_ARGOCD="agent-managed:argocd agent-autonomous:argocd"
action="$1"

# Kubectl context to restore to
initial_context=`kubectl config current-context`

cleanup() {
	kubectl config use-context ${initial_context}
}

cluster() {
	IFS=":" s=($1); echo ${s[0]}
}

namespace() {
	IFS=":" s=($1); echo ${s[1]}
}

trap cleanup EXIT ERR

apply() {

	echo "-> Create Argo CD on control plane"

	cluster=control-plane
	namespace=argocd
	echo "  --> Creating instance in vcluster $cluster"
	kubectl --context vcluster-$cluster create ns $namespace || true
	kubectl --context vcluster-$cluster apply -n $namespace -k ${SCRIPTPATH}/${cluster} || true
	kubectl --context vcluster-$cluster apply -n $namespace -k ${SCRIPTPATH}/${cluster}

	echo "-> Waiting for Redis load balancer on control plane Argo CD"

	while [ true ]
	do
		REDIS_ADDR=`kubectl --context vcluster-control-plane -n argocd   get service/argocd-redis -o json | jq -r '.status.loadBalancer.ingress[0].hostname'`

		if [[ "$REDIS_ADDR" != "" ]] && [[ "$REDIS_ADDR" != "null" ]]; then
			echo "Redis: $REDIS_ADDR"
			break
		fi

		sleep 2
	done

	echo "-> Waiting for repo-server load balancer on control plane Argo CD"

	while [ true ]
	do
		REPO_SERVER_ADDR=`kubectl --context vcluster-control-plane -n argocd   get service/argocd-repo-server -o json | jq -r '.status.loadBalancer.ingress[0].hostname'`

		if [[ "$REPO_SERVER_ADDR" != "" ]] && [[ "$REPO_SERVER_ADDR" != "null" ]]; then
			echo "Repo server URL: $REPO_SERVER_ADDR"
			break
		fi
		sleep 2

	done

	TMP_DIR=`mktemp -d`

	echo "-> TMP_DIR is $TMP_DIR"
	cp -r ${SCRIPTPATH}/* $TMP_DIR

	echo $REPO_SERVER_ADDR
	echo $REDIS_ADDR
	sed -i '' "s/repo-server-address/$REPO_SERVER_ADDR/g" "$TMP_DIR/agent-managed/argocd-cmd-params-cm.yaml"
	sed -i '' "s/redis-server-address/$REDIS_ADDR/g" "$TMP_DIR/agent-managed/argocd-cmd-params-cm.yaml"

	echo "-> Creating Argo CD instances in vclusters"
	for c in $VCLUSTERS_ARGOCD; do
		cluster=$(cluster $c)
		namespace=$(namespace $c)
		echo "  --> Creating instance in vcluster $cluster"
		kubectl --context vcluster-$cluster create ns $namespace || true
		kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster} || true
		kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster}
	done
	kubectl --context vcluster-control-plane create ns agent-autonomous || true
	kubectl --context vcluster-control-plane create ns agent-managed || true
	kubectl --context vcluster-agent-managed create ns agent-managed || true
}

case "$action" in
create)
	kubectl create ns vcluster-agent-managed --context=${initial_context} || true
	oc adm policy add-scc-to-user privileged -z default -n vcluster-agent-managed --context=${initial_context} || true
	oc adm policy add-scc-to-user privileged -z vc-vcluster-agent-managed -n vcluster-agent-managed  --context=${initial_context} || true
	oc adm policy add-scc-to-user privileged -z vc-workload-vcluster-agent-managed -n vcluster-agent-managed  --context=${initial_context} || true

	oc adm policy add-scc-to-user anyuid -z default -n vcluster-agent-managed --context=${initial_context} || true
	oc adm policy add-scc-to-user anyuid -z vc-vcluster-agent-managed -n vcluster-agent-managed  --context=${initial_context} || true
	oc adm policy add-scc-to-user anyuid -z vc-workload-vcluster-agent-managed -n vcluster-agent-managed  --context=${initial_context} || true

	kubectl create ns vcluster-control-plane --context=${initial_context} || true
	
	oc adm policy add-scc-to-user anyuid -z default -n vcluster-control-plane --context=${initial_context} || true
	oc adm policy add-scc-to-user anyuid -z vc-vcluster-control-plane -n vcluster-control-plane  --context=${initial_context} || true
	oc adm policy add-scc-to-user anyuid -z vc-workload-vcluster-control-plane -n vcluster-control-plane  --context=${initial_context} || true

	oc adm policy add-scc-to-user privileged -z default -n vcluster-control-plane --context=${initial_context} || true
	oc adm policy add-scc-to-user privileged -z vc-vcluster-control-plane -n vcluster-control-plane  --context=${initial_context} || true
	oc adm policy add-scc-to-user privileged -z vc-workload-vcluster-control-plane -n vcluster-control-plane  --context=${initial_context} || true


	kubectl create ns vcluster-agent-autonomous --context=${initial_context} || true

	oc adm policy add-scc-to-user anyuid -z default -n vcluster-agent-autonomous --context=${initial_context} || true
	oc adm policy add-scc-to-user anyuid -z vc-vcluster-agent-autonomous -n vcluster-agent-autonomous  --context=${initial_context} || true
	oc adm policy add-scc-to-user anyuid -z vc-workload-vcluster-agent-autonomous -n vcluster-agent-autonomous  --context=${initial_context} || true

	oc adm policy add-scc-to-user privileged -z default -n vcluster-agent-autonomous --context=${initial_context} || true
	oc adm policy add-scc-to-user privileged -z vc-vcluster-agent-autonomous -n vcluster-agent-autonomous  --context=${initial_context} || true
	oc adm policy add-scc-to-user privileged -z vc-workload-vcluster-agent-autonomous -n vcluster-agent-autonomous  --context=${initial_context} || true



	sleep 60

	echo "-> Creating required vclusters"
	for c in $VCLUSTERS; do
		cluster=$(cluster $c)
 		echo "  --> Creating vcluster $cluster"
		vcluster create --context=${initial_context} -f $SCRIPTPATH/resources/vcluster.yaml -n vcluster-${cluster} --expose --kube-config-context-name vcluster-${cluster} vcluster-${cluster}
		sleep 60
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
	
	kubectl config delete-context vcluster-control-plane || true
	kubectl config delete-context vcluster-agent-managed || true
	kubectl config delete-context vcluster-agent-autonomous || true

	;;
*)
	echo "$0 (create|delete)" >&2
	exit 1
esac
