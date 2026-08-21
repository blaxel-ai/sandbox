ARG SANDBOX_VERSION=latest

FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-slim@sha256:2c87ef9bd3c6a3bd4b472b4bec2ce9d16354b0c574f736c476489d09f560a203

ARG AMP_VERSION=0.0.1787328285-g05ea44
ARG AMP_INSTALLER_SHA256=4ceab71af4f5a126d8ffe0e1af3b06f210be67eae93c8e4583793a52064fc2e2

RUN apt-get update && apt-get install -y --no-install-recommends \
  bash \
  ca-certificates \
  curl \
  git \
  gzip \
  python3 \
  python-is-python3 \
  ripgrep \
  && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://ampcode.com/install.sh -o /tmp/install-amp.sh \
  && echo "${AMP_INSTALLER_SHA256}  /tmp/install-amp.sh" | sha256sum -c - \
  && AMP_HOME=/opt/amp AMP_VERSION="${AMP_VERSION}" NO_COLOR=1 bash /tmp/install-amp.sh \
  && ln -s /opt/amp/bin/amp /usr/local/bin/amp \
  && amp --version | grep -F "${AMP_VERSION}" \
  && rm /tmp/install-amp.sh

WORKDIR /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
RUN chmod +x /usr/local/bin/sandbox-api

EXPOSE 8080

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
