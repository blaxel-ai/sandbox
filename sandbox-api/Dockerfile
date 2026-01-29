FROM golang:1.25.0-bookworm as build

WORKDIR /blaxel/sandbox-api

COPY . /blaxel/sandbox-api

RUN set -xe; \
  go build \
  -v \
  -buildmode=pie \
  -ldflags "-linkmode external -extldflags -static-pie" \
  -tags netgo \
  -o sandbox-api .


RUN mv sandbox-api /