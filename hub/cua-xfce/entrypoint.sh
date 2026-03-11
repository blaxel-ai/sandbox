#!/bin/bash
set -e

# Create runtime directories
mkdir -p /run/user/0
chmod 700 /run/user/0

# Create supervisor log directory
mkdir -p /var/log/supervisor

# Fix hostname resolution (required by TigerVNC's vncserver script).
# In Unikraft/Blaxel the hostname is often "(none)" or empty and /etc/hosts
# may be missing entries, causing "Could not acquire fully qualified host name".
CURRENT_HOSTNAME=$(hostname 2>/dev/null || echo "localhost")
if [ "$CURRENT_HOSTNAME" = "(none)" ] || [ -z "$CURRENT_HOSTNAME" ]; then
    CURRENT_HOSTNAME="localhost"
    hostname "$CURRENT_HOSTNAME" 2>/dev/null || true
fi
# Ensure /etc/hosts has the necessary entries
if ! grep -q "127.0.0.1" /etc/hosts 2>/dev/null; then
    echo "127.0.0.1 localhost $CURRENT_HOSTNAME" >> /etc/hosts
fi
if ! grep -q "$CURRENT_HOSTNAME" /etc/hosts 2>/dev/null; then
    echo "127.0.0.1 $CURRENT_HOSTNAME" >> /etc/hosts
fi

# Ensure cua user home directory is properly set up
chown -R cua:cua /home/cua 2>/dev/null || true

echo "Starting cua-xfce desktop sandbox..."

# Start supervisord (manages VNC, noVNC, computer-server, and sandbox-api)
exec /usr/bin/supervisord -c /etc/supervisor/supervisord.conf
