---
name: add-hub-template
description: Create a new sandbox hub template (a pre-configured micro VM environment). Use when adding support for a new language, framework, or toolchain as a reusable sandbox image.
---

# Add a New Hub Template

Hub templates are pre-built sandbox images stored in `hub/<template-name>/`. Each template is a Docker image with a `sandbox-api` binary pre-installed and a configured entrypoint.

## Step 1: Create the Template Directory

```bash
mkdir hub/<template-name>
```

Convention: use lowercase kebab-case (e.g., `ruby-app`, `go-app`, `playwright-firefox`).

---

## Step 2: Write the Dockerfile

Create `hub/<template-name>/Dockerfile`. The Dockerfile must:
1. Start from a base image with the required runtime
2. Copy in the `sandbox-api` binary (or build it from source)
3. Expose port `8080` for the sandbox-api
4. Set an entrypoint that starts `sandbox-api`

Minimal example (adapts the base-image pattern):

```dockerfile
FROM ubuntu:22.04

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    curl \
    <your-tools> \
    && rm -rf /var/lib/apt/lists/*

# Copy sandbox-api binary (built by the multi-stage or pre-built)
COPY sandbox-api/sandbox-api /usr/local/bin/sandbox-api
RUN chmod +x /usr/local/bin/sandbox-api

# Working directory for user code
WORKDIR /blaxel/app

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
```

Look at existing templates for reference patterns:
- `hub/base-image/Dockerfile` — minimal baseline
- `hub/py-app/Dockerfile` — Python with pip
- `hub/ts-app/Dockerfile` — Node.js/TypeScript
- `hub/expo/Dockerfile` — Expo with entrypoint script that auto-starts a dev server

If your template needs to auto-start a service on boot, create an `entrypoint.sh` script and call it before `sandbox-api` (see `hub/expo/entrypoint.sh` for an example using `curl` to POST to `/process`).

---

## Step 3: Write template.json

Create `hub/<template-name>/template.json`:

```json
{
  "name": "<template-name>",
  "displayName": "<Human Readable Name>",
  "categories": [],
  "description": "Short one-line description",
  "longDescription": "Longer description explaining use cases and what's included.",
  "url": "https://link-to-upstream-project",
  "icon": "https://url-to-icon-image.png",
  "memory": 4096,
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

- `memory`: RAM in MB (`4096` = 4GB is standard; increase for heavy workloads)
- `ports`: List all ports the template exposes. Add extra entries for app ports (e.g., 3000 for a dev server)
- `enterprise`: Set `true` to restrict to enterprise workspaces
- `coming_soon`: Set `true` to show the template in the UI but disable creation

---

## Step 4: Add to docker-compose.yaml

Add a service entry so you can test locally:

```yaml
  <template-name>:
    platform: linux/amd64
    build:
      context: .
      dockerfile: hub/<template-name>/Dockerfile
    env_file:
      - .env
    ports:
      - "8080:8080"
      - "<app-port>:<app-port>"  # add any extra ports
      - "3010:3010"
```

---

## Step 5: Build and Test Locally

```bash
# Build the image
docker-compose build <template-name>

# Start it
docker-compose up <template-name>

# Verify sandbox-api is running
curl http://localhost:8080/health

# Run a process inside it
curl -X POST http://localhost:8080/process \
  -H "Content-Type: application/json" \
  -d '{"command": "echo hello", "waitForCompletion": true}'
```

---

## Step 6: Run Integration Tests Against It

```bash
cd sandbox-api/integration-tests
API_HOST=localhost API_PORT=8080 ./run_tests.sh
```

---

## Checklist Before PR

- [ ] `hub/<template-name>/Dockerfile` builds successfully
- [ ] `hub/<template-name>/template.json` is valid JSON with all required fields
- [ ] Service added to `docker-compose.yaml`
- [ ] `curl http://localhost:8080/health` returns `200 OK`
- [ ] Integration tests pass against the new image
- [ ] Template listed in the root `README.md` under the Templates section
