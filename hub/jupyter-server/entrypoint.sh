#!/bin/sh


# Start sandbox-api in the foreground
/usr/local/bin/sandbox-api &

# Start jupyter kernelgateway in the background
jupyter server --no-browser --KernelGatewayApp.ip=0.0.0.0 --ip=0.0.0.0 --port=8888 --ServerApp.token='' --ServerApp.allow_origin='*' --allow-root &

wait $!
