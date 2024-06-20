for cluster in control-plane agent-managed agent-autonomous; do
	kubectl --context vcluster-${cluster} delete apps --all-namespaces --all
done
