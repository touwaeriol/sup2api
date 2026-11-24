# syntax=docker/dockerfile:1.7
# =============================================================================
# sub2api-next image. Build context: next/
#
#   docker build -f next/Dockerfile next/ --build-arg VERSION=0.1.0
#
# Stages:
#   uisrc  - collects only the frontend sources (web/, plugins/*/ui/) so the
#            npm stage is cached independently of Go changes
#   ui     - builds web/ -> server/web/dist and plugins/*/ui/native -> dist
#            (the plugin UIs build with web/'s toolchain, so web/ must be
#            there; see deploy/docker/build-ui.sh)
#   build  - builds server, tools/sub2api-plugin and the plugins; packs and
#            signs plugins with a dev key and writes the (unsigned) market index
#   final  - debian:trixie-slim runtime
#
# The dev signing key is generated on first build and kept in a BuildKit cache
# mount (id=sub2api-next-devkeys) so rebuilds keep the same key and installed
# plugins stay verifiable. Only the PUBLIC key is copied into the image
# (/opt/sub2api/market/dev-official.pub); the entrypoint turns it into
# SUB2API_PLUGIN_OFFICIAL_KEYS unless set. Stacks that serve the bundled
# market point SUB2API_MARKET_URL at it.
# =============================================================================

ARG NODE_IMAGE=node:24
# Preserve the compiler used by published immutable plugin packages. A toolchain
# upgrade requires explicit package versioning and artifact verification.
ARG GO_IMAGE=golang:1.27.1-trixie@sha256:8f58fd67ea075142d947a60e0caa4317746a55118d312f027793d382c7741734
ARG RUNTIME_IMAGE=debian:trixie-slim

# -----------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS uisrc
RUN --mount=type=bind,target=/ctx sh /ctx/deploy/docker/collect-ui.sh /ctx /s

# -----------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS ui
ARG NPM_CONFIG_REGISTRY=
WORKDIR /src
COPY --from=uisrc /s/ /src/
RUN --mount=type=cache,id=sub2api-next-npm,target=/root/.npm \
    sh /src/deploy/docker/build-ui.sh /src /out

# -----------------------------------------------------------------------------
FROM ${GO_IMAGE} AS build
ARG VERSION=0.1.0-dev
ARG GOPROXY=
ARG REQUIRE_EXISTING_DEV_KEY=0
ARG BUILD_MAX_PROCS=
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
WORKDIR /src
COPY . .
COPY --from=ui /out/ /src/
RUN --mount=type=cache,id=sub2api-next-gomod,target=/go/pkg/mod \
    --mount=type=cache,id=sub2api-next-gobuild,target=/root/.cache/go-build \
    --mount=type=cache,id=sub2api-next-devkeys,target=/devkeys \
    if [ -n "${GOPROXY}" ]; then export GOPROXY="${GOPROXY}"; fi && \
    VERSION="${VERSION}" REQUIRE_EXISTING_DEV_KEY="${REQUIRE_EXISTING_DEV_KEY}" BUILD_MAX_PROCS="${BUILD_MAX_PROCS}" sh deploy/docker/build-go.sh /src /out /devkeys

# -----------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE}
ARG VERSION=0.1.0-dev
LABEL org.opencontainers.image.title="sub2api-next" \
      org.opencontainers.image.version="${VERSION}"

# procps: ps/pkill for debugging and for e2e tests that kill plugin processes.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata procps \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 1000 sub2api \
 && useradd -u 1000 -g sub2api -d /var/lib/sub2api -s /usr/sbin/nologin sub2api \
 && mkdir -p /var/lib/sub2api/plugins /opt/sub2api/market \
 && chown -R sub2api:sub2api /var/lib/sub2api

COPY --from=build /out/bin/ /usr/local/bin/
COPY --from=build /out/market/ /opt/sub2api/market/
COPY --from=build /out/builtin/ /opt/sub2api/builtin/
COPY --chmod=0755 deploy/docker/entrypoint.sh /usr/local/bin/docker-entrypoint.sh

ENV SUB2API_HTTP_ADDR=:8080 \
    SUB2API_PLUGIN_DIR=/var/lib/sub2api/plugins
USER sub2api
WORKDIR /var/lib/sub2api
EXPOSE 8080

# No curl in the slim image: probe /healthz with bash's /dev/tcp.
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=3 \
  CMD ["bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/8080 && printf 'GET /healthz HTTP/1.0\\r\\nHost: localhost\\r\\n\\r\\n' >&3 && head -n1 <&3 | grep -q ' 200 '"]

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["sub2api"]
