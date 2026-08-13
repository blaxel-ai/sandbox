# Build context is the repository root, matching every other hub image and what
# .github/workflows/build.yaml passes (context: . , file: ./hub/<name>/Dockerfile).
ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM golang:1.25-alpine AS blbuild-build
WORKDIR /src
COPY blbuild/go.mod blbuild/go.sum ./
RUN go mod download
COPY blbuild/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /blbuild .

FROM alpine:3.21

# buildctl is a SEPARATE apk package from buildkit (which only ships buildkitd).
RUN apk add --no-cache \
  bash \
  buildctl \
  buildkit \
  curl \
  gzip \
  jq \
  tar \
  unzip \
  zstd \
  && rm -rf /var/cache/apk/*

# erofs-utils from edge (1.9.x), NOT 3.21's 1.8.2. Measured on a real build:
# 1.8.2's --incremental produces "no enough room for the root inode" and an
# image fsck.erofs reports as corrupted. 1.9.3 builds clean.
RUN apk add --no-cache -u \
  --repository=https://dl-cdn.alpinelinux.org/alpine/edge/main \
  --repository=https://dl-cdn.alpinelinux.org/alpine/edge/community \
  erofs-utils \
  && rm -rf /var/cache/apk/* \
  && mkfs.erofs --version

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
COPY --from=blbuild-build /blbuild /usr/local/bin/blbuild

# The artefacts a produced image needs, same set the current builder embeds:
#  - kernel.bin ships as kraft/bin/kernel
#  - metamorph-wrapper is injected at /bin/metamorph-wrapper, which cmdline.txt
#    execs (mk3.0 needs it, inert on mk3.1)
# Populated by scripts/fetch-builder-artefacts.sh. blfs is NOT here: the push
# that builds this image injects it at /usr/local/bin/blfs, and blbuild
# re-injects that same binary into the images it produces, which keeps the FUSE
# client in lockstep with the platform.
COPY hub/builder/embedded/kernel.bin /opt/blaxel/kernel.bin
COPY hub/builder/embedded/metamorph-wrapper /opt/blaxel/metamorph-wrapper

# Dockerfile templates for projects that ship none, taken from metamorph so a
# project builds the same whichever builder handles it.
COPY hub/builder/embedded/dockerfile.node.tmpl /opt/blaxel/templates/dockerfile.node.tmpl
COPY hub/builder/embedded/dockerfile.python.tmpl /opt/blaxel/templates/dockerfile.python.tmpl
COPY hub/builder/embedded/dockerfile.golang.tmpl /opt/blaxel/templates/dockerfile.golang.tmpl

COPY hub/builder/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /usr/local/bin/blbuild /opt/blaxel/metamorph-wrapper

ENTRYPOINT ["/entrypoint.sh"]
