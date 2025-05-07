#!/bin/bash
set -e

# Default configuration
API_PORT=${API_PORT:-8080}
API_HOST=${API_HOST:-localhost}
DOCKER_COMPOSE_FILE=${DOCKER_COMPOSE_FILE:-../docker-compose.yaml}

# Color outputs
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo "==== Sandbox API Integration Tests ===="
echo "API Host: $API_HOST"
echo "API Port: $API_PORT"
echo "Docker Compose File: $DOCKER_COMPOSE_FILE"

# Check if we need to start the API
if [ "$START_API" = "true" ]; then
    echo -e "${GREEN}Starting API using Docker Compose...${NC}"
    docker-compose -f $DOCKER_COMPOSE_FILE up -d dev

    # Wait for API to be ready
    echo "Waiting for API to be ready..."
    max_retries=30
    retries=0
    while [ $retries -lt $max_retries ]; do
        if curl -s "http://$API_HOST:$API_PORT/health" > /dev/null; then
            echo -e "${GREEN}API is ready!${NC}"
            break
        fi
        retries=$((retries+1))
        echo "Waiting for API to be ready... ($retries/$max_retries)"
        sleep 1
    done

    if [ $retries -eq $max_retries ]; then
        echo -e "${RED}API did not become ready in time!${NC}"
        exit 1
    fi
fi

# Set environment variables for tests
export API_BASE_URL="http://$API_HOST:$API_PORT"

# Run tests
echo -e "${GREEN}Running integration tests...${NC}"
cd "$(dirname "$0")"
go test -v ./... ./tests/...

# Cleanup if we started the API
if [ "$START_API" = "true" ]; then
    echo -e "${GREEN}Stopping API...${NC}"
    docker-compose -f $DOCKER_COMPOSE_FILE down
fi

echo -e "${GREEN}Integration tests completed!${NC}"