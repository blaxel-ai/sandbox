ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:22-alpine

RUN apk update && apk add --no-cache \
  git \
  curl \
  netcat-openbsd \
  && rm -rf /var/cache/apk/*

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

WORKDIR /blaxel

RUN npm create vite@latest app -- --template react-ts

# Install patch-package globally first to avoid postinstall script errors
RUN npm install -g patch-package

RUN cd app && npm install

COPY /hub/vite/src/vite.config.ts /blaxel/app/vite.config.ts

COPY /hub/vite/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 5173

ENTRYPOINT ["/entrypoint.sh"]
