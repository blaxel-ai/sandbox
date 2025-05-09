# Sandbox API Integration Tests

This directory contains integration tests for the Sandbox API. These tests are designed to run against a live API instance, which can be running in Docker or elsewhere.

## Project Structure

The integration tests are organized as follows:

```
integration-tests/
├── common/                # Shared utilities and helpers
│   ├── config.go          # Configuration utilities
│   └── request.go         # HTTP request utilities
├── tests/                 # Test files organized by API feature
│   ├── filesystem/        # Tests for filesystem operations
│   ├── process/           # Tests for process operations
│   └── network/           # Tests for network operations
├── main_test.go           # Main test setup and health check tests
├── run_tests.sh           # Test runner script
├── go.mod                 # Go module definition
└── README.md              # This file
```

## Overview

The integration tests use Go's standard testing package to:

1. Connect to a running Sandbox API server
2. Test the API endpoints
3. Verify the correct functioning of the API

## Running the Tests

There are two ways to run the integration tests:

### 1. Against an already running API

If you already have the Sandbox API running (locally or in Docker), you can run the tests against it:

```bash
# Run from the project root directory
make integration-test

# Or directly
cd sandbox-api/integration-tests
./run_tests.sh
```

You can customize the API host and port by setting environment variables:

```bash
API_HOST=localhost API_PORT=8080 ./run_tests.sh
```

### 2. Starting the API automatically with Docker

The test script can also start the API using Docker Compose before running the tests:

```bash
# Run from the project root directory
make integration-test-with-docker

# Or directly
cd sandbox-api/integration-tests
START_API=true ./run_tests.sh
```

This will:
1. Start the API using Docker Compose
2. Wait for the API to be ready
3. Run the tests
4. Stop the API when done

You can customize the Docker Compose file:

```bash
DOCKER_COMPOSE_FILE=../custom-docker-compose.yaml START_API=true ./run_tests.sh
```

## Test Coverage

The integration tests cover:

1. **File System Operations**: Creating, reading, and deleting files
2. **Process Management**: Starting, monitoring, and stopping processes
3. **Network Operations**: Monitoring network ports used by processes

## Adding New Tests

To add new tests:

1. Identify which feature you're testing and use the appropriate directory in `tests/`
2. Create a new test file with the `_test.go` suffix
3. Use the common package utilities for making requests

Example test for a new endpoint:

```go
package tests

import (
	"net/http"
	"testing"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFeature(t *testing.T) {
	// Make a request to the API
	resp, err := common.MakeRequest(http.MethodGet, "/new-endpoint", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Check response status
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Parse and verify response
	var response map[string]interface{}
	err = common.ParseJSONResponse(resp, &response)
	require.NoError(t, err)

	// Add your assertions here
}
```

## Testing with Other Tools

In addition to these Go-based integration tests, you can also test the API using:

1. **Bruno**: The project includes Bruno API collections in the `.bruno` directory
2. **Postman**: You can import the OpenAPI specification
3. **Curl**: For manual testing of specific endpoints