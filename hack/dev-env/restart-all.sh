#!/bin/bash

set -eo pipefail

kubectl --context vcluster-control-plane -n argocd rollout restart deployment argocd-server
kubectl --context vcluster-control-plane -n argocd rollout status --watch deployment argocd-server
kubectl --context vcluster-control-plane -n argocd rollout restart deployment argocd-repo-server
kubectl --context vcluster-control-plane -n argocd rollout status --watch deployment argocd-repo-server
kubectl --context vcluster-control-plane -n argocd rollout restart deployment argocd-agent-principal
kubectl --context vcluster-control-plane -n argocd rollout status --watch deployment argocd-agent-principal
kubectl --context vcluster-agent-managed -n argocd rollout restart deployment argocd-agent-agent
kubectl --context vcluster-agent-managed -n argocd rollout status --watch deployment argocd-agent-agent
kubectl --context vcluster-agent-autonomous -n argocd rollout restart deployment argocd-agent-agent
kubectl --context vcluster-agent-autonomous -n argocd rollout status --watch deployment argocd-agent-agent

