DOCKER_BIN?=docker

IMAGE_REPOSITORY=quay.io/jannfis
IMAGE_NAME_AGENT=argocd-agent-agent
IMAGE_NAME_PRINCIPAL=argocd-agent-principal
IMAGE_TAG?=latest

.PHONY: build
build:
	go build ./...

.PHONY: test
test:
	mkdir -p test/out
	./hack/test.sh

.PHONY: codegen
codegen: protogen

.PHONY: protogen
protogen:
	./hack/generate-proto.sh

.PHONY: agent
agent:
	CGO_ENABLED=0 go build -v -o dist/argocd-agent -ldflags="-extldflags=-static" cmd/agent/main.go 

.PHONY: principal
principal:
	CGO_ENABLED=0 go build -v -o dist/argocd-principal -ldflags="-extldflags=-static" cmd/server/main.go

.PHONY: images
images: image-agent image-principal

.PHONY: image-agent
image-agent: agent
	$(DOCKER_BIN) build -f Dockerfile.agent -t $(IMAGE_REPOSITORY)/$(IMAGE_NAME_AGENT):$(IMAGE_TAG)

.PHONY: image-principal
image-principal: principal
	$(DOCKER_BIN) build -f Dockerfile.principal -t $(IMAGE_REPOSITORY)/$(IMAGE_NAME_PRINCIPAL):$(IMAGE_TAG)
