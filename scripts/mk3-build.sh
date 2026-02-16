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

if [ -z "$BATCH_JOB_QUEUE" ]; then
    echo "Error: BATCH_JOB_QUEUE environment variable is required"
    exit 1
fi

if [ -z "$BATCH_JOB_DEFINITION" ]; then
    echo "Error: BATCH_JOB_DEFINITION environment variable is required"
    exit 1
fi

if [ -z "$BATCH_REGION" ]; then
    echo "Error: BATCH_REGION environment variable is required"
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
echo "Batch Job Queue: $BATCH_JOB_QUEUE"
echo "Batch Job Definition: $BATCH_JOB_DEFINITION"
echo "Batch Region: $BATCH_REGION"

OUTPUT_S3="s3://$IMAGE_BUCKET_MK3/blaxel/sbx/$SANDBOX_NAME/$IMAGE_TAG"
echo "Target S3 location: $OUTPUT_S3"

# Check if template.json has build.slim set to false
NO_SLIM="false"
TEMPLATE_FILE="hub/$SANDBOX_NAME/template.json"
if [ -f "$TEMPLATE_FILE" ]; then
    BUILD_SLIM=$(jq -r 'if .build.slim == false then "false" else "true" end' "$TEMPLATE_FILE")
    if [ "$BUILD_SLIM" = "false" ]; then
        NO_SLIM="true"
        echo "Template has build.slim=false, setting NO_SLIM=true"
    fi
fi

# Build container overrides with environment variables and command for the Batch job.
# Pass --image as CLI arg since metamorph may not read IMAGE env var (INPUT_S3 flow works, IMAGE flow fails).
IMAGE_REF="$SRC_REGISTRY:$BASE_IMAGE_TAG"
CONTAINER_OVERRIDES=$(jq -n \
  --arg image "$IMAGE_REF" \
  --arg otel_enabled "false" \
  --arg bl_env "$BL_ENV" \
  --arg output_s3 "$OUTPUT_S3" \
  --arg no_optimize "false" \
  --arg depot_token "$DEPOT_TOKEN" \
  --arg bl_build_id "$BUILD_ID" \
  --arg bl_type "$BL_TYPE" \
  --arg bl_generation "mk3" \
  --arg log_level "$LOG_LEVEL" \
  --arg depot_project_id "$DEPOT_PROJECT_ID" \
  --arg no_slim "$NO_SLIM" \
  '{
    command: ["--image", $image],
    environment: [
      {name: "OTEL_ENABLED", value: $otel_enabled},
      {name: "BL_ENV", value: $bl_env},
      {name: "OUTPUT_S3", value: $output_s3},
      {name: "NO_OPTIMIZE", value: $no_optimize},
      {name: "DEPOT_TOKEN", value: $depot_token},
      {name: "BL_BUILD_ID", value: $bl_build_id},
      {name: "BL_TYPE", value: $bl_type},
      {name: "BL_GENERATION", value: $bl_generation},
      {name: "LOG_LEVEL", value: $log_level},
      {name: "DEPOT_PROJECT_ID", value: $depot_project_id},
      {name: "IMAGE", value: $image},
      {name: "NO_SLIM", value: $no_slim}
    ]
  }')

JOB_NAME="mk3-build-${SANDBOX_NAME}-${IMAGE_TAG//[^a-zA-Z0-9-]/-}-$(date +%s)"

echo "Submitting Batch job..."
JOB_SUBMIT=$(aws batch submit-job \
  --job-name "$JOB_NAME" \
  --job-queue "$BATCH_JOB_QUEUE" \
  --job-definition "$BATCH_JOB_DEFINITION" \
  --region "$BATCH_REGION" \
  --container-overrides "$CONTAINER_OVERRIDES" \
  --output json)

JOB_ID=$(echo "$JOB_SUBMIT" | jq -r '.jobId')
if [ -z "$JOB_ID" ] || [ "$JOB_ID" = "null" ]; then
    echo "Error: Failed to submit Batch job"
    echo "$JOB_SUBMIT" | jq '.'
    exit 1
fi

echo "Batch job submitted: $JOB_ID"
echo "Waiting for job to complete..."

# Poll for job completion
MAX_ATTEMPTS=180
POLL_INTERVAL=10
for attempt in $(seq 1 $MAX_ATTEMPTS); do
    JOB_STATUS=$(aws batch describe-jobs \
      --jobs "$JOB_ID" \
      --region "$BATCH_REGION" \
      --query 'jobs[0].status' \
      --output text)

    echo "  Attempt $attempt/$MAX_ATTEMPTS: Job status $JOB_STATUS"

    case "$JOB_STATUS" in
        SUCCEEDED)
            echo "Build completed successfully"
            break
            ;;
        FAILED)
            echo "Build failed"
            aws batch describe-jobs --jobs "$JOB_ID" --region "$BATCH_REGION" | jq '.jobs[0] | {status, statusReason, container: .container}'
            exit 1
            ;;
        SUBMITTED|PENDING|RUNNABLE|STARTING|RUNNING)
            sleep $POLL_INTERVAL
            ;;
        *)
            echo "Unknown job status: $JOB_STATUS"
            exit 1
            ;;
    esac

    if [ $attempt -eq $MAX_ATTEMPTS ]; then
        echo "Error: Job did not complete within timeout"
        exit 1
    fi
done

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
  --arg mk3 "blaxel/blaxel/sbx/$SANDBOX_NAME:$IMAGE_TAG" \
  --arg tag "$IMAGE_TAG" \
  --arg registry_type "$REGISTRY_TYPE" \
  --arg original "sbx/$SANDBOX_NAME:$IMAGE_TAG" \
  --arg region "$BATCH_REGION" \
  --arg bucket "$IMAGE_BUCKET_MK3" \
  '{
    registry: $registry,
    workspace: $workspace,
    repository: $repository,
    tag: $tag,
    registry_type: $registry_type,
    original: $original,
    region: $region,
    bucket: $bucket,
    mk3: $mk3
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
