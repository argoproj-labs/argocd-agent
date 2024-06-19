#!/bin/sh
set -e
set -o pipefail
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
VCLUSTERS="control-plane agent-managed agent-autonomous"
action="$1"

# Kubectl context to restore to
context=default

cleanup() {
	kubectl config set-context ${context}
}

trap cleanup EXIT ERR

case "$action" in
create)
	echo "-> Creating required vclusters"
	for cluster in $VCLUSTERS; do
		echo "  --> Creating vcluster $cluster"
		vcluster create --context=default -n vcluster-${cluster} --expose --kube-config-context-name vcluster-${cluster} vcluster-${cluster}
	done
	sleep 2
	echo "-> Creating Argo CD instances in vclusters"
	for cluster in $VCLUSTERS; do
		echo "  --> Creating instance in vcluster $cluster"
		kubectl --context vcluster-${cluster} create ns argocd
		kubectl --context vcluster-${cluster} apply -n argocd -k ${SCRIPTPATH}/${cluster}
	done
	;;
delete)
	echo "-> Deleting vclusters"
	for cluster in $VCLUSTERS; do
		echo "  --> Deleting vcluster $cluster"
		vcluster delete --context=default vcluster-${cluster} || true
	done
	;;
*)
	echo "$0 (create|delete)" >&2
	exit 1
esac

