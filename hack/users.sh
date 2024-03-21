#!/bin/sh
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

