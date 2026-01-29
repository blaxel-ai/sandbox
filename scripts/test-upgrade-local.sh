#!/bin/bash
set -e

# This script sets up a local test environment for testing sandbox-api upgrades
# without needing to release to GitHub.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
DIST_DIR="$PROJECT_DIR/dist"
SANDBOX_API_DIR="$PROJECT_DIR/sandbox-api"

# Clean and create dist directory
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR/download/develop"

echo "==> Building sandbox-api binaries..."

cd "$SANDBOX_API_DIR"

VERSION="develop"
GIT_COMMIT=$(git rev-parse HEAD 2>/dev/null || echo "local")
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS="-s -w \
  -X github.com/blaxel-ai/sandbox-api/src/handler.Version=${VERSION} \
  -X github.com/blaxel-ai/sandbox-api/src/handler.GitCommit=${GIT_COMMIT} \
  -X github.com/blaxel-ai/sandbox-api/src/handler.BuildTime=${BUILD_TIME}"

# Build for linux/amd64
echo "Building linux/amd64..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$DIST_DIR/download/develop/sandbox-api-linux-amd64" .

# Build for linux/arm64
echo "Building linux/arm64..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$LDFLAGS" -o "$DIST_DIR/download/develop/sandbox-api-linux-arm64" .

echo "==> Binaries built in $DIST_DIR/download/develop/"
ls -la "$DIST_DIR/download/develop/"

echo ""
echo "==> Starting HTTP server on port 9999..."
echo "    Binaries available at:"
echo "    - http://host.docker.internal:9999/download/develop/sandbox-api-linux-amd64"
echo "    - http://host.docker.internal:9999/download/develop/sandbox-api-linux-arm64"
echo ""
echo "    To test upgrade, use:"
echo '    curl -X POST http://localhost:8080/upgrade -H "Content-Type: application/json" -d '"'"'{"version": "develop", "baseUrl": "http://host.docker.internal:9999"}'"'"
echo ""
echo "Press Ctrl+C to stop the server"

cd "$DIST_DIR"
python3 -m http.server 9999
