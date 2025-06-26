FROM node:22-alpine

COPY --from=ghcr.io/blaxel-ai/sandbox:latest /sandbox-api /usr/local/bin/sandbox-api

RUN apk update && apk add --no-cache \
  git \
  && rm -rf /var/cache/apk/*

WORKDIR /blaxel

RUN npm create vite@latest app -- --template react-ts

RUN cd app && npm install

COPY /hub/vite/src/vite.config.ts /blaxel/app/vite.config.ts

EXPOSE 5173

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
