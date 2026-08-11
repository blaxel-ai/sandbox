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
