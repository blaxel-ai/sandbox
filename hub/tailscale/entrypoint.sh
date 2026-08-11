#!/bin/sh
set -eu

if [ -z "${TS_AUTHKEY:-}" ]; then
  echo "TS_AUTHKEY is required to connect this sandbox to Tailscale" >&2
  exit 1
fi

export TS_HOSTNAME="${TS_HOSTNAME:-${SANDBOX_NAME:-${BL_NAME:-tailscale-sandbox}}}"

/usr/local/bin/sandbox-api &
exec /usr/local/bin/containerboot
