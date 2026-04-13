# syntax=docker/dockerfile:1
FROM docker.io/library/golang:1.25 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN make argocd-agent

FROM docker.io/library/alpine:3.23
RUN apk upgrade --no-cache
COPY --from=builder /src/dist/argocd-agent /bin/argocd-agent
USER 999
ENTRYPOINT ["/bin/argocd-agent"]
