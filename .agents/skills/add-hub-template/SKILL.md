---
name: add-hub-template
description: Add or update a Blaxel Sandbox Hub image/template under hub/. Use when adding a new hub image, sandbox image, runtime image, template.json, Dockerfile, hidden/internal image, or workflow-dispatch build option in the blaxel-ai/sandbox repository.
---

# Add a Sandbox Hub Image

Use this skill for new images under `hub/<name>/`, including visible templates and hidden/internal runtime images.

## Scope First

1. Check repository instructions at the relevant scope before editing.
2. Inspect nearby templates:
   - generic base: `hub/base-image`, `hub/node`, `hub/ts-app`, `hub/py-app`
   - entrypoint examples: `hub/expo`, `hub/vite`, `hub/nextjs`
   - hidden/internal examples: `hub/benchmark`, `hub/vibekit-*`
3. Check the current git status and preserve unrelated user changes.
4. Decide whether the image is user-facing or internal:
   - User-facing: add useful metadata and README mention.
   - Internal/platform-managed: set `"hidden": true` and avoid README marketing.

## Required Files

Create or update:

- `hub/<name>/Dockerfile`
- `hub/<name>/template.json`
- `.github/workflows/build.yaml`

Usually update for local testing:

- `docker-compose.yaml`

Only update `README.md` for visible user-facing templates. Do not list hidden/internal images as normal templates.

## Dockerfile Rules

Follow the existing multi-stage pattern:

```dockerfile
ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api

FROM <runtime-base>

RUN <install required runtime tools>

WORKDIR /blaxel

COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
RUN chmod +x /usr/local/bin/sandbox-api

EXPOSE 8080

ENV HOME=/blaxel

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
```

Rules:

- Always include the `ARG SANDBOX_VERSION=latest` stage unless the image has a specific reason not to.
- Copy `/sandbox-api` from the sandbox-api stage into `/usr/local/bin/sandbox-api`.
- Expose `8080` for `sandbox-api`.
- Only expose app/dev ports when they are intrinsic to that template. Do not expose dynamic app ports for platform-managed hidden images.
- If an entrypoint script is needed, keep `sandbox-api` running as the control plane and test the real startup path.
- Keep runtime dependencies explicit. If the runner supports `s3://`, `gs://`, or Azure Blob, install and test `rclone`.

## template.json Rules

Minimal shape:

```json
{
  "name": "<name>",
  "displayName": "<Display Name>",
  "categories": [],
  "description": "Short description.",
  "longDescription": "Longer description.",
  "url": "https://github.com/blaxel-ai/sandbox",
  "icon": "https://blaxel.ai/logo.png",
  "memory": 2048,
  "ports": [
    {
      "name": "sandbox-api",
      "target": 8080,
      "protocol": "HTTP"
    }
  ],
  "enterprise": false,
  "coming_soon": false
}
```

For hidden/internal images, add:

```json
"hidden": true
```

Port rule: put only stable ports in `template.json`. If another service creates previews with a caller-provided `runtime.port`, leave that dynamic port out of the template.

## GitHub Workflow

Update `.github/workflows/build.yaml` every time a new hub image is added:

- Add `<name>` under `on.workflow_dispatch.inputs.sandbox.options`.
- Keep alphabetical-ish order near existing options.

The automatic build matrix uses folders under `hub/`, so the directory is enough for push/tag auto-detection. The `workflow_dispatch` options list is separate and must be updated manually.

## docker-compose

Add a service when local build/run is useful:

```yaml
  <name>:
    platform: linux/amd64
    build:
      context: .
      dockerfile: hub/<name>/Dockerfile
    env_file:
      - .env
    ports:
      - "8080:8080"
      - "3010:3010"
```

Add app ports only when they are stable template ports. Avoid dynamic platform-managed app ports.

## Validation

Run the cheapest structural checks first:

```bash
jq empty hub/<name>/template.json
find hub/<name> -maxdepth 1 -name '*.sh' -print -exec sh -n {} \;
ruby -e 'require "yaml"; YAML.load_file(".github/workflows/build.yaml"); puts "yaml-ok"'
```

Build with an explicit platform:

```bash
docker build --platform linux/amd64 -t blaxel/<name>:test -f hub/<name>/Dockerfile .
```

Prefer explicit `docker build --platform linux/amd64` over `docker compose build` on Apple Silicon. Some local compose providers ignore the service platform when pulling `ghcr.io/blaxel-ai/sandbox:latest` and fail on `arm64`.

If the image has a runner or entrypoint script, smoke-test it inside the built image with realistic env vars and mounted test input. Verify:

- required binaries exist (`command -v <tool>`)
- source materialization works
- pre-start/setup hooks run in the intended order
- the final command starts or exits as expected

When feasible, start the sandbox API and check:

```bash
curl http://localhost:8080/health
curl -X POST http://localhost:8080/process \
  -H "Content-Type: application/json" \
  -d '{"command": "echo hello", "waitForCompletion": true}'
```

## Final Checklist

- [ ] `hub/<name>/Dockerfile` builds with `--platform linux/amd64`.
- [ ] `hub/<name>/template.json` is valid JSON.
- [ ] Hidden/internal images have `"hidden": true`.
- [ ] Dynamic app ports are not hard-coded in `Dockerfile`, `template.json`, or `docker-compose.yaml`.
- [ ] `.github/workflows/build.yaml` includes `<name>` in `workflow_dispatch` options.
- [ ] `docker-compose.yaml` is updated when local testing is useful.
- [ ] Runner or entrypoint scripts are syntax-checked and smoke-tested.
- [ ] User-facing visible templates are documented in `README.md`; hidden/internal ones are not.
- [ ] No public action, commit, push, PR, or deploy is performed without user confirmation.
