ARG SANDBOX_VERSION=latest
FROM ghcr.io/basetenlabs/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-alpine3.21

RUN apk update && apk add --no-cache \
  bash \
  curl \
  git \
  netcat-openbsd \
  && rm -rf /var/cache/apk/*

WORKDIR /app

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

# Create Expo project with default template
RUN npx create-expo-app@latest /app


# Install web dependencies and expo-asset for asset loading support
RUN npx expo install react-dom react-native-web expo-asset

# Patch expo-asset: it hardcodes http:// when constructing asset URLs
# from the manifest's debuggerHost. Behind the sandbox HTTPS proxy,
# Android's native downloader can't follow the HTTP→HTTPS redirect,
# so fonts and images fail to load. Force https://.
RUN sed -i "s|'http://' + manifest2.extra.expoGo.debuggerHost|'https://' + manifest2.extra.expoGo.debuggerHost|g" \
    node_modules/expo-asset/build/AssetSources.js

# Pre-warm Metro bundler cache by starting the dev server briefly
RUN timeout 60 npx expo start --web --port 8081 2>/dev/null || true

ENV PATH="/usr/local/bin:$PATH"

COPY /hub/expo/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8081

ENTRYPOINT ["/entrypoint.sh"]
