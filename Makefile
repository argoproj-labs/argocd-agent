DOCKER_BIN?=docker

# Image names
IMAGE_REPOSITORY?=ghcr.io/argoproj-labs/argocd-agent
IMAGE_NAME=argocd-agent
IMAGE_PLATFORMS?=linux/amd64
IMAGE_TAG?=latest

# mkdocs related configuration
MKDOCS_DOCKER_IMAGE?=squidfunk/mkdocs-material:9
MKDOCS_RUN_ARGS?=

# Binary names
BIN_NAME_ARGOCD_AGENT=argocd-agent
BIN_NAME_CLI=argocd-agentctl
BIN_ARCH?=$(shell go env GOARCH)
BIN_OS?=$(shell go env GOOS)
LDFLAGS?=

current_dir := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
BIN_DIR := $(current_dir)/build/bin

PROTOC_GEN_GO_VERSION?=v1.28
PROTOC_GEN_GO_GRPC_VERSION=v1.2
GOLANG_CI_LINT_VERSION=v2.1.6
MOCKERY_V2_VERSION?=v2.43.0

GO_BIN_DIR=$(shell go env GOPATH)/bin

GIT_COMMIT=$(shell git rev-parse HEAD)
VERSION=$(shell cat VERSION)

VERSION_PACKAGE=github.com/argoproj-labs/argocd-agent/internal/version
override LDFLAGS += -extldflags "-static"
override LDFLAGS += \
        -X ${VERSION_PACKAGE}.version=${VERSION} \
        -X ${VERSION_PACKAGE}.gitRevision=${GIT_COMMIT}

all: build

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: build
build: argocd-agent cli

.PHONY: setup-e2e
setup-e2e: cli
	./hack/dev-env/setup-vcluster-env.sh create
	./hack/dev-env/gen-creds.sh
	./hack/dev-env/create-agent-config.sh

.PHONY: teardown-e2e
teardown-e2e:
	./hack/dev-env/setup-vcluster-env.sh delete

.PHONY: start-e2e
start-e2e: cli install-goreman
	./hack/dev-env/start-e2e.sh

.PHONY: test-e2e
test-e2e:
	./test/run-e2e.sh

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
	GOBIN=$(BIN_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANG_CI_LINT_VERSION)

./build/bin/protoc-gen-go: ./build/bin
	GOBIN=$(BIN_DIR) go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)

./build/bin/protoc-gen-go-grpc: ./build/bin
	GOBIN=$(BIN_DIR) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

./build/bin/protoc: ./build/bin
	./hack/install/install-protoc.sh

./build/bin/mockery: ./build/bin
	GOBIN=$(current_dir)/build/bin go install github.com/vektra/mockery/v2@$(MOCKERY_V2_VERSION)

$(GO_BIN_DIR)/goreman:
	go install github.com/mattn/goreman@latest

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

.PHONY: install-goreman
install-goreman: $(GO_BIN_DIR)/goreman
	@echo "goreman installed to $(GO_BIN_DIR)"

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

.PHONY: argocd-agent
argocd-agent:
	CGO_ENABLED=0 GOARCH=$(BIN_ARCH) GOOS=$(BIN_OS) go build -v -o dist/$(BIN_NAME_AGENT) -ldflags '$(LDFLAGS)' ./cmd/argocd-agent

.PHONY: cli
cli:
	CGO_ENABLED=0 GOARCH=$(BIN_ARCH) GOOS=$(BIN_OS) go build -v -o dist/$(BIN_NAME_CLI) -ldflags '$(LDFLAGS)' ./cmd/ctl

.PHONY: image
image:
	$(DOCKER_BIN) build -f Dockerfile --platform $(IMAGE_PLATFORMS) -t $(IMAGE_REPOSITORY)/$(IMAGE_NAME):$(IMAGE_TAG) .

.PHONY: push-image
push-image:
	$(DOCKER_BIN) push $(IMAGE_REPOSITORY)/$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: serve-docs
serve-docs:
	${DOCKER_BIN} run ${MKDOCS_RUN_ARGS} --rm -it -p 8000:8000 -v ${current_dir}:/docs ${MKDOCS_DOCKER_IMAGE} serve -a 0.0.0.0:8000

.PHONY: build-docs
build-docs:
	${DOCKER_BIN} run ${MKDOCS_RUN_ARGS} --rm -v ${current_dir}:/docs ${MKDOCS_DOCKER_IMAGE} build


.PHONY: help
help:
	@echo "Not yet, sorry."
