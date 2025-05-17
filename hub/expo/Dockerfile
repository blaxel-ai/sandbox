FROM --platform=linux/amd64 golang:1.23.3-bookworm as build

WORKDIR /blaxel/sandbox-api

COPY ./sandbox-api /blaxel/sandbox-api

RUN set -xe; \
    go build \
    -v \
    -buildmode=pie \
    -ldflags "-linkmode external -extldflags -static-pie" \
    -tags netgo \
    -o sandbox-api-binary . \
    ;

FROM --platform=linux/amd64 node:22-alpine

RUN apk update && apk add --no-cache \
  git rsync \
  && rm -rf /var/cache/apk/*

WORKDIR /blaxel

COPY --from=build /blaxel/sandbox-api/sandbox-api-binary /usr/local/bin/sandbox-api


# Option 1: Start with a fresh Expo project
RUN npx create-expo-app@latest app

# Expose ports for Expo dev server and Metro bundler
EXPOSE 8080 19000 19001 19002 19006 8081

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]