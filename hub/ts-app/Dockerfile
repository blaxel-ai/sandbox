ARG SANDBOX_VERSION=latest
FROM docker.io/baseten/sandbox:${SANDBOX_VERSION} AS sandbox-api

# Use an official Python runtime as a parent image
FROM node:24-slim

RUN apt-get update && apt-get install -y git \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 3000

ENTRYPOINT ["/usr/local/bin/sandbox-api"]