---
name: run-integration-tests
description: Run the sandbox-api integration tests against a live API instance. Use after making changes to sandbox-api to verify all endpoints still work correctly.
---

# Run Integration Tests

Integration tests live in `sandbox-api/integration-tests/` and test real HTTP endpoints against a running sandbox-api instance.

## Quick Start (Recommended)

Run against an already-running dev environment:

```bash
# 1. Start the dev environment first (in another terminal)
docker-compose up dev

# 2. Run integration tests
make integration-test
```

---

## Run With Docker Auto-Start

Let the test script start and stop Docker automatically:

```bash
cd sandbox-api/integration-tests
START_API=true ./run_tests.sh
```

Or with a custom compose file:

```bash
DOCKER_COMPOSE_FILE=../docker-compose.yaml START_API=true ./run_tests.sh
```

---

## Run Against a Remote/Custom Host

```bash
cd sandbox-api/integration-tests
API_HOST=<hostname> API_PORT=8080 ./run_tests.sh
```

---

## Run a Specific Test File or Test

```bash
cd sandbox-api/integration-tests
API_BASE_URL=http://localhost:8080 go test -v ./tests/filesystem/...
API_BASE_URL=http://localhost:8080 go test -v ./tests/process/...
API_BASE_URL=http://localhost:8080 go test -v ./tests/network/...
API_BASE_URL=http://localhost:8080 go test -v ./tests/mcp/...
API_BASE_URL=http://localhost:8080 go test -v ./tests/codegen/...

# Run a single test by name
API_BASE_URL=http://localhost:8080 go test -v -run TestFilesystemRead ./tests/filesystem/...
```

---

## Test Coverage Areas

| Directory | What It Tests |
|-----------|--------------|
| `tests/filesystem/` | File CRUD, directory listing, tree ops, multipart upload |
| `tests/process/` | Process execute, logs, stop/kill, shell wrapper |
| `tests/network/` | Port monitoring, tunnel config |
| `tests/mcp/` | MCP tool registration and invocation |
| `tests/codegen/` | File search, grep search, rerank, edit file |

---

## Adding a New Integration Test

1. Pick the matching directory in `tests/` (or create one)
2. Create a `*_test.go` file with `package tests`
3. Use shared helpers from `common/`:

```go
package tests

import (
    "net/http"
    "testing"

    "github.com/blaxel-ai/sandbox-api/integration_tests/common"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestMyNewEndpoint(t *testing.T) {
    resp, err := common.MakeRequest(http.MethodGet, "/my-endpoint", nil)
    require.NoError(t, err)
    defer resp.Body.Close()

    assert.Equal(t, http.StatusOK, resp.StatusCode)

    var result map[string]interface{}
    err = common.ParseJSONResponse(resp, &result)
    require.NoError(t, err)
}
```

---

## Troubleshooting

- **`connection refused`**: The sandbox-api is not running. Start it with `docker-compose up dev` first
- **Test hangs**: A previous test left a process running; restart the dev container
- **Codegen tests skip**: `RELACE_API_KEY` or `MORPH_API_KEY` env vars not set — codegen tools require an LLM provider key
