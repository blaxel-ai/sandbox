ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM trycua/cua-xfce:0.1.9

USER root

# Copy sandbox-api binary from Blaxel
COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api

# Create D-Bus directory and fake socket to prevent connection errors (needed for Unikraft)
RUN mkdir -p /var/run/dbus && \
    touch /var/run/dbus/system_bus_socket && \
    chmod 666 /var/run/dbus/system_bus_socket

# Replace supervisor config to remove the CUA computer-server and add sandbox-api
COPY hub/xfce-vnc/supervisord.conf /etc/supervisor/conf.d/supervisord.conf
COPY hub/xfce-vnc/sandbox-api.conf /etc/supervisor/conf.d/sandbox-api.conf

# Copy custom entrypoint
COPY hub/xfce-vnc/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Expose ports:
# - 8080: sandbox-api (Blaxel)
# - 5901: TigerVNC server
# - 6901: noVNC HTML5 client
EXPOSE 8080 5901 6901

ENTRYPOINT ["/entrypoint.sh"]
