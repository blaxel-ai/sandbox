ARG SANDBOX_VERSION=latest
FROM docker.io/baseten/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-alpine3.21

RUN apk update && apk add --no-cache \
  bash \
  curl \
  git \
  netcat-openbsd \
  && rm -rf /var/cache/apk/*

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

WORKDIR /

RUN npm create vite@latest app -- --template react-ts

# Install patch-package globally first to avoid postinstall script errors
RUN npm install -g patch-package

RUN cd app && npm install

WORKDIR /app

COPY /hub/vite/src/vite.config.ts /app/vite.config.ts

COPY /hub/vite/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 5173

ENTRYPOINT ["/entrypoint.sh"]
