AUTONOMOUS_MODE?=false
GOBIN=$(shell go env GOPATH)/bin

.PHONY: start-local
start-local:
	AUTONOMOUS_MODE=$(AUTONOMOUS_MODE) $(GOBIN)/goreman -set-ports=false -f Procfile start

.PHONY: help
help:
	@echo "Not yet, sorry."
