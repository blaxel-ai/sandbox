ARG SANDBOX_VERSION=latest
FROM --platform=linux/amd64 ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS blaxel

# Use ComputeSDK self-hosted compute runtime as base (Debian Bookworm)
FROM --platform=linux/amd64 computesdk/compute:latest

# Copy Blaxel sandbox API binary (required for all Blaxel sandbox templates)
COPY --from=blaxel /sandbox-api /usr/local/bin/sandbox-api

# Switch to root for package installation
USER root

# Install netcat for the health-check loop in entrypoint.sh
RUN apt-get update && apt-get install -y --no-install-recommends \
    netcat-openbsd \
    && rm -rf /var/lib/apt/lists/*

# Copy and set up entrypoint
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
