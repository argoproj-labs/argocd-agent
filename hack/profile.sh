#!/bin/sh

set -eo pipefail

go_packages=$(go list ./...) 
for pkg in $go_packages; do
	path=$(echo $pkg | sed -e 's,github.com/argoproj-labs/argocd-application-agent/,,')
	mkdir -p test/profile/$path
	go test -race -mutexprofile test/profile/$path/mutex.profile $pkg || true
done
