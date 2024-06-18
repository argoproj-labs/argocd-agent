DOCKER_BIN?=docker

# Image names
IMAGE_REPOSITORY=quay.io/jannfis
IMAGE_NAME_AGENT=argocd-agent-agent
IMAGE_NAME_PRINCIPAL=argocd-agent-principal
IMAGE_PLATFORMS?=linux/amd64
IMAGE_TAG?=latest

# Binary names
BIN_NAME_AGENT=argocd-agent-agent
BIN_NAME_PRINCIPAL=argocd-agent-principal
BIN_ARCH?=$(shell go env GOARCH)
BIN_OS?=$(shell go env GOOS)

current_dir := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
BIN_DIR := $(current_dir)/build/bin

PROTOC_GEN_GO_VERSION?=v1.28
PROTOC_GEN_GO_GRPC_VERSION=v1.2
GOLANG_CI_LINT_VERSION=v1.58.1
MOCKERY_V2_VERSION?=v2.43.0

.PHONY: build
build: agent principal

.PHONY: test
test:
	mkdir -p test/out
	./hack/test.sh

.PHONY: mod-vendor
mod-vendor:
	go mod vendor

.PHONY: clean
clean:
	rm -rf dist/ vendor/

# clean-all also removes any build-time dependencies installed into the tree
.PHONY: clean-all
clean-all: clean
	rm -rf build

./build/bin:
	mkdir -p build/bin

./build/bin/golangci-lint: ./build/bin
	GOBIN=$(BIN_DIR) go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANG_CI_LINT_VERSION)

./build/bin/protoc-gen-go: ./build/bin
	GOBIN=$(BIN_DIR) go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)

./build/bin/protoc-gen-go-grpc: ./build/bin
	GOBIN=$(BIN_DIR) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

./build/bin/protoc: ./build/bin
	./hack/install/install-protoc.sh

./build/bin/mockery: ./build/bin
	GOBIN=$(current_dir)/build/bin go install github.com/vektra/mockery/v2@$(MOCKERY_V2_VERSION)

.PHONY: install-mockery
install-mockery: ./build/bin/mockery

.PHONY: install-golangci-lint
install-golangci-lint: ./build/bin/golangci-lint
	@echo "golangci-lint installed."

.PHONY: install-protoc-go
install-protoc-go: ./build/bin/protoc-gen-go ./build/bin/protoc-gen-go-grpc
	@echo "protoc-gen-go and protoc-gen-go-grpc installed."

.PHONY: install-protoc
install-protoc: ./build/bin/protoc
	@echo "protoc installed."

.PHONY: install-proto-toolchain
install-proto-toolchain: install-protoc install-protoc-go
	@echo "Build toolchain installed."

.PHONY: install-lint-toolchain
install-lint-toolchain: install-golangci-lint
	@echo "Lint toolchain installed."

.PHONY: install-build-deps
install-build-deps: install-lint-toolchain install-proto-toolchain
	@echo "Build dependencies installed"

.PHONY: protogen
protogen: mod-vendor install-proto-toolchain
	./hack/generate-proto.sh

.PHONY: mocks
mocks: install-mockery
	$(BIN_DIR)/mockery

.PHONY: codegen
codegen: protogen

.PHONY: lint
lint: install-lint-toolchain
	$(BIN_DIR)/golangci-lint run --verbose

.PHONY: agent
agent:
	CGO_ENABLED=0 GOARCH=$(BIN_ARCH) GOOS=$(BIN_OS) go build -v -o dist/$(BIN_NAME_AGENT) -ldflags="-extldflags=-static" cmd/agent/main.go

.PHONY: principal
principal:
	CGO_ENABLED=0 GOARCH=$(BIN_ARCH) GOOS=$(BIN_OS) go build -v -o dist/$(BIN_NAME_PRINCIPAL) -ldflags="-extldflags=-static" cmd/principal/main.go

.PHONY: images
images: image-agent image-principal

.PHONY: image-agent
image-agent:
	$(DOCKER_BIN) build -f Dockerfile.agent --platform $(IMAGE_PLATFORMS) -t $(IMAGE_REPOSITORY)/$(IMAGE_NAME_AGENT):$(IMAGE_TAG) .

.PHONY: image-principal
image-principal:
	$(DOCKER_BIN) build -f Dockerfile.principal --platform $(IMAGE_PLATFORMS) -t $(IMAGE_REPOSITORY)/$(IMAGE_NAME_PRINCIPAL):$(IMAGE_TAG) .

push-images:
	$(DOCKER_BIN) push $(IMAGE_REPOSITORY)/$(IMAGE_NAME_AGENT):$(IMAGE_TAG)
	$(DOCKER_BIN) push $(IMAGE_REPOSITORY)/$(IMAGE_NAME_PRINCIPAL):$(IMAGE_TAG)

.PHONY: help
help:
	@echo "Not yet, sorry."
