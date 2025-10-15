ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:22-alpine

RUN apk update && apk add --no-cache \
  git \
  && rm -rf /var/cache/apk/*

WORKDIR /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]