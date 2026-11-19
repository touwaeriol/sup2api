# syntax=docker/dockerfile:1.7
# Mock Anthropic / OpenAI / Gemini upstream for the test environment (see main.go).
ARG GO_IMAGE=golang:1.27-trixie
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
RUN --mount=type=cache,id=sub2api-next-gomod,target=/go/pkg/mod \
    --mount=type=cache,id=sub2api-next-gobuild,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOWORK=off go build -trimpath -ldflags "-s -w" -o /out/mock-upstream .

FROM debian:trixie-slim
COPY --from=build /out/mock-upstream /usr/local/bin/mock-upstream
# MOCK_TLS_DIR: the CA the E2E nodes trust is written here (tls.go).
RUN mkdir -p /tls && chown 65534:65534 /tls
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/mock-upstream"]
