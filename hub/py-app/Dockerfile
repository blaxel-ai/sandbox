ARG SANDBOX_VERSION=latest

# Use an official Python runtime as a parent image
FROM python:3.12-slim

RUN apt-get update && apt-get install -y git \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /blaxel

COPY --from=ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 8000

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]