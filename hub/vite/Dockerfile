ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:22-alpine

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

RUN apk update && apk add --no-cache \
  git \
  && rm -rf /var/cache/apk/*

WORKDIR /blaxel

RUN npm create vite@latest app -- --template react-ts

# Install patch-package globally first to avoid postinstall script errors
RUN npm install -g patch-package

RUN cd app && npm install

COPY /hub/vite/src/vite.config.ts /blaxel/app/vite.config.ts

EXPOSE 5173

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
