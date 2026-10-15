ARG SANDBOX_VERSION=latest
FROM docker.io/baseten/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM superagentai/vibekit-codex:1.0

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
