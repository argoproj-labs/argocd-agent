FROM docker.io/library/golang:1.23 AS builder
WORKDIR /src
COPY . .
RUN make argocd-agent
	
FROM docker.io/library/alpine:latest

COPY --from=builder /src/dist/argocd-agent /bin/argocd-agent
USER 999
ENTRYPOINT ["/bin/argocd-agent"]
