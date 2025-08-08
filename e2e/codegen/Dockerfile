FROM node:22-alpine

RUN apk update && apk add --no-cache \
  git \
  curl \
  netcat-openbsd \
  && rm -rf /var/cache/apk/*

WORKDIR /app

COPY --from=ghcr.io/blaxel-ai/sandbox:main-f7c711f /sandbox-api /usr/local/bin/sandbox-api

ENTRYPOINT ["/usr/local/bin/sandbox-api"]