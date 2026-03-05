ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM node:20-bookworm

WORKDIR /home/playwright

ENV HOME=/home/playwright

# Install system dependencies first
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential net-tools \
    && rm -rf /var/lib/apt/lists/*

# Clean npm cache and install Playwright with explicit dependencies
RUN npm cache clean --force && \
    npm i -g playwright@1.53.0 playwright-core@1.53.0 && \
    npm i -g happy-eyeballs && \
    playwright install --with-deps firefox

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 8081

COPY hub/playwright-firefox/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Sample test script to verify Playwright is working
# Usage: node /home/playwright/sample-test.js
COPY hub/playwright-firefox/sample-test.js /home/playwright/sample-test.js

ENV PLAYWRIGHT_BROWSERS_PATH=/home/playwright/.cache/ms-playwright

ENTRYPOINT ["/entrypoint.sh"]
