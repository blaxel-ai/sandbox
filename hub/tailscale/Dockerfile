ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM tailscale/tailscale:stable

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
COPY hub/tailscale/entrypoint.sh /entrypoint.sh

RUN apk add --no-cache bash && chmod +x /entrypoint.sh

ENV TS_EXTRA_ARGS=--ssh \
    TS_STATE_DIR=/var/lib/tailscale \
    TS_USERSPACE=true

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
