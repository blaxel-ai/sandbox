#!/bin/bash

# Start sandbox-api in the background
echo "Starting sandbox-api on port 8080..."
/usr/local/bin/sandbox-api &

# Wait for sandbox-api to be ready
while ! curl -s http://127.0.0.1:8080/health > /dev/null 2>&1; do
    sleep 0.1
done
echo "Sandbox API ready"

# Write code-server config to bind on port 8081 (CLI args don't override the config file)
mkdir -p /root/.config/code-server
cat > /root/.config/code-server/config.yaml << 'CONF'
bind-addr: 0.0.0.0:8081
auth: none
cert: false
trusted-origins:
  - "*"
CONF

# Start code-server via the sandbox API
echo "Starting code-server on port 8081 via sandbox API..."
curl -s http://127.0.0.1:8080/process -X POST \
    -H "Content-Type: application/json" \
    -d '{"name":"code-server","command":"code-server --disable-telemetry","workingDir":"/home/user","waitForCompletion":false, "env": {"PORT": "8081"}}'

echo "code-server started via sandbox API"

# Keep the entrypoint alive
wait