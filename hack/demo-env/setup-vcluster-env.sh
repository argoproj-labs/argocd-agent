#!/bin/sh
set -e
set -o pipefail
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
VCLUSTERS="control-plane:argocd agent-managed:argocd agent-autonomous:argocd"
action="$1"

# Kubectl context to restore to
context=default

cleanup() {
	kubectl config set-context ${context}
}

cluster() {
	IFS=":" s=($1); echo ${s[0]}
}

namespace() {
	IFS=":" s=($1); echo ${s[1]}
}

trap cleanup EXIT ERR

apply() {
	echo "-> Creating Argo CD instances in vclusters"
	for c in $VCLUSTERS; do
		cluster=$(cluster $c)
		namespace=$(namespace $c)
		echo "  --> Creating instance in vcluster $cluster"
		kubectl --context vcluster-$cluster create ns $namespace || true
		kubectl --context vcluster-$cluster apply -n $namespace -k ${SCRIPTPATH}/${cluster}
	done
	kubectl --context vcluster-control-plane create ns agent-autonomous || true
	kubectl --context vcluster-control-plane create ns agent-managed || true
	kubectl --context vcluster-agent-managed create ns agent-managed || true
}

case "$action" in
create)
	echo "-> Creating required vclusters"
	for c in $VCLUSTERS; do
		cluster=$(cluster $c)
		echo "  --> Creating vcluster $cluster"
		vcluster create --context=default -n vcluster-${cluster} --expose --kube-config-context-name vcluster-${cluster} vcluster-${cluster}
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
		vcluster delete --context=default vcluster-${cluster} || true
	done
	;;
*)
	echo "$0 (create|delete)" >&2
	exit 1
esac

