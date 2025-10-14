#!/bin/bash

set -e

echo "==================================="
echo "MCP Integration Tests"
echo "==================================="
echo ""

# Configuration
SERVER_PORT=8080
SERVER_URL="http://localhost:${SERVER_PORT}/mcp"
SERVER_BINARY="../tmp/sandbox-api"
TEST_BINARY="./mcp.test"

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if server binary exists
if [ ! -f "$SERVER_BINARY" ]; then
    echo -e "${RED}Error: Server binary not found at $SERVER_BINARY${NC}"
    echo "Please build the server first with: cd ../sandbox-api && go build -o tmp/sandbox-api"
    exit 1
fi

# Build test binary if needed
if [ ! -f "$TEST_BINARY" ] || [ "$1" == "--rebuild" ]; then
    echo -e "${YELLOW}Building test binary...${NC}"
    go test -c ./tests/mcp -o mcp.test
    echo -e "${GREEN}✓ Test binary built${NC}"
    echo ""
fi

# Start the server
echo -e "${YELLOW}Starting MCP server on port ${SERVER_PORT}...${NC}"
$SERVER_BINARY -p $SERVER_PORT > /tmp/mcp_server.log 2>&1 &
SERVER_PID=$!
echo -e "${GREEN}✓ Server started with PID $SERVER_PID${NC}"

# Function to cleanup
cleanup() {
    echo ""
    echo -e "${YELLOW}Stopping server...${NC}"
    kill $SERVER_PID 2>/dev/null || true
    wait $SERVER_PID 2>/dev/null || true
    echo -e "${GREEN}✓ Server stopped${NC}"
}

# Register cleanup on exit
trap cleanup EXIT INT TERM

# Wait for server to be ready
echo -e "${YELLOW}Waiting for server to be ready...${NC}"
max_attempts=30
attempt=0

while [ $attempt -lt $max_attempts ]; do
    if curl -s "$SERVER_URL" > /dev/null 2>&1; then
        echo -e "${GREEN}✓ Server is ready${NC}"
        break
    fi
    attempt=$((attempt + 1))
    sleep 0.5
done

if [ $attempt -eq $max_attempts ]; then
    echo -e "${RED}Error: Server did not start in time${NC}"
    echo "Server log:"
    cat /tmp/mcp_server.log
    exit 1
fi

echo ""
echo "==================================="
echo "Running Integration Tests"
echo "==================================="
echo ""

# Set environment variable for tests
export SANDBOX_API_URL="$SERVER_URL"

# Run the tests
if [ -z "$2" ]; then
    # Run all tests
    ./mcp.test -test.v
else
    # Run specific test
    ./mcp.test -test.v -test.run "$2"
fi

TEST_EXIT_CODE=$?

echo ""
if [ $TEST_EXIT_CODE -eq 0 ]; then
    echo -e "${GREEN}==================================="
    echo -e "✓ All tests passed!"
    echo -e "===================================${NC}"
else
    echo -e "${RED}==================================="
    echo -e "✗ Some tests failed"
    echo -e "===================================${NC}"
    echo ""
    echo "Server log (last 50 lines):"
    tail -50 /tmp/mcp_server.log
fi

exit $TEST_EXIT_CODE

