#!/bin/sh

# Start sandbox-api in the background
/usr/local/bin/sandbox-api &

# Start Playwright server in the foreground
exec /usr/local/bin/playwright run-server --port 8081 --host 0.0.0.0
