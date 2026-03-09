---
name: run-e2e
description: Run the full end-to-end test suite against a custom sandbox image deployed locally or on Blaxel. Use before merging significant changes to validate the complete sandbox-api binary, not just unit/integration tests.
---

# Run End-to-End Tests

E2E tests build a real Docker image from the current source, deploy it, and run the test suite against it. This validates the full binary, not just the in-process unit tests.

## Architecture

- **Custom sandbox**: `e2e/custom-sandbox/` — a Docker image that embeds the full `sandbox-api` source and builds it at image-build time
- **Simple custom sandbox**: `e2e/simple-custom-sandbox/` — a leaner image that takes a pre-compiled binary
- **Test scripts**: `e2e/scripts/` — Node.js tests run with `npm run test:local`

---

## Option 1: Full Local E2E (Build + Run + Test)

```bash
make e2e
```

This single command:
1. Removes any existing `sandbox-dev` container
2. Builds the custom sandbox image (`make build-custom-sandbox`)
3. Starts the container (`make run-custom-sandbox`) on ports 8080-8083
4. Waits 5 seconds for startup
5. Logs open file descriptors (leak detection)
6. Runs the e2e test suite (`make test-custom-sandbox`)
7. Logs file descriptors again after tests

> **Note:** Watches for file descriptor leaks — the count before and after tests should not grow significantly.

---

## Option 2: Step-by-Step

### Build the Custom Sandbox Image

```bash
make build-custom-sandbox
```

Copies `sandbox-api/` into `e2e/custom-sandbox/` and builds `custom-sandbox:latest`.

### Run It Locally

```bash
make run-custom-sandbox
```

Starts `custom-sandbox:latest` as `sandbox-dev` container with ports `8080-8083` exposed.

### Run Tests Against It

```bash
make test-custom-sandbox
```

Runs `e2e/scripts/npm run test:local` against the running container.

---

## Option 3: Deploy to Blaxel and Test

Deploy the custom sandbox to the Blaxel platform, wait for it to be `DEPLOYED`, then run tests:

```bash
make deploy-custom-sandbox
```

Then wait and test:

```bash
# This waits until status=DEPLOYED, then runs tests
# (embedded in make test-custom-sandbox)
make test-custom-sandbox
```

---

## Option 4: Simple Custom Sandbox (Pre-Compiled Binary)

Compile for Linux first, then deploy:

```bash
make deploy-simple-custom-sandbox
```

This:
1. Cross-compiles `sandbox-api` for `linux/amd64`
2. Copies the binary to `e2e/simple-custom-sandbox/`
3. Deploys to Blaxel with `bl deploy`
4. Cleans up the binary

Useful for faster iteration when you don't need to rebuild the full Docker image.

---

## Troubleshooting

- **Build fails**: Check `sandbox-api/` compiles cleanly first with `make test`
- **Container exits immediately**: Run `docker logs sandbox-dev` to see the error
- **Tests fail with connection refused**: Container may still be starting; increase the sleep or poll `/health`
- **File descriptor count grows**: There is a resource leak in the code path being tested — investigate with `lsof -p <pid>` inside the container
- **`bl` command not found**: Install the Blaxel CLI (`brew install blaxel-ai/tap/bl` or equivalent)
