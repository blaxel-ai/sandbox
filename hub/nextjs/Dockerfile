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

# Start with a fresh Next.js project
RUN mkdir -p /blaxel/app

RUN npx create-next-app@latest /blaxel/app --use-npm --typescript --eslint --tailwind --src-dir --app --import-alias "@/*" --no-git --yes --no-turbopack

COPY /hub/nextjs/next.config.ts /blaxel/app/next.config.ts

COPY /hub/nextjs/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Expose ports for Next.js dev server
EXPOSE 3000

ENTRYPOINT ["/entrypoint.sh"]
