#!/bin/sh
# requires openssl and httpd-tools (htpasswd) installed
for n in 1 2 3 4 5; do
	client_id=$(openssl rand -hex 16)
	client_secret=$(openssl rand -hex 32)
	hash=$(htpasswd -bnBC 10 "" $client_secret | tr -d ':\n')
	echo "# $client_secret"
	echo $client_id:$hash
done
