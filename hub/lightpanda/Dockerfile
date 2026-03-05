ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM lightpanda/browser:latest

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

COPY hub/lightpanda/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Sample test script to verify LightPanda is working
# Usage: npx puppeteer-core is needed, or install globally first
COPY hub/lightpanda/sample-test.js /home/sample-test.js

EXPOSE 8080 8081

ENTRYPOINT ["/entrypoint.sh"]
