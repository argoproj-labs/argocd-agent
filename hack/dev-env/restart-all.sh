#!/bin/bash

set -eo pipefail

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
source ${SCRIPTPATH}/namespaces.sh

kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} rollout restart deployment argocd-server
kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} rollout status --watch deployment argocd-server
kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} rollout restart deployment argocd-repo-server
kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} rollout status --watch deployment argocd-repo-server
kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} rollout restart deployment argocd-agent-principal
kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} rollout status --watch deployment argocd-agent-principal
kubectl --context vcluster-agent-managed -n ${ARGOCD_MANAGED_NAMESPACE} rollout restart deployment argocd-agent-agent
kubectl --context vcluster-agent-managed -n ${ARGOCD_MANAGED_NAMESPACE} rollout status --watch deployment argocd-agent-agent
kubectl --context vcluster-agent-autonomous -n ${ARGOCD_AUTONOMOUS_NAMESPACE} rollout restart deployment argocd-agent-agent
kubectl --context vcluster-agent-autonomous -n ${ARGOCD_AUTONOMOUS_NAMESPACE} rollout status --watch deployment argocd-agent-agent

