#!/bin/bash

# Start code-server in the background
echo "Starting code-server on port 8081..."
code-server --auth none --bind-addr 0.0.0.0:8081 --disable-telemetry &
CODE_SERVER_PID=$!
echo "code-server started with PID $CODE_SERVER_PID"

# Give code-server time to start
sleep 2

# Check if code-server is running
if kill -0 $CODE_SERVER_PID 2>/dev/null; then
    echo "code-server is running on port 8081"
else
    echo "Warning: code-server failed to start"
fi

# Start sandbox-api in the foreground
echo "Starting sandbox-api on port 8080..."
exec /usr/local/bin/sandbox-api