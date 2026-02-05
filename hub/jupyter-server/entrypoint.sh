#!/bin/sh

# Set runtime environment variables
export HOME=/app
export JUPYTER_CONFIG_PATH=/app/.jupyter
export IPYTHON_CONFIG_PATH=/app/.ipython
export MATPLOTLIBRC=/app/.config/matplotlib/.matplotlibrc

# Start jupyter server with configuration file
# The configuration file handles most settings, but we still need to specify some command-line args
MATPLOTLIBRC=/app/.config/matplotlib/.matplotlibrc jupyter server \
  --no-browser \
  --ip=0.0.0.0 \
  --port=12345 \
  --ServerApp.token='' \
  --ServerApp.password='' \
  --IdentityProvider.token='' \
  --allow-root \
  --ServerApp.root_dir=/app &

# Start the custom FastAPI server that provides /execute endpoint first
# It will retry connecting to Jupyter internally
cd /app/server
python -m uvicorn main:app --host 0.0.0.0 --port 8888 --workers 1 --no-access-log --no-use-colors --timeout-keep-alive 640 &

# Start sandbox-api in the background
/usr/local/bin/sandbox-api &

wait $!
