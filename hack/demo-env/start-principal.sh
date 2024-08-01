#!/bin/sh
set -ex -o pipefail
ARGS=$*
if ! kubectl config get-contexts | tail -n +2 | awk '{ print $2 }' | grep -qE '^vcluster-control-plane$'; then
    echo "kube context vcluster-agent-autonomous is not configured; missing setup?" >&2
    exit 1
fi
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
test -f cmd/principal/main.go || (echo "Script should be run from argocd-agent's root path" >&2; exit 1)
go run ./cmd/principal --allowed-namespaces '*' --insecure-tls-generate --insecure-jwt-generate --kubecontext vcluster-control-plane --log-level trace --passwd ${SCRIPTPATH}/creds/users.control-plane $ARGS
