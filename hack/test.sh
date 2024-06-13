#!/bin/bash
# Wrapper script to run unit tests for argocd-agent
set -eo pipefail
UNIT_LIST=$(go list ./... | grep -E -v '(mock|mocks|e2e|fake|pkg/api/grpc)')
go test -race -timeout 10m -coverprofile test/out/coverage.out $UNIT_LIST
