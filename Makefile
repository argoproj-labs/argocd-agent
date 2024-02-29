DOCKER_BIN?=docker

# Image names
IMAGE_REPOSITORY=quay.io/jannfis
IMAGE_NAME_AGENT=argocd-agent-agent
IMAGE_NAME_PRINCIPAL=argocd-agent-principal
IMAGE_TAG?=latest

# Binary names
BIN_NAME_AGENT=argocd-agent-agent
BIN_NAME_PRINCIPAL=argocd-agent-principal
BIN_ARCH?=$(shell go env GOARCH)
BIN_OS?=$(shell go env GOOS)

current_dir := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
BIN_DIR := $(current_dir)/build/bin

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
	GOBIN=$(current_dir)/build/bin go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

./build/bin/protoc-gen-go: ./build/bin
	GOBIN=$(current_dir)/build/bin go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28

./build/bin/protoc-gen-go-grpc: ./build/bin
	GOBIN=$(current_dir)/build/bin go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2

./build/bin/protoc: ./build/bin
	./hack/install/install-protoc.sh

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

.PHONY: protogen
protogen: mod-vendor install-proto-toolchain
	./hack/generate-proto.sh

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
image-agent: agent
	$(DOCKER_BIN) build -f Dockerfile.agent -t $(IMAGE_REPOSITORY)/$(IMAGE_NAME_AGENT):$(IMAGE_TAG)

.PHONY: image-principal
image-principal: principal
	$(DOCKER_BIN) build -f Dockerfile.principal -t $(IMAGE_REPOSITORY)/$(IMAGE_NAME_PRINCIPAL):$(IMAGE_TAG)
