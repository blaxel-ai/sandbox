ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-alpine3.21

RUN apk update && apk add --no-cache \
  bash \
  ca-certificates \
  curl \
  git \
  jq \
  openssh-client \
  py3-pip \
  python3 \
  rclone \
  wget \
  && rm -rf /var/cache/apk/* \
  && rm -f /usr/lib/python3.*/EXTERNALLY-MANAGED

WORKDIR /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
COPY hub/app-runner/runner.sh /usr/local/bin/app-runner

RUN chmod +x /usr/local/bin/sandbox-api /usr/local/bin/app-runner

EXPOSE 8080

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
