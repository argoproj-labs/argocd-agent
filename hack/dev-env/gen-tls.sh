#!/bin/bash
# Copyright 2025 The argocd-agent Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

##############################################################################
# Script to generate TLS certs for development/e2e-tests of argocd-agent.
#
# WARNING: Development script. Do not use to produce production credentials.
# This script comes without any promises. It should only be used to generate
# certificates for your dev or demo environments. 
##############################################################################

set -eo pipefail

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
cp_context=vcluster-control-plane
rp_tls_secret=resource-proxy-tls
rp_ca_secret=argocd-agent-ca

creds_path=${SCRIPTPATH}/creds
test -d ${creds_path} || mkdir ${creds_path}
days=${TLS_VALID_DAYS:-30}
kubectl=$(which kubectl 2>/dev/null) || true
if test "x${kubectl}" = "x"; then
    kubectl=$(which oc 2>/dev/null) || true
fi
if test "x${kubectl}" = "x"; then
    echo "No kubectl or oc found in \$PATH" >&2
    exit 1
fi

generate_rp_ca() {
	echo "Generating CA"
	openssl genrsa -out ${creds_path}/ca.key
	openssl req -x509 -new -nodes -key ${creds_path}/ca.key -sha256 -days ${days} -out ${creds_path}/ca.crt -subj '/CN=DO NOT USE FOR PRODUCTION/O=resource-proxy'
}

generate_rp_cert() {
    echo "Generating ResourceProxy TLS server certificate"
	openssl genrsa -out ${creds_path}/rp.key
	openssl req -new -sha256 \
	    -key ${creds_path}/rp.key \
	    -subj "/CN=DO NOT USE FOR PRODUCTION/O=resource-proxy" \
	    -reqexts SAN \
	    -config <(cat /etc/ssl/openssl.cnf \
		<(printf "\n[SAN]\nsubjectAltName=DNS:localhost,IP:127.0.0.1")) \
	    -out ${creds_path}/rp.csr
	openssl x509 -req -in ${creds_path}/rp.csr \
	    -extfile <(cat /etc/ssl/openssl.cnf <(printf "[SAN]\nsubjectAltName=DNS:localhost,IP:127.0.0.1")) \
	    -extensions SAN \
	    -CA ${creds_path}/ca.crt -CAkey ${creds_path}/ca.key -CAcreateserial -out ${creds_path}/rp.crt -days ${days} -sha256
}

generate_rp_ca
generate_rp_cert

echo "Generating secrets..."
export ARGOCD_AGENT_CONTEXT=vcluster-control-plane
#${SCRIPTPATH}/../../dist/argocd-agentctl ca generate
#kubectl --context=${cp_context} delete secret -n argocd ${rp_tls_secret} --ignore-not-found
#kubectl --context=${cp_context} delete secret -n argocd ${rp_ca_secret} --ignore-not-found
#kubectl --context=${cp_context} create secret -n argocd tls ${rp_tls_secret} --key=${creds_path}/rp.key --cert=${creds_path}/rp.crt
#kubectl --context=${cp_context} create secret -n argocd tls ${rp_ca_secret} --cert=${creds_path}/ca.crt --key=${creds_path}/ca.key
