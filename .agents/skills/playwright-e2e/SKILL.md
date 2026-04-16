---
name: playwright-e2e
description: Deploy a Playwright sandbox (chromium or firefox) on Blaxel and run e2e tests against it. Use to validate that playwright hub images work end-to-end (browser connection, page navigation, DOM interaction) after changes.
---

# Playwright E2E Test

Deploy a Playwright sandbox to Blaxel dev, wait for it to be ready, connect via Playwright, and run browser tests.

Supported sandbox templates:
- `playwright-chromium` -- Chromium browser, connect via `chromium.connect()`
- `playwright-firefox` -- Firefox browser, connect via `firefox.connect()`

Both expose the same Playwright server on port 8081 with the same protocol. The only difference is the browser type used to connect.

## Prerequisites

- `bl` CLI authenticated on the target workspace (`bl login`)
- `node` available locally
- `playwright` npm package installed locally (matching the server version)

## Step 1: Pick the sandbox template

Determine which browser to test. Use `SANDBOX_NAME=playwright-chromium` or `SANDBOX_NAME=playwright-firefox`. If the user doesn't specify, default to `playwright-chromium`. If both need testing, run the steps below once per browser.

## Step 2: Build and push the image

Use `bl push` to build and push the image without creating the sandbox resource:

```bash
bl push -y -t sandbox -d hub/$SANDBOX_NAME -n $SANDBOX_NAME
```

This builds the image from `hub/$SANDBOX_NAME/Dockerfile` and pushes it to the Blaxel registry.

**Note**: The CLI may report `build timed out after 15 minutes` for large images (~1.9 GB). If the logs show `✅ Build completed successfully` before the timeout, the image was pushed successfully and you can proceed. The timeout is a CLI-side log monitoring limit, not a server-side failure.

## Step 3: Create the sandbox with port config

Use `bl apply` to create the sandbox resource with the correct port declarations. Port 8081 (Playwright server) must be declared, otherwise only port 8080 (sandbox-api) is exposed and Playwright connections fail with 502.

```bash
bl apply -f - <<EOF
apiVersion: blaxel.ai/v1alpha1
kind: Sandbox
metadata:
  name: $SANDBOX_NAME
spec:
  runtime:
    image: sandbox/$SANDBOX_NAME:latest
    memory: 4096
    ports:
      - name: sandbox-api
        target: 8080
        protocol: HTTP
      - name: playwright
        target: 8081
        protocol: HTTP
EOF
```

## Step 4: Wait for the sandbox to be ready

Poll until the sandbox status is `DEPLOYED`. Note: `bl get sbx` returns a JSON **array**, so access `[0]`.

```bash
while true; do
  STATUS=$(bl get sbx $SANDBOX_NAME -ojson 2>/dev/null | jq -r '.[0].status')
  echo "Sandbox status: $STATUS"
  if [ "$STATUS" = "DEPLOYED" ]; then
    break
  fi
  sleep 5
done
```

## Step 5: Retrieve the sandbox URL and wait for Playwright server

```bash
SANDBOX_URL=$(bl get sbx $SANDBOX_NAME -ojson | jq -r '.[0].metadata.url')
```

The `DEPLOYED` status means the image is pushed, but the VM may still be cold-starting. Poll the Playwright server until it responds:

```bash
TOKEN=$(bl token)
while true; do
  HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" "$SANDBOX_URL/port/8081/json" 2>/dev/null)
  echo "Playwright server HTTP status: $HTTP_STATUS"
  if [ "$HTTP_STATUS" = "200" ]; then
    break
  fi
  sleep 10
done
```

## Step 6: Run the Playwright test

Connect with the token as a `?token=` query parameter on the WebSocket URL.

Use the browser type matching the sandbox:
- `playwright-chromium` --> `chromium.connect()`
- `playwright-firefox` --> `firefox.connect()`

Write a Node.js script that:

1. Connects to `wss://{SANDBOX_HOST}/port/8081/?token={TOKEN}` using the appropriate browser type
2. Creates a new page
3. Navigates to a test URL (e.g. `https://example.com`)
4. Asserts the page loaded correctly (title, DOM elements)
5. Closes the browser

### Chromium example

```javascript
const { chromium } = require('playwright');

const browser = await chromium.connect(
  `wss://${SANDBOX_HOST}/port/8081/?token=${TOKEN}`,
  { timeout: 15000 }
);

const page = await browser.newPage();
await page.goto('https://example.com');
const title = await page.title();
assert(title.includes('Example Domain'));

await page.close();
await browser.close();
```

### Firefox example

```javascript
const { firefox } = require('playwright');

const browser = await firefox.connect(
  `wss://${SANDBOX_HOST}/port/8081/?token=${TOKEN}`,
  { timeout: 15000 }
);

const page = await browser.newPage();
await page.goto('https://example.com');
const title = await page.title();
assert(title.includes('Example Domain'));

await page.close();
await browser.close();
```

## Important notes

- **Version match**: The local `playwright` package version MUST match the server version. The sandbox runs Playwright v1.58. If you get a `428 Precondition Required` with a version mismatch error, install the correct version: `npm install playwright@1.58`.
- **Connection method**: Use `browserType.connect()`, NOT `connectOverCDP()`. The sandbox runs a Playwright server, not a raw CDP endpoint.
- **Browser type**: Must match the sandbox template. `chromium.connect()` for `playwright-chromium`, `firefox.connect()` for `playwright-firefox`. Using the wrong browser type will fail.
- **Auth**: Pass the JWT token as `?token=` query parameter on the WebSocket URL.
- **Ports**: Port 8080 is the sandbox-api, port 8081 is the Playwright server. Both must be declared in the `bl apply` spec.

## Step 7: Cleanup

After tests pass, optionally delete the sandbox:

```bash
bl delete sbx $SANDBOX_NAME
```

## Troubleshooting

- **401 Unauthorized**: Token expired or wrong workspace. Run `bl token` to refresh.
- **428 Precondition Required**: Playwright version mismatch between client and server. Check the error message for the server version and install the matching client.
- **502 Bad Gateway on port 8081**: Either the Playwright server hasn't started yet (wait longer), or port 8081 was not declared in the `bl apply` spec. Check with `bl get sbx $SANDBOX_NAME -ojson | jq '.[0].spec.runtime.ports'`.
- **Sandbox stuck in DEPLOYING**: Check `bl logs sbx $SANDBOX_NAME` for build errors.
