ARG SANDBOX_VERSION=latest
FROM docker.io/baseten/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-alpine3.21

RUN apk update && apk add --no-cache \
  bash \
  git \
  && rm -rf /var/cache/apk/*

WORKDIR /workspace

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 3000

ENTRYPOINT ["/usr/local/bin/sandbox-api"]