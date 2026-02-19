FROM alpine:3.19

COPY --from=ghcr.io/blaxel-ai/sandbox:latest /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sandbox-api"]