ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:24-bookworm

WORKDIR /home/playwright

ENV HOME=/home/playwright

# Install system dependencies first
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential net-tools \
    && rm -rf /var/lib/apt/lists/*

# Install Playwright globally (for CLI) and locally (for require()).
# Pinned to 1.60.0: on node:24-bookworm, playwright <= 1.59.x hangs forever
# while extracting the downloaded browser archive (the download itself
# succeeds). 1.60.0 fixes the extraction, so the image builds again.
RUN npm cache clean --force && \
    npm i -g playwright@1.60.0 playwright-core@1.60.0 && \
    npm i -g happy-eyeballs && \
    playwright install --with-deps chromium

RUN npm init -y && npm i playwright-core@1.60.0

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 8081

COPY hub/playwright-chromium/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

COPY hub/playwright-chromium/sample-test.js /home/playwright/sample-test.js

ENV PLAYWRIGHT_BROWSERS_PATH=/home/playwright/.cache/ms-playwright

ENTRYPOINT ["/entrypoint.sh"]
