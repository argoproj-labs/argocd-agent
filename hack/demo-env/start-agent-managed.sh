#!/bin/sh
set -ex -o pipefail
if ! kubectl config get-contexts | tail -n +2 | awk '{ print $2 }' | grep -qE '^vcluster-agent-managed$'; then
    echo "kube context vcluster-agent-autonomous is not configured; missing setup?" >&2
    exit 1
fi
SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
test -f cmd/agent/main.go || (echo "Script should be run from argocd-agent's root path" >&2; exit 1)
go run ./cmd/agent/main.go --agent-mode managed --creds userpass:${SCRIPTPATH}/creds/creds.agent-managed --server-address 127.0.0.1 --server-port 8443 --insecure-tls --kubecontext vcluster-agent-managed --namespace agent-managed
