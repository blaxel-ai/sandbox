#!/bin/sh

/usr/local/bin/sandbox-api &
SANDBOX_API_PID=$!

lightpanda serve --host 0.0.0.0 --port 8081 &
LIGHTPANDA_PID=$!

trap 'kill $SANDBOX_API_PID $LIGHTPANDA_PID 2>/dev/null' EXIT
while kill -0 $SANDBOX_API_PID 2>/dev/null && kill -0 $LIGHTPANDA_PID 2>/dev/null; do
    sleep 1
done
exit 1
