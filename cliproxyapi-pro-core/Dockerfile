FROM golang:1.26-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS builder

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl jq python3 build-essential git && rm -rf /var/lib/apt/lists/*

WORKDIR /src/CLIProxyAPI

ARG CLIPROXY_REPO=router-for-me/CLIProxyAPI
ARG CLIPROXY_VERSION=""
ARG CLIPROXY_COMMIT=""
ARG CLIPROXY_BUILD_VERSION=""
ARG GITHUB_TOKEN=""
ARG TARGETOS
ARG TARGETARCH

RUN set -eux; \
    auth_header=""; \
    if [ -n "$GITHUB_TOKEN" ]; then auth_header="Authorization: Bearer $GITHUB_TOKEN"; fi; \
    if [ -z "$CLIPROXY_VERSION" ]; then \
      if [ -n "$auth_header" ]; then \
        CLIPROXY_VERSION="$(curl -fsSL -H "$auth_header" "https://api.github.com/repos/${CLIPROXY_REPO}/releases/latest" | jq -r '.tag_name')"; \
      else \
        CLIPROXY_VERSION="$(curl -fsSL "https://api.github.com/repos/${CLIPROXY_REPO}/releases/latest" | jq -r '.tag_name')"; \
      fi; \
    fi; \
    test -n "$CLIPROXY_VERSION"; \
    test "$CLIPROXY_VERSION" != "null"; \
    source_ref="${CLIPROXY_COMMIT:-refs/tags/${CLIPROXY_VERSION}}"; \
    curl -fsSL "https://github.com/${CLIPROXY_REPO}/archive/${source_ref}.tar.gz" -o /tmp/cliproxy.tar.gz; \
    tar -xzf /tmp/cliproxy.tar.gz --strip-components=1 -C /src/CLIProxyAPI; \
    rm /tmp/cliproxy.tar.gz; \
    echo "$CLIPROXY_VERSION" > /tmp/cliproxy-version

COPY embeddedusage /tmp/embeddedusage
COPY patches /tmp/patches

RUN python3 /tmp/patches/apply_upstream_patches.py

RUN set -eux; \
    go mod tidy; \
    CLIPROXY_SOURCE_VERSION="$(cat /tmp/cliproxy-version)"; \
    if [ -z "$CLIPROXY_BUILD_VERSION" ]; then CLIPROXY_BUILD_VERSION="$CLIPROXY_SOURCE_VERSION"; fi; \
    BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
    CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -buildvcs=false \
      -ldflags="-s -w -X 'main.Version=${CLIPROXY_BUILD_VERSION}' -X 'main.Commit=release-${CLIPROXY_SOURCE_VERSION}' -X 'main.BuildDate=${BUILD_DATE}'" \
      -o /out/CLIProxyAPI ./cmd/server/

FROM debian:bookworm@sha256:9344f8b8992482f80cba753f323adeaf17690076c095ccff6cc9536be98185dc

RUN mkdir -p /CLIProxyAPI/usage

COPY --from=ghcr.io/komari-monitor/komari-agent:latest@sha256:48815bdfce540d83c60b92809858d9aea3635d07abf78581486e0433ca97d676 /app/komari-agent /CLIProxyAPI/komari-agent

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tzdata && rm -rf /var/lib/apt/lists/*

WORKDIR /CLIProxyAPI

COPY --from=builder /out/CLIProxyAPI /CLIProxyAPI/CLIProxyAPI
COPY --from=builder /src/CLIProxyAPI/config.example.yaml /CLIProxyAPI/config.example.yaml
COPY entrypoint.sh /CLIProxyAPI/entrypoint.sh

ENV TZ=Asia/Shanghai
ENV USAGE_DATA_DIR=/CLIProxyAPI/usage
ENV USAGE_DB_PATH=/CLIProxyAPI/usage/usage.sqlite
ENV USAGE_BATCH_SIZE=100
ENV USAGE_POLL_INTERVAL_MS=500
ENV USAGE_QUERY_LIMIT=50000

VOLUME ["/CLIProxyAPI/usage"]

EXPOSE 8317

RUN chmod -R 777 /CLIProxyAPI && chmod +x /CLIProxyAPI/entrypoint.sh

CMD ["/CLIProxyAPI/entrypoint.sh"]
