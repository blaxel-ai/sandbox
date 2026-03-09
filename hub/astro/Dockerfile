ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:22-alpine

RUN apk update && apk add --no-cache \
  git \
  curl \
  netcat-openbsd \
  && rm -rf /var/cache/apk/*

WORKDIR /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

RUN mkdir -p /blaxel/app

RUN npx create-astro@latest /blaxel/app --template basics --no-install --no-git --yes \
  && cd /blaxel/app && npm install

COPY /hub/astro/astro.config.mjs /blaxel/app/astro.config.mjs

COPY /hub/astro/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 4321

ENTRYPOINT ["/entrypoint.sh"]
