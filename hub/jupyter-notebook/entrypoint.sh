#!/bin/sh

# Start jupyter kernelgateway in the background
jupyter kernelgateway --KernelGatewayApp.ip=0.0.0.0 --KernelGatewayApp.port=8888 --KernelGatewayApp.allow_origin='*' &
JUPYTER_PID=$!

# Function to wait for port to be available
wait_for_port() {
  local port=$1
  local timeout=30
  local count=0

  echo "Waiting for port $port to be available..."

  while ! nc -z 0.0.0.0 $port; do
    sleep 1
    count=$((count + 1))
    if [ $count -gt $timeout ]; then
      echo "Timeout waiting for port $port"
      exit 1
    fi
  done

  echo "Port $port is now available"
}

# Wait a moment for jupyter to start
wait_for_port 8888

echo "Jupyter Kernel Gateway started successfully (PID: $JUPYTER_PID)"

# Start sandbox-api in the foreground
/usr/local/bin/sandbox-api

