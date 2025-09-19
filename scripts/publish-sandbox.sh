#!/bin/bash

# Exit on any error and print commands as they are executed
set -e
set -x

# Validate required environment variables
if [ -z "$SANDBOX_NAME" ]; then
    echo "Error: SANDBOX_NAME environment variable is required"
    exit 1
fi

if [ -z "$BL_ENV" ]; then
    echo "Error: BL_ENV environment variable is required"
    exit 1
fi

if [ -z "$BL_API_URL" ]; then
    echo "Error: BL_API_URL environment variable is required"
    exit 1
fi

if [ -z "$BL_ADMIN_USERNAME" ]; then
    echo "Error: BL_ADMIN_USERNAME environment variable is required"
    exit 1
fi

if [ -z "$BL_ADMIN_PASSWORD" ]; then
    echo "Error: BL_ADMIN_PASSWORD environment variable is required"
    exit 1
fi

if [ -z "$TAG" ]; then
    echo "Error: TAG environment variable is required"
    exit 1
fi

echo "Publishing sandbox: $SANDBOX_NAME"
echo "Environment: $BL_ENV"
echo "Tag: $TAG"

# Create template image name in the format sandbox:latest
TEMPLATE_IMAGE="$SANDBOX_NAME:latest"

mkdir -p tmp/$SANDBOX_NAME

# Read and update the JSON file
if [ -f "hub/$SANDBOX_NAME/template.json" ]; then
    echo "Updating hub/$SANDBOX_NAME/template.json with image information"
    jq --arg img "$TEMPLATE_IMAGE" '. + {"image": $img}' "hub/$SANDBOX_NAME/template.json" > "tmp/$SANDBOX_NAME/template.json.tmp"
else
    echo "Warning: hub/$SANDBOX_NAME/template.json not found"
    exit 1
fi

echo "Making API call to update sandbox..."
if ! curl -X PUT -H "Content-Type: application/json" \
    -d @tmp/$SANDBOX_NAME/template.json.tmp \
    $BL_API_URL/admin/store/sandboxes/$SANDBOX_NAME \
    -u $BL_ADMIN_USERNAME:$BL_ADMIN_PASSWORD; then
    echo "ERROR: API call to update sandbox failed!"
    exit 1
fi

echo "Sandbox publish completed successfully"
cat tmp/$SANDBOX_NAME/template.json.tmp
