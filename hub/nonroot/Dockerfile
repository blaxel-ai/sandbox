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

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
COPY hub/nonroot/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 8080

ENV HOME=/blaxel

# Consumed by the entrypoint, which passes it to sandbox-api as --user. Setting
# the variable alone is enough: sandbox-api reads it directly when no flag is
# given. The entrypoint exists so the image can also do root-only preparation
# (chown, mkdir, mounts) before the workload identity takes effect.
ENV BL_SANDBOX_USER=app

# Processes, terminals, codegen and the startup command run as that user, and
# the filesystem API enforces its permissions. Calling the API from inside a
# sandbox process therefore grants nothing extra: it hands back the same
# identity.
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
