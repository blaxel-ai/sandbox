#!/bin/sh

# Normalize ComputeSDK credential env vars BEFORE starting any processes.
# The compute binary reads API_KEY / ACCESS_TOKEN directly, but users may
# pass COMPUTESDK_API_KEY / COMPUTESDK_ACCESS_TOKEN instead.
export API_KEY="${API_KEY:-${COMPUTESDK_API_KEY:-}}"
export ACCESS_TOKEN="${ACCESS_TOKEN:-${COMPUTESDK_ACCESS_TOKEN:-}}"

# Start the Blaxel sandbox API (required)
# It inherits the exported env vars, so child processes it spawns will too.
/usr/local/bin/sandbox-api &

# Wait for sandbox API to be ready
echo "Waiting for sandbox API..."
while ! nc -z 127.0.0.1 8080; do
  sleep 0.1
done

echo "Sandbox API ready"

# Start the ComputeSDK compute daemon via the sandbox API process manager.
if [ -n "$ACCESS_TOKEN" ] || [ -n "$API_KEY" ]; then
  echo "Starting ComputeSDK compute daemon (credentials found)..."
  curl -s http://127.0.0.1:8080/process \
    -X POST \
    -H "Content-Type: application/json" \
    -d '{"name": "compute-daemon", "workingDir": "/app", "command": "/app/compute serve-daemon", "waitForCompletion": false}'
else
  echo "WARNING: No COMPUTESDK_ACCESS_TOKEN or COMPUTESDK_API_KEY set."
  echo "The compute daemon will NOT start automatically."
  echo "Provide credentials at sandbox creation time, or start manually via:"
  echo "  /app/compute serve-daemon --access-token <TOKEN>"
fi

echo "ComputeSDK sandbox ready"

# Keep the container running
wait
