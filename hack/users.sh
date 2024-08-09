#!/bin/sh
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

# requires httpd-tools (htpasswd) installed
set -e -o pipefail
action="$1"
username="$2"
password="$3"

passwdfile="argocd-agent.passwd"

add_user() {
	local username="$1"
	local password="$2"
	if test -z "$password"; then
		htpasswd -BC 10 "$passwdfile" "$username"
	else
		htpasswd -BbC 10 "$passwdfile" "$username" "${password}"
	fi
}

rm_user() {
	local username="$1"
	htpasswd -D "$passwdfile" "$username"
}

usage() {
	echo "USAGE: $0 add <username> [<password>]" >&2
	echo "       $0 rm <username>" >&2
}

test -f $passwdfile || touch $passwdfile

case "$action" in
"add")
	test -z "$username" && {
		usage
		exit 1
	}
	add_user "$username" "$password"
	;;
"rm")
	test -z "$username" && {
		usage
		exit 1
	}
	rm_user "$username"
	;;
*)
	usage
	exit 1
	;;
esac

