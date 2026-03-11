---
name: dev-environment
description: Start the local sandbox-api development environment with hot-reload. Use when developing or testing changes to the sandbox-api Go code locally.
---

# Start the Local Dev Environment

The sandbox-api runs inside Docker with Air for hot-reload. Changes to Go files in `sandbox-api/` are picked up automatically without restarting the container.

## Prerequisites

Install dev dependencies (one-time):

```bash
make dependencies
```

This installs:
- `air` — hot-reload for Go
- `swag` — OpenAPI doc generation
- `yq` — YAML processor (via brew)

---

## Start the Dev Server

**Important:** Before starting, kill any existing process on port 8080:
```bash
lsof -ti :8080 | xargs kill -9 2>/dev/null || true
```

```bash
docker-compose up dev
```

This starts the `dev` service defined in `docker-compose.yaml`, which:
- Builds from `docker/alpine.Dockerfile`
- Mounts `./sandbox-api` and `./tmp` as volumes
- Sets `SANDBOX_DEV_MODE=true`
- Exposes port `8080` (sandbox-api) and `3010`
- Runs `air` for hot-reload

Wait for this log line before testing:
```
Starting Sandbox API server on :8080
```

---

## Verify It's Running

```bash
curl http://localhost:8080/health
```

Or test the filesystem endpoint:

```bash
curl http://localhost:8080/filesystem/~
```

---

## Alternative: Run Without Docker (Faster Iteration)

If you want to skip Docker and run air directly on the host:

```bash
lsof -ti :8080 | xargs kill -9 2>/dev/null || true
make api
```

This runs `air` with `SANDBOX_LOG_DIR=./tmp/log` from the `sandbox-api/` directory. Requires Go installed locally.

---

## Slim Dev Image (No Source Mount)

For testing the production-like image without hot-reload:

```bash
docker-compose up dev-slim
```

This builds from `docker/slim.Dockerfile` — no volume mounts, no hot-reload.

---

## Run Unit Tests

```bash
make test
```

Runs `go test -v ./...` from within `sandbox-api/`. These are fast in-process tests.

---

## Build the Docker Image

```bash
make docker-build
```

Builds `blaxel/sandbox-api:latest`. To run it:

```bash
make docker-run
```

---

## Logs

Dev logs are written to `./tmp/log/` on the host (mounted from the container). Tail them with:

```bash
tail -f tmp/log/*.log
```
