FROM node:23-alpine

RUN apk update && apk add --no-cache \
  git \
  && rm -rf /var/cache/apk/*

WORKDIR /blaxel

COPY --from=ghcr.io/blaxel-ai/sandbox:latest /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 3000

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]