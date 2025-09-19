#!/bin/bash

# Exit on any error and print commands as they are executed
set -e
set -x

# Load environment variables from .env file if it exists
if [ -f ".env" ]; then
    echo "Loading environment variables from .env file"
    export $(grep -v '^#' .env | xargs)
fi

# Validate required environment variables
if [ -z "$SANDBOX_NAME" ]; then
    echo "Error: SANDBOX_NAME environment variable is required"
    exit 1
fi

if [ -z "$IMAGE_TAG" ]; then
    echo "Error: IMAGE_TAG environment variable is required"
    exit 1
fi

if [ -z "$BL_ENV" ]; then
    echo "Error: BL_ENV environment variable is required"
    exit 1
fi

if [ -z "$SRC_REGISTRY" ]; then
    echo "Error: SRC_REGISTRY environment variable is required"
    exit 1
fi

if [ -z "$BUILD_ID" ]; then
    echo "Error: BUILD_ID environment variable is required"
    exit 1
fi

if [ -z "$IMAGE_BUCKET_MK3" ]; then
    echo "Error: IMAGE_BUCKET_MK3 environment variable is required"
    exit 1
fi

if [ -z "$DEPOT_PROJECT_ID" ]; then
    echo "Error: DEPOT_PROJECT_ID environment variable is required"
    exit 1
fi

if [ -z "$LAMBDA_FUNCTION_NAME" ]; then
    echo "Error: LAMBDA_FUNCTION_NAME environment variable is required"
    exit 1
fi

if [ -z "$LAMBDA_REGION" ]; then
    echo "Error: LAMBDA_REGION environment variable is required"
    exit 1
fi

if [ -z "$DEPOT_TOKEN" ]; then
    echo "Error: DEPOT_TOKEN environment variable is required"
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

# Set defaults for optional variables
BL_TYPE="${BL_TYPE:-sandbox}"
LOG_LEVEL="${LOG_LEVEL:-debug}"
BASE_IMAGE_TAG="${BASE_IMAGE_TAG:-latest}"

echo "Starting mk3 build process for sandbox..."
echo "Sandbox Name: $SANDBOX_NAME"
echo "Image Tag: $IMAGE_TAG"
echo "BL Environment: $BL_ENV"
echo "BL Type: $BL_TYPE"
echo "Build ID: $BUILD_ID"
echo "Base Image Tag: $BASE_IMAGE_TAG"
echo "Lambda Function: $LAMBDA_FUNCTION_NAME"
echo "Lambda Region: $LAMBDA_REGION"

# Build the JSON payload
PAYLOAD=$(cat <<EOF
{
  "otel_enabled": false,
  "bl_env": "$BL_ENV",
  "output_s3": "s3://$IMAGE_BUCKET_MK3/blaxel/sbx/$SANDBOX_NAME/$IMAGE_TAG",
  "no_optimize": false,
  "depot_token": "$DEPOT_TOKEN",
  "bl_build_id": "$BUILD_ID",
  "bl_type": "$BL_TYPE",
  "bl_generation": "mk3",
  "log_level": "$LOG_LEVEL",
  "depot_project_id": "$DEPOT_PROJECT_ID",
  "image": "$SRC_REGISTRY:$BASE_IMAGE_TAG"
}
EOF
)

echo "Target S3 location: s3://$IMAGE_BUCKET_MK3/blaxel/sbx/$SANDBOX_NAME/$IMAGE_TAG"

# Invoke Lambda and capture response
echo "Invoking Lambda function..."
aws lambda invoke \
  --function-name "$LAMBDA_FUNCTION_NAME" \
  --region "$LAMBDA_REGION" \
  --payload "$PAYLOAD" \
  --cli-binary-format raw-in-base64-out \
  --log-type Tail \
  --no-cli-pager \
  --cli-read-timeout 900 \
  --cli-connect-timeout 60 \
  /tmp/response.json

# Check response
if [ ! -f /tmp/response.json ]; then
    echo "Error: No response from Lambda"
    exit 1
fi

echo "Lambda response received, parsing..."

# Display the full response for debugging
cat /tmp/response.json | jq '.'

# Check if the build was successful
BUILD_SUCCESS=$(jq -r '.success // false' /tmp/response.json 2>/dev/null)
if [ "$BUILD_SUCCESS" != "true" ]; then
    echo "Build failed"
    MESSAGE=$(jq -r '.message // "No message provided"' /tmp/response.json)
    echo "Error: $MESSAGE"
    exit 1
fi

echo "Build completed successfully"

# Update image registry after successful build
echo "Updating image registry..."

# Create Basic auth header (base64 encode username:password)
AUTH_HEADER=$(echo -n "$BL_ADMIN_USERNAME:$BL_ADMIN_PASSWORD" | base64)

# Workspace is always blaxel
WORKSPACE="blaxel"

# Determine registry type based on the registry URL
if [[ "$SRC_REGISTRY" == *"ghcr.io"* ]]; then
    REGISTRY_TYPE="github"
else
    REGISTRY_TYPE="docker_hub"
fi

# Extract the registry URL from SRC_REGISTRY
REGISTRY_URL=$(echo "$SRC_REGISTRY" | cut -d'/' -f1)

# Prepare the JSON payload for the API
API_PAYLOAD=$(jq -n \
  --arg registry "$REGISTRY_URL" \
  --arg workspace "$WORKSPACE" \
  --arg repository "$SANDBOX_NAME" \
  --arg tag "$IMAGE_TAG" \
  --arg registry_type "$REGISTRY_TYPE" \
  --arg original "sbx/$SANDBOX_NAME:$IMAGE_TAG" \
  --arg region "$LAMBDA_REGION" \
  --arg bucket "$IMAGE_BUCKET_MK3" \
  '{
    registry: $registry,
    workspace: $workspace,
    repository: $repository,
    tag: $tag,
    registry_type: $registry_type,
    original: $original,
    region: $region,
    bucket: $bucket
  }')

echo "Calling Blaxel API to register image..."
echo "URL: $BL_API_URL/admin/images"
echo "Workspace: $WORKSPACE"
echo "Repository: $SANDBOX_NAME"
echo "Tag: $IMAGE_TAG"
echo "Payload: $API_PAYLOAD"

# Make the API call
HTTP_RESPONSE=$(curl -s -w "\n%{http_code}" --request PUT \
  --url "$BL_API_URL/admin/images" \
  --header "Authorization: Basic $AUTH_HEADER" \
  --header "Content-Type: application/json" \
  --data "$API_PAYLOAD")

# Extract HTTP status code
HTTP_STATUS=$(echo "$HTTP_RESPONSE" | tail -n 1)
HTTP_BODY=$(echo "$HTTP_RESPONSE" | sed '$d')

echo "API Response Status: $HTTP_STATUS"
if [ ! -z "$HTTP_BODY" ]; then
    echo "API Response Body: $HTTP_BODY"
fi

# Check if the API call was successful
if [ "$HTTP_STATUS" -ge 200 ] && [ "$HTTP_STATUS" -lt 300 ]; then
    echo "Image successfully registered in Blaxel"
else
    echo "Warning: Failed to register image in Blaxel (HTTP $HTTP_STATUS)"
    # Don't fail the build if image registration fails
    # exit 1
fi

echo "mk3 build process completed successfully"
