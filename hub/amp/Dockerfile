ARG SANDBOX_VERSION=latest

FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-slim@sha256:2c87ef9bd3c6a3bd4b472b4bec2ce9d16354b0c574f736c476489d09f560a203

ARG TARGETARCH
# Pinned Amp CLI release. Bump AMP_VERSION together with both checksums, taken from
# https://static.ampcode.com/cli/${AMP_VERSION}/<platform>-amp.sha256.
ARG AMP_VERSION=0.0.1790816272-ge96b2b
# linux-x64-baseline runs on any x86-64 host, unlike linux-x64 which needs AVX2.
ARG AMP_SHA256_AMD64=7a7d6310164ac01ae52619c2e67b1e7c8f635870054d904f065f17a5761214d5
ARG AMP_SHA256_ARM64=f237015d326e99c70f907d6b6c97c199b6eaec2943d7fd1381fa8f72e1bbb90e

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

# Download the pinned release binary directly and verify it against the pinned
# checksum, so the build never runs a mutable remote installer script.
RUN set -eu; \
  case "${TARGETARCH:-amd64}" in \
    amd64) platform=linux-x64-baseline; sha256="${AMP_SHA256_AMD64}" ;; \
    arm64) platform=linux-arm64; sha256="${AMP_SHA256_ARM64}" ;; \
    *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
  esac; \
  mkdir -p /opt/amp/bin; \
  curl -fsSL "https://static.ampcode.com/cli/${AMP_VERSION}/amp-${platform}.gz" | gzip -dc > /opt/amp/bin/amp; \
  echo "${sha256}  /opt/amp/bin/amp" | sha256sum -c -; \
  chmod 0755 /opt/amp/bin/amp; \
  ln -s /opt/amp/bin/amp /usr/local/bin/amp; \
  amp --version | grep -F "${AMP_VERSION}"

WORKDIR /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
RUN chmod +x /usr/local/bin/sandbox-api

EXPOSE 8080

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
