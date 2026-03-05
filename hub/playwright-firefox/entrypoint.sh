#!/bin/sh

/usr/local/bin/sandbox-api &
SANDBOX_API_PID=$!

/usr/local/bin/playwright run-server --port 8081 --host 0.0.0.0 &
PLAYWRIGHT_PID=$!

trap 'kill $SANDBOX_API_PID $PLAYWRIGHT_PID 2>/dev/null' EXIT
while kill -0 $SANDBOX_API_PID 2>/dev/null && kill -0 $PLAYWRIGHT_PID 2>/dev/null; do
    sleep 1
done
exit 1
