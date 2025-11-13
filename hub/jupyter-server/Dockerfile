ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

# Use an official Python runtime as a parent image
FROM python:3.12-slim

RUN apt-get update && apt-get install -y git curl netcat-openbsd fonts-noto-cjk \
  && rm -rf /var/lib/apt/lists/*

# Install UV
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

WORKDIR /app

# Set build-time environment variables (needed during package installation)
ENV PIP_DEFAULT_TIMEOUT=100
ENV PIP_DISABLE_PIP_VERSION_CHECK=1
ENV PIP_NO_CACHE_DIR=1

# Create necessary directories
RUN mkdir -p /app/.jupyter /app/.ipython/profile_default/startup /app/.config/matplotlib

# Copy and install Python packages using UV
COPY hub/jupyter-server/requirements.txt /app/requirements.txt
RUN uv pip install --system -r /app/requirements.txt

# Copy and install server dependencies
COPY hub/jupyter-server/server/requirements.txt /app/server/requirements.txt
RUN uv pip install --system -r /app/server/requirements.txt

# Install IPython kernel
RUN ipython kernel install --name 'python3' --user

# Copy configuration files
COPY hub/jupyter-server/jupyter_server_config.py /app/.jupyter/jupyter_server_config.py
COPY hub/jupyter-server/ipython_kernel_config.py /app/.ipython/profile_default/ipython_kernel_config.py
COPY hub/jupyter-server/matplotlibrc /app/.config/matplotlib/.matplotlibrc

# Copy server code
COPY hub/jupyter-server/server /app/server

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 8888 49999

COPY hub/jupyter-server/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]