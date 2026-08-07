ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

# Keep the Omnigent runtime immutable until this recipe is deliberately updated.
FROM ghcr.io/omnigent-ai/omnigent-host:latest@sha256:0d9480a4ae4b179c450de0e346628165ab5190f867b5465f52c8c9d50d1f4f27

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
RUN chmod +x /usr/local/bin/sandbox-api

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
