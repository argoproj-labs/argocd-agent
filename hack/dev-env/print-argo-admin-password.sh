#!/bin/sh

set -eo pipefail

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
source ${SCRIPTPATH}/namespaces.sh

kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d && echo
