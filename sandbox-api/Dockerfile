# The virtio_ring_resync kernel module the virtio watchdog loads into the guest
# (see kmod/). It is built against the exact kernel the mk3 sandboxes run:
# same upstream release, same .config. Only x86_64 guests exist; other
# architectures ship a binary that detects a broken ring but cannot fix it.
FROM debian:bookworm as kmod

ARG TARGETARCH
ARG GUEST_KERNEL=6.12.75

RUN set -xe; \
  if [ "$TARGETARCH" = "amd64" ]; then \
    apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      build-essential bc bison flex libelf-dev libssl-dev xz-utils curl ca-certificates kmod && \
    rm -rf /var/lib/apt/lists/*; \
  fi

WORKDIR /usr/src
COPY kmod /usr/src/kmod

RUN set -xe; mkdir -p /out; \
  if [ "$TARGETARCH" = "amd64" ]; then \
    curl -fsSL https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-${GUEST_KERNEL}.tar.xz | tar xJ && \
    cp kmod/guest-kernel.config linux-${GUEST_KERNEL}/.config && \
    make -C linux-${GUEST_KERNEL} -j"$(nproc)" olddefconfig modules_prepare && \
    make -C kmod/virtio_ring_resync KDIR=/usr/src/linux-${GUEST_KERNEL} KBUILD_MODPOST_WARN=1 && \
    cp kmod/virtio_ring_resync/virtio_ring_resync.ko /out/ && \
    { make -s -C linux-${GUEST_KERNEL} kernelrelease; grep -v '^#' kmod/guest-kernel.config | grep . | sha256sum | cut -d' ' -f1; } \
      > /out/virtio_ring_resync.kernel && \
    cat /out/virtio_ring_resync.kernel && modinfo /out/virtio_ring_resync.ko; \
  fi

FROM golang:1.25.0-bookworm as build

ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG SENTRY_DSN=""
ARG SENTRY_ENVIRONMENT=""

WORKDIR /blaxel/sandbox-api

COPY . /blaxel/sandbox-api
COPY --from=kmod /out/ /blaxel/sandbox-api/src/lib/networking/kmod/

RUN set -xe; \
  BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") && \
  go build \
  -v \
  -buildmode=pie \
  -ldflags "-linkmode external -extldflags -static-pie \
    -X github.com/blaxel-ai/sandbox-api/src/handler.Version=${VERSION} \
    -X github.com/blaxel-ai/sandbox-api/src/handler.GitCommit=${GIT_COMMIT} \
    -X github.com/blaxel-ai/sandbox-api/src/handler.BuildTime=${BUILD_TIME} \
    -X github.com/blaxel-ai/sandbox-api/src/lib/sentrylib.DSN=${SENTRY_DSN} \
    -X github.com/blaxel-ai/sandbox-api/src/lib/sentrylib.Environment=${SENTRY_ENVIRONMENT}" \
  -tags netgo \
  -o sandbox-api .


RUN mv sandbox-api /
# The published image exists for exactly one purpose: hub images copy the binary
# out of it, in 31 Dockerfiles that are identical to the character —
#
#   COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
#
# Nothing else is ever extracted, and nothing uses it as a runtime base. Without
# this stage the published image IS the builder above, so every one of those
# copies dragged the Go toolchain, the module cache and the source tree along for
# a 53MB binary.
#
# That is not merely wasteful, it broke builds. Sandbox builds run buildkit with
# the native snapshotter — mandatory, since overlayfs cannot stack on the
# read-only EROFS rootfs — which materialises a parent stage in FULL, by copying
# files, once per layer. Measured on hub/base-image: peak scratch went from
# 10 485MB to 2 078MB and the build from 46.1s to 9.3s, for a byte-identical
# 417MiB result. At 10GB the image could not build in the default 8GB of memory
# at all, and failed telling the user to buy a disk it did not need.
#
# scratch rather than a distro: the binary is static-pie with netgo, and this
# image is never run. Use `--target build` to get a shell for debugging.
FROM scratch
COPY --from=build /sandbox-api /sandbox-api
