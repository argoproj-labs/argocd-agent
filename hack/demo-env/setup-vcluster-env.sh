#!/bin/sh
set -e
set -o pipefail
VCLUSTERS="control-plane agent-managed agent-autonomous"
action="$1"

case "$action" in
create)
	for cluster in $VCLUSTERS; do
		vcluster create --context=default -n vcluster-${cluster} --expose --kube-config-context-name vcluster-${cluster} vcluster-${cluster}
		kubectl --context vcluster-${cluster} create ns argocd
		kubectl --context vcluster-${cluster} apply -n argocd -k ${cluster}
	done
	kubectx default
	;;
delete)
	for cluster in $VCLUSTERS; do
		vcluster delete --context=default vcluster-${cluster}
	done
	kubectx default
	;;
*)
	echo "$0 (create|delete)" >&2
	exit 1
esac

