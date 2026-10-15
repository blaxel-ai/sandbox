ARG SANDBOX_VERSION=latest
FROM docker.io/baseten/sandbox:${SANDBOX_VERSION} AS sandbox-api

# Node 24 (current LTS), on Debian slim (glibc). Two reasons over node:23-alpine:
# (1) glibc — heavy native modules (duckdb, canvas, ...) ship glibc node-pre-gyp
#     prebuilts but no musl ones, so on Alpine they compile from source and fail.
# (2) Node 24 (ABI node-v137) — duckdb publishes a v137 prebuilt; Node 23
#     (v131, a non-LTS odd release) was skipped, so it 404s and falls back to a
#     source compile. Node 24 downloads the prebuilt and just works.
FROM node:24-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
  bash \
  git \
  ca-certificates \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 3000

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
