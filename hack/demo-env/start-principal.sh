#!/bin/sh
set -ex -o pipefail
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
test -f cmd/principal/main.go || (echo "Script should be run from argocd-agent's root path" >&2; exit 1)
go run ./cmd/principal --allowed-namespaces '*' --insecure-tls-generate --insecure-jwt-generate --kubecontext vcluster-control-plane --log-level trace --passwd ${SCRIPTPATH}/creds/users.control-plane
