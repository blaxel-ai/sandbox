ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM ubuntu:24.04

ARG RUNNER_VERSION=2.338.2
ARG RUNNER_SHA256_X64=baebf204307bac6193480852732f0aba2ae97b6c5597c244f4d5558390147c40
ARG RUNNER_SHA256_ARM64=670adb2f268fe21dcbb58b3db0e0aa9d5b3bddfe2c651481336e561ff86de945
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive \
    RUNNER_ALLOW_RUNASROOT=1 \
    DOCKER_INSECURE_NO_IPTABLES_RAW=1

RUN apt-get update -qq \
  && apt-get install -y -qq --no-install-recommends \
    build-essential \
    ca-certificates \
    curl \
    docker-buildx \
    docker-compose-v2 \
    docker.io \
    git \
    git-lfs \
    gnupg \
    iproute2 \
    iptables \
    jq \
    lsb-release \
    python3 \
    python3-pip \
    python3-venv \
    sudo \
    unzip \
    xz-utils \
    zip \
    zstd \
  && rm -rf /var/lib/apt/lists/*

RUN update-alternatives --set iptables /usr/sbin/iptables-legacy \
  && update-alternatives --set ip6tables /usr/sbin/ip6tables-legacy

RUN case "${TARGETARCH:-amd64}" in \
      amd64) arch=x64 sha=${RUNNER_SHA256_X64} ;; \
      arm64) arch=arm64 sha=${RUNNER_SHA256_ARM64} ;; \
      *) echo "unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
  && mkdir -p /actions-runner \
  && curl -fsSL --retry 3 --retry-delay 5 -o /tmp/actions-runner.tar.gz \
    "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${arch}-${RUNNER_VERSION}.tar.gz" \
  && echo "${sha}  /tmp/actions-runner.tar.gz" | sha256sum -c - \
  && tar xzf /tmp/actions-runner.tar.gz -C /actions-runner \
  && rm /tmp/actions-runner.tar.gz \
  && /actions-runner/bin/installdependencies.sh \
  && rm -rf /var/lib/apt/lists/*

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

COPY hub/github-runner/entrypoint.sh /entrypoint.sh
COPY hub/github-runner/start-runner.sh /usr/local/bin/start-runner
COPY hub/github-runner/job-started.sh /usr/local/bin/job-started.sh
RUN chmod +x /entrypoint.sh /usr/local/bin/start-runner /usr/local/bin/job-started.sh

ENTRYPOINT ["/entrypoint.sh"]
