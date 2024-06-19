#!/bin/sh
set -ex -o pipefail
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
test -f cmd/agent/main.go || (echo "Script should be run from argocd-agent's root path" >&2; exit 1)
go run ./cmd/agent/main.go --agent-mode autonomous --creds userpass:${SCRIPTPATH}/creds/creds.agent-autonomous --server-address 127.0.0.1 --server-port 8443 --insecure-tls --kubecontext vcluster-agent-autonomous --namespace argocd
