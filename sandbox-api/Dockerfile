FROM --platform=linux/amd64 golang:1.25.0-bookworm as build

ARG VERSION=dev
ARG GIT_COMMIT=unknown

WORKDIR /blaxel/sandbox-api

COPY . /blaxel/sandbox-api

RUN set -xe; \
  BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") && \
  go build \
  -v \
  -buildmode=pie \
  -ldflags "-linkmode external -extldflags -static-pie \
    -X github.com/blaxel-ai/sandbox-api/src/handler.Version=${VERSION} \
    -X github.com/blaxel-ai/sandbox-api/src/handler.GitCommit=${GIT_COMMIT} \
    -X github.com/blaxel-ai/sandbox-api/src/handler.BuildTime=${BUILD_TIME}" \
  -tags netgo \
  -o sandbox-api .


RUN mv sandbox-api /