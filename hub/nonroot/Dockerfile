ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-alpine3.21

RUN apk update && apk add --no-cache \
  bash \
  git \
  python3 \
  py3-pip \
  && rm -rf /var/cache/apk/*

# The unprivileged identity every user-launched process runs as. There is
# deliberately no USER directive: the runtime would then setuid PID 1, which is
# sandbox-api itself, and it would lose the privileges it needs to mount agent
# drives (FUSE), bring up the egress tunnel and merge the CA bundle.
RUN adduser -D -u 10001 -h /blaxel app

WORKDIR /blaxel

# Everything the workload owns has to be writable by it, since it is no longer
# root. Drive mount points are chowned by the API when the drive is mounted.
RUN chown -R app:app /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080

ENV HOME=/blaxel

# Processes, terminals, codegen and the startup command run as this user, and
# the filesystem API enforces its permissions. Calling the API from inside a
# sandbox process therefore grants nothing extra: it hands back the same
# identity.
ENV BL_SANDBOX_USER=app

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
