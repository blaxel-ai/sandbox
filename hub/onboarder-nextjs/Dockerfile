ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-alpine3.21

RUN apk update && apk add --no-cache \
  bash \
  curl \
  git \
  netcat-openbsd \
  && rm -rf /var/cache/apk/*

WORKDIR /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

# Platform-managed image for the onboarder's first-sandbox flow (see
# HELLO_WORLD_IMAGE in controlplane). Ships a Blaxel-native Sandbox Lab
# starter instead of a generic Next.js/Vercel scaffold. Not the general
# nextjs template — see hub/nextjs for that.
RUN mkdir -p /blaxel/app

COPY /hub/onboarder-nextjs/app-template/package.json /blaxel/app/package.json
COPY /hub/onboarder-nextjs/app-template/package-lock.json /blaxel/app/package-lock.json
RUN cd /blaxel/app && npm ci

COPY /hub/onboarder-nextjs/app-template/ /blaxel/app/
COPY /hub/onboarder-nextjs/next.config.ts /blaxel/app/next.config.ts

COPY /hub/onboarder-nextjs/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Expose ports for Next.js dev server
EXPOSE 3000

ENTRYPOINT ["/entrypoint.sh"]
