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


# Use an official Python runtime as a parent image
FROM --platform=linux/amd64 python:3.12-slim

RUN apt-get update && apt-get install -y \
  git \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /blaxel

COPY --from=build /blaxel/sandbox-api/sandbox-api-binary /usr/local/bin/sandbox-api

EXPOSE 8080 8000

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]