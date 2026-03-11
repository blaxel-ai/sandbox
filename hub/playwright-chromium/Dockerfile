ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:20-bookworm

WORKDIR /home/playwright

ENV HOME=/home/playwright

# Install system dependencies first
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential net-tools \
    && rm -rf /var/lib/apt/lists/*

# Install Playwright globally (for CLI) and locally (for require())
RUN npm cache clean --force && \
    npm i -g playwright@1.58.2 playwright-core@1.58.2 && \
    npm i -g happy-eyeballs && \
    playwright install --with-deps chromium

RUN npm init -y && npm i playwright-core@1.58.2

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 8081

COPY hub/playwright-chromium/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

COPY hub/playwright-chromium/sample-test.js /home/playwright/sample-test.js

ENV PLAYWRIGHT_BROWSERS_PATH=/home/playwright/.cache/ms-playwright

ENTRYPOINT ["/entrypoint.sh"]
