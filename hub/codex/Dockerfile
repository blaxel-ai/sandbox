ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
  bash \
  ca-certificates \
  curl \
  git \
  ripgrep \
  && rm -rf /var/lib/apt/lists/*

RUN npm install -g @openai/codex@latest

WORKDIR /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
RUN chmod +x /usr/local/bin/sandbox-api

EXPOSE 8080

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
