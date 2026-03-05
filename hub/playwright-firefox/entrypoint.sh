#!/bin/sh

# Start Playwright server in the background
/usr/local/bin/playwright run-server --port 8081 --host 0.0.0.0 &
PLAYWRIGHT_PID=$!

# Start sandbox-api in the foreground
/usr/local/bin/sandbox-api
