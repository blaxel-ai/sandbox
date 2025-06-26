FROM node:22-alpine

RUN apk update && apk add --no-cache \
  git rsync \
  && rm -rf /var/cache/apk/*

WORKDIR /blaxel

COPY --from=ghcr.io/blaxel-ai/sandbox:latest /sandbox-api /usr/local/bin/sandbox-api

# Option 1: Start with a fresh Expo project
RUN npx create-expo-app@latest app

# Expose ports for Expo dev server and Metro bundler
EXPOSE 8080 19000 19001 19002 19006 8081

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]