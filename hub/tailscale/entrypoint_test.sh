#!/bin/sh
set -eu

entrypoint=$(CDPATH= cd -- "$(dirname "$0")" && pwd)/entrypoint.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if output=$(env -u TS_AUTHKEY sh "$entrypoint" 2>&1); then
  echo "entrypoint accepted a missing TS_AUTHKEY" >&2
  exit 1
fi
case "$output" in
  *"TS_AUTHKEY is required"*) ;;
  *) echo "entrypoint did not explain the missing TS_AUTHKEY" >&2; exit 1 ;;
esac

cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
sleep 1
EOF
cat >"$tmp/tailscale" <<'EOF'
#!/bin/sh
printf '%s\n' '{"BackendState": "Running"}'
EOF
cat >"$tmp/sandbox-api" <<'EOF'
#!/bin/sh
test "$TS_HOSTNAME" = "test-sandbox"
EOF
chmod +x "$tmp/containerboot" "$tmp/tailscale" "$tmp/sandbox-api"

PATH="$tmp:$PATH" TS_AUTHKEY=test BL_NAME=test-sandbox sh "$entrypoint"

cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
exit 1
EOF
cat >"$tmp/tailscale" <<'EOF'
#!/bin/sh
printf '%s\n' '{"BackendState": "NeedsLogin"}'
EOF
if output=$(PATH="$tmp:$PATH" TS_AUTHKEY=test sh "$entrypoint" 2>&1); then
  echo "entrypoint ignored containerboot failure" >&2
  exit 1
fi
case "$output" in
  *"Tailscale stopped before connecting"*) ;;
  *) echo "entrypoint did not explain containerboot failure" >&2; exit 1 ;;
esac

cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
sleep 5
EOF
if output=$(PATH="$tmp:$PATH" TS_AUTHKEY=test TS_STARTUP_TIMEOUT_SECONDS=0 sh "$entrypoint" 2>&1); then
  echo "entrypoint ignored the startup timeout" >&2
  exit 1
fi
case "$output" in
  *"Tailscale did not connect within 0 seconds"*) ;;
  *) echo "entrypoint did not explain the startup timeout" >&2; exit 1 ;;
esac

cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
sleep 1
exit 1
EOF
cat >"$tmp/tailscale" <<'EOF'
#!/bin/sh
printf '%s\n' '{"BackendState": "Running"}'
EOF
cat >"$tmp/sandbox-api" <<'EOF'
#!/bin/sh
sleep 5
EOF
if PATH="$tmp:$PATH" TS_AUTHKEY=test sh "$entrypoint"; then
  echo "entrypoint ignored post-readiness Tailscale failure" >&2
  exit 1
fi
