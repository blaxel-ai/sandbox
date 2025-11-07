ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM superagentai/vibekit-gemini:1.0

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
