FROM golang:1.25.0-bookworm as build

WORKDIR /blaxel/sandbox-api

COPY sandbox-api/ /blaxel/sandbox-api/

RUN go build \
  -v \
  -buildmode=pie \
  -ldflags "-linkmode external -extldflags -static-pie" \
  -tags netgo \
  -o sandbox-api .

FROM python:3.12-slim

WORKDIR /blaxel

COPY --from=build /blaxel/sandbox-api/sandbox-api /blaxel/sandbox-api

EXPOSE 8080

ENTRYPOINT ["/blaxel/sandbox-api"]
