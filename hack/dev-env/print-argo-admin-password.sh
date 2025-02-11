#!/bin/sh

set -eo pipefail

kubectl --context vcluster-control-plane -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d && echo
