#!/bin/bash
##############################################################################
# Script to generate credentials for development/e2e-tests of argocd-agent.
#
# WARNING: Development script. Do not use to produce production credentials.
# This script comes without any promises. It should only be used to generate
# credentials for your dev or demo environments. The passwords produced are
# weak.
##############################################################################

set -x -o pipefail

# Use pwmake if it exists, otherwise use pwgen
which pwmake
if [[ $? == 0 ]]; then
	set -e
	pwmake=$(which pwmake)

else
	set -e
	pwmake=$(which pwgen)
fi

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
htpasswd=$(which htpasswd)
creds_path=${SCRIPTPATH}/creds
test -d ${creds_path} || mkdir ${creds_path}

if test -f "${creds_path}/users.control-plane"; then
	echo "Truncating existing creds"
	rm -f "${creds_path}/users.control-plane"
fi
touch "${creds_path}/users.control-plane"

for ag in agent-managed agent-autonomous; do
	password=$(pwmake 56)
	htpasswd -b -B "${creds_path}/users.control-plane" "${ag}" "${password}"
	echo "${ag}:${password}" > "${creds_path}/creds.${ag}"
done
