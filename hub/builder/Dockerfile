# Build context is the repository root, matching every other hub image and what
# .github/workflows/build.yaml passes (context: . , file: ./hub/<name>/Dockerfile).
ARG SANDBOX_VERSION=latest
# latest tracks metamorph releases, develop tracks their branch. Releases, so
# the builder ships what production runs.
ARG UNIKRAFT_WRAPPER_IMAGE=ghcr.io/blaxel-ai/metamorph/unikraft-wrapper:latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

# The wrapper comes from the image metamorph itself consumes, not from the
# binary committed at metamorph's templates/unikraft_wrapper. That file is dead:
# metamorph's own Dockerfile overwrites it with this image
# (COPY --from=unikraft-wrapper), the committed copy dates from January while
# its .c moved in August, and copying it shipped a six-month-old wrapper.
#
# It cost a day to find, because the failure is invisible on mk3.1: the wrapper
# is in cmdline.txt on both generations, but only mk3.0 delegates the user
# switch to it (mk3.1's init does that). hub/cua-xfce — the one hub image that
# runs as a non-root user — built fine, ran in us-was-1 (mk3.1) and answered
# nothing in eu-dub-1 (mk3.0): the VM booted, got an IP, and the gateway
# returned 404 after its 10s timeout.
#
# Pulled rather than compiled from the .c on purpose: this way the binary is
# identical to metamorph's bit for bit, with no toolchain of ours to drift.
#
# The package grants blaxel-ai/sandbox read access under its own Actions access
# settings, so the job's GITHUB_TOKEN is enough and no extra secret is involved.
FROM --platform=linux/amd64 ${UNIKRAFT_WRAPPER_IMAGE} AS unikraft-wrapper

FROM golang:1.25-alpine AS blbuild-build
WORKDIR /src
COPY blbuild/go.mod blbuild/go.sum ./
RUN go mod download
COPY blbuild/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /blbuild .

# erofs-utils 1.8.10, built from source and pinned — the version metamorph
# builds, not whatever the distribution ships.
#
# The tool writes the on-disk format, so its version is part of the artefact
# contract. mk3.1 runs a current Linux kernel and mounts anything; mk3.0 is a
# Unikraft unikernel whose EROFS driver is frozen, and an image it cannot mount
# kills the VM 70 microseconds after Firecracker reports "Successfully started
# microvm" — no guest logs, just an instant vcpu Shutdown. hub/cua-xfce built
# with 1.9.3 did exactly that on mk3.0 while running fine on mk3.1.
#
# Not the distribution package either way: 3.21 ships 1.8.2, whose --incremental
# produces "no enough room for the root inode" and an image fsck.erofs calls
# corrupted, which is what sent this to edge in the first place. 1.8.10 is
# between the two and is what metamorph validates against.
#
# --enable-multithreading is not optional: it is what makes --workers=N do
# anything, and mkfs is a large share of build time.
FROM alpine:3.21 AS erofs-builder
RUN apk add --no-cache build-base autoconf automake libtool pkgconf \
      lz4-dev util-linux-dev linux-headers curl tar \
  && curl -fsSL https://github.com/erofs/erofs-utils/archive/refs/tags/v1.8.10.tar.gz | tar xz \
  && cd erofs-utils-1.8.10 \
  && autoreconf -i \
  && ./configure --enable-lz4 --enable-multithreading --without-selinux \
  && grep -q 'HAVE_PTHREAD' config.h \
  && make -j"$(nproc)" \
  && make install DESTDIR=/erofs-out

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

# Runtime libraries for the erofs-utils built above: it links against lz4 and
# libuuid, which the build stage had and this image otherwise would not.
RUN apk add --no-cache lz4-libs libuuid && rm -rf /var/cache/apk/*


COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
COPY --from=blbuild-build /blbuild /usr/local/bin/blbuild
# mkfs/fsck/dump from the pinned build above, so the on-disk format the compute
# plane has to mount never depends on what the distribution happens to ship.
COPY --from=erofs-builder /erofs-out/usr/local/bin/ /usr/local/bin/
COPY --from=erofs-builder /erofs-out/usr/local/lib/ /usr/local/lib/

# The artefacts a produced image needs, same set the current builder embeds:
#  - kernel.bin ships as kraft/bin/kernel
#  - metamorph-wrapper is injected at /bin/metamorph-wrapper, which cmdline.txt
#    execs (mk3.0 delegates the user switch to it; mk3.1's init does that itself)
# kernel.bin is populated by scripts/fetch-builder-artefacts.sh; the wrapper is
# pulled from metamorph's published image above. blfs is NOT here: the push
# that builds this image injects it at /usr/local/bin/blfs, and blbuild
# re-injects that same binary into the images it produces, which keeps the FUSE
# client in lockstep with the platform.
COPY hub/builder/embedded/kernel.bin /opt/blaxel/kernel.bin
COPY --from=unikraft-wrapper /unikraft_wrapper /opt/blaxel/metamorph-wrapper

# Dockerfile templates for projects that ship none, taken from metamorph so a
# project builds the same whichever builder handles it.
COPY hub/builder/embedded/dockerfile.node.tmpl /opt/blaxel/templates/dockerfile.node.tmpl
COPY hub/builder/embedded/dockerfile.python.tmpl /opt/blaxel/templates/dockerfile.python.tmpl
COPY hub/builder/embedded/dockerfile.golang.tmpl /opt/blaxel/templates/dockerfile.golang.tmpl

COPY hub/builder/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /usr/local/bin/blbuild /opt/blaxel/metamorph-wrapper

ENTRYPOINT ["/entrypoint.sh"]
