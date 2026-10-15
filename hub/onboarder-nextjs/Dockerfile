ARG SANDBOX_VERSION=latest
FROM docker.io/baseten/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-alpine3.21

RUN apk update && apk add --no-cache \
  bash \
  curl \
  git \
  netcat-openbsd \
  && rm -rf /var/cache/apk/*

WORKDIR /app

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

# Platform-managed image for the onboarding first-sandbox flow. Ships a
# Sandbox Lab starter instead of a generic Next.js/Vercel scaffold. Not the
# general nextjs template — see hub/nextjs for that.
COPY /hub/onboarder-nextjs/app-template/package.json /app/package.json
COPY /hub/onboarder-nextjs/app-template/package-lock.json /app/package-lock.json
RUN cd /app && npm ci

COPY /hub/onboarder-nextjs/app-template/ /app/
COPY /hub/onboarder-nextjs/next.config.ts /app/next.config.ts

COPY /hub/onboarder-nextjs/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Expose ports for Next.js dev server
EXPOSE 3000

ENTRYPOINT ["/entrypoint.sh"]
