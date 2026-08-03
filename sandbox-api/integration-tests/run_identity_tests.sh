#!/bin/bash
# Runs the integration suite against an API that supervises an unprivileged
# workload user: the API keeps root (mounts, tunnel, keep-alive) while
# everything it runs for the user is scoped to WORKLOAD_USER.
#
# Needs root, because that is the situation being tested. Everything it creates
# (the user, the binary, the root-owned probe file) is disposable.
set -euo pipefail

cd "$(dirname "$0")"

WORKLOAD_USER=${WORKLOAD_USER:-sbxtest}
API_PORT=${API_PORT:-8091}
API_BASE_URL="http://localhost:${API_PORT}"

if [ "$(id -u)" -ne 0 ]; then
    echo "This suite must run as root (the API under test keeps root and drops privileges itself)." >&2
    exit 1
fi

if ! id "$WORKLOAD_USER" >/dev/null 2>&1; then
    echo "Creating test user $WORKLOAD_USER"
    useradd --create-home --shell /bin/sh "$WORKLOAD_USER"
fi

BINARY=$(mktemp -d)/sandbox-api
echo "Building sandbox-api"
(cd ../ && go build -o "$BINARY" .)

# A root-owned secret the workload must not be able to read through the API.
mkdir -p /root
printf 'root only\n' > /root/.integration-identity-secret
chmod 600 /root/.integration-identity-secret

LOG=$(mktemp)
SANDBOX_LOG_DIR=${SANDBOX_LOG_DIR:-/tmp/sandbox-api-logs} \
    "$BINARY" --user "$WORKLOAD_USER" -port "$API_PORT" > "$LOG" 2>&1 &
API_PID=$!

cleanup() {
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
}
trap cleanup EXIT

for _ in $(seq 30); do
    if curl -sf "$API_BASE_URL/health" > /dev/null; then break; fi
    if ! kill -0 "$API_PID" 2>/dev/null; then
        echo "sandbox-api exited during startup:" >&2
        cat "$LOG" >&2
        exit 1
    fi
    sleep 1
done

echo "Running identity integration tests as workload user $WORKLOAD_USER"
API_BASE_URL="$API_BASE_URL" WORKLOAD_USER="$WORKLOAD_USER" go test -v ./tests/identity/...
