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

COPY ./hub/bannana-company/src /blaxel/app

RUN cd /blaxel/app && npm install

EXPOSE 3000

ENTRYPOINT ["/usr/local/bin/sandbox-api", "-c", "cd /blaxel/app && npm run dev -- -p 3000"]
