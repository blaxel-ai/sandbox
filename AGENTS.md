# Repository Instructions

## Communication

- Do not push, open PRs, create releases, post comments, send messages, or run public/company-facing actions without explicit user approval.

## Repository Hygiene

- Do not read `.env` or `.env.*` files unless the user explicitly asks for that exact file.
- Prefer existing Make targets, helpers, and patterns before adding new tooling.
- Keep changes scoped to the affected subsystem and avoid unrelated refactors.

## Sandbox API

- For `sandbox-api` code changes, run unit tests from `sandbox-api/`. On local macOS, use `SANDBOX_LOG_DIR=./tmp/log go test -v ./...` if `/var/log/sandbox-api` is not writable.
- For every bugfix or new feature that changes runtime behavior, add or update an integration test under `sandbox-api/integration-tests/tests/` and run it against a live dev API when feasible.
- If handlers, routes, request/response shapes, or Swagger annotations change, regenerate the API reference with `make reference`.

## Hub Images

- For hub template changes, update the matching `hub/<name>` files, `docker-compose.yaml`, and any relevant workflow-dispatch options together.
- Validate changed hub images with a local build and a `/health` check when feasible.
