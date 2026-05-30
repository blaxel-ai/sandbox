ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:23-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
  bash \
  git \
  ca-certificates \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 3000

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
