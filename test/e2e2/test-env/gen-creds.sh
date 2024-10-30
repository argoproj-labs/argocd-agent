#!/bin/bash
# Copyright 2024 The argocd-agent Authors
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
# Script to generate credentials for e2e-tests of argocd-agent.
#
# WARNING: Development script. Do not use to produce production credentials.
# This script comes without any promises. It should only be used to generate
# credentials for your dev or demo environments. The passwords produced are
# weak.
##############################################################################

set -eo pipefail
if ! pwmake=$(which pwmake); then
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
	password=$($pwmake 56)
	$htpasswd -b -B "${creds_path}/users.control-plane" "${ag}" "${password}"
	echo "${ag}:${password}" > "${creds_path}/creds.${ag}"
done
