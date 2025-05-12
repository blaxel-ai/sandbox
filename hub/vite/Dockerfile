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

COPY --from=build /blaxel/sandbox-api/sandbox-api-binary /usr/local/bin/sandbox-api

WORKDIR /blaxel

RUN npm create vite@latest app -- --template react-ts

RUN cd app && npm install

COPY /hub/vite/src/vite.config.ts /blaxel/app/vite.config.ts

EXPOSE 5173

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
