# Multipart Upload API

This API implements S3-style multipart uploads, allowing large files to be uploaded in chunks for better reliability, performance, and resumability.

## Overview

Multipart upload consists of three main steps:
1. **Initiate** - Create an upload session and get an upload ID
2. **Upload Parts** - Upload file chunks with part numbers (1-10000)
3. **Complete** - Assemble all parts into the final file

## API Endpoints

### 1. Initiate Multipart Upload

Create a new multipart upload session.

**Endpoint:** `POST /filesystem-multipart/initiate/{path}`

**Request Body (optional):**
```json
{
  "permissions": "0644"
}
```

**Response:**
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "path": "/path/to/file.dat"
}
```

**Example:**
```bash
curl -X POST http://localhost:8080/filesystem-multipart/initiate/uploads/largefile.dat \
  -H "Content-Type: application/json" \
  -d '{"permissions": "0644"}'
```

### 2. Upload Part

Upload a single part of the file. Parts can be uploaded in any order and in parallel.

**Endpoint:** `PUT /filesystem-multipart/{uploadId}/part?partNumber={partNumber}`

**Request:** multipart/form-data with a `file` field

**Response:**
```json
{
  "partNumber": 1,
  "etag": "5d41402abc4b2a76b9719d911017c592",
  "size": 5242880
}
```

**Example:**
```bash
# Upload part 1
curl -X PUT "http://localhost:8080/filesystem-multipart/550e8400-e29b-41d4-a716-446655440000/part?partNumber=1" \
  -F "file=@part1.dat"

# Upload part 2
curl -X PUT "http://localhost:8080/filesystem-multipart/550e8400-e29b-41d4-a716-446655440000/part?partNumber=2" \
  -F "file=@part2.dat"

# Upload part 3
curl -X PUT "http://localhost:8080/filesystem-multipart/550e8400-e29b-41d4-a716-446655440000/part?partNumber=3" \
  -F "file=@part3.dat"
```

### 3. Complete Multipart Upload

Finalize the upload by assembling all parts in order.

**Endpoint:** `POST /filesystem-multipart/{uploadId}/complete`

**Request Body:**
```json
{
  "parts": [
    {
      "partNumber": 1,
      "etag": "5d41402abc4b2a76b9719d911017c592"
    },
    {
      "partNumber": 2,
      "etag": "7d793037a0760186574b0282f2f435e7"
    },
    {
      "partNumber": 3,
      "etag": "9f6e6800cfae7749eb6c486619254b9c"
    }
  ]
}
```

**Response:**
```json
{
  "message": "Multipart upload completed successfully",
  "path": "/path/to/file.dat"
}
```

**Example:**
```bash
curl -X POST http://localhost:8080/filesystem-multipart/550e8400-e29b-41d4-a716-446655440000/complete \
  -H "Content-Type: application/json" \
  -d '{
    "parts": [
      {"partNumber": 1, "etag": "5d41402abc4b2a76b9719d911017c592"},
      {"partNumber": 2, "etag": "7d793037a0760186574b0282f2f435e7"},
      {"partNumber": 3, "etag": "9f6e6800cfae7749eb6c486619254b9c"}
    ]
  }'
```

### 4. Abort Multipart Upload

Cancel an upload and clean up all uploaded parts.

**Endpoint:** `DELETE /filesystem-multipart/{uploadId}/abort`

**Response:**
```json
{
  "message": "Multipart upload aborted successfully"
}
```

**Example:**
```bash
curl -X DELETE http://localhost:8080/filesystem-multipart/550e8400-e29b-41d4-a716-446655440000/abort
```

### 5. List Parts

View all uploaded parts for an upload session.

**Endpoint:** `GET /filesystem-multipart/{uploadId}/parts`

**Response:**
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "parts": [
    {
      "partNumber": 1,
      "etag": "5d41402abc4b2a76b9719d911017c592",
      "size": 5242880,
      "uploadedAt": "2024-01-01T12:00:00Z"
    },
    {
      "partNumber": 2,
      "etag": "7d793037a0760186574b0282f2f435e7",
      "size": 5242880,
      "uploadedAt": "2024-01-01T12:00:30Z"
    }
  ]
}
```

**Example:**
```bash
curl http://localhost:8080/filesystem-multipart/550e8400-e29b-41d4-a716-446655440000/parts
```

### 6. List All Multipart Uploads

View all active multipart upload sessions.

**Endpoint:** `GET /filesystem-multipart`

**Response:**
```json
{
  "uploads": [
    {
      "uploadId": "550e8400-e29b-41d4-a716-446655440000",
      "path": "/path/to/file1.dat",
      "permissions": 420,
      "initiatedAt": "2024-01-01T12:00:00Z",
      "parts": {
        "1": {
          "partNumber": 1,
          "etag": "5d41402abc4b2a76b9719d911017c592",
          "size": 5242880,
          "uploadedAt": "2024-01-01T12:00:10Z"
        }
      }
    }
  ]
}
```

**Example:**
```bash
curl http://localhost:8080/filesystem/multipart
```

## Complete Example Workflow

Here's a complete example of uploading a large file in parts:

```bash
#!/bin/bash

# 1. Split a large file into 5MB parts
split -b 5M largefile.dat part_

  # 2. Initiate multipart upload
  RESPONSE=$(curl -s -X POST http://localhost:8080/filesystem/multipart/initiate/uploads/largefile.dat \
    -H "Content-Type: application/json" \
    -d '{"permissions": "0644"}')

UPLOAD_ID=$(echo $RESPONSE | jq -r '.uploadId')
echo "Upload ID: $UPLOAD_ID"

# 3. Upload each part and collect ETags
PART_NUM=1
PARTS_JSON="["

for part_file in part_*; do
  echo "Uploading part $PART_NUM from $part_file..."

  PART_RESPONSE=$(curl -s -X PUT \
    "http://localhost:8080/filesystem/multipart/$UPLOAD_ID/part?partNumber=$PART_NUM" \
    -F "file=@$part_file")

  ETAG=$(echo $PART_RESPONSE | jq -r '.etag')

  if [ $PART_NUM -gt 1 ]; then
    PARTS_JSON="$PARTS_JSON,"
  fi
  PARTS_JSON="$PARTS_JSON{\"partNumber\":$PART_NUM,\"etag\":\"$ETAG\"}"

  PART_NUM=$((PART_NUM + 1))

  # Clean up part file
  rm $part_file
done

PARTS_JSON="$PARTS_JSON]"

# 4. Complete the multipart upload
echo "Completing upload..."
curl -X POST "http://localhost:8080/filesystem/multipart/$UPLOAD_ID/complete" \
  -H "Content-Type: application/json" \
  -d "{\"parts\":$PARTS_JSON}"

echo "Upload complete!"
```

## Features

### S3-Compatible Design
- Upload ID system for tracking sessions
- Part numbers (1-10000) for ordering
- ETag (MD5 hash) for data integrity verification
- Parts can be uploaded in any order
- Support for parallel uploads

### Benefits
- **Large File Support**: Upload files of any size in manageable chunks
- **Resumability**: If an upload fails, only failed parts need to be re-uploaded
- **Parallel Uploads**: Upload multiple parts simultaneously for faster speeds
- **Network Resilience**: Small part sizes reduce impact of network interruptions
- **Memory Efficient**: Streaming I/O avoids loading entire file in memory

### Technical Details
- Part size: No minimum or maximum (though S3 recommends 5MB-5GB per part)
- Maximum parts: 10,000 per upload
- Storage: Parts are stored in temporary directory until completion
- Cleanup: Aborted or completed uploads are automatically cleaned up
- Persistence: Upload metadata is saved to disk and restored on server restart
- Data Integrity: MD5 checksums (ETags) verify part integrity

## Error Handling

Common error scenarios:

1. **Upload not found**: The upload ID doesn't exist or has been aborted
2. **Part number out of range**: Part numbers must be 1-10000
3. **ETag mismatch**: The provided ETag doesn't match the stored part
4. **Missing parts**: Completion requires all parts to be uploaded
5. **Invalid file path**: The destination path is invalid or inaccessible

## Best Practices

1. **Part Size**: Use 5-10MB parts for optimal performance
2. **Parallel Uploads**: Upload 3-5 parts in parallel for best throughput
3. **Track ETags**: Store returned ETags for completion request
4. **Error Retry**: Implement exponential backoff for failed part uploads
5. **Cleanup**: Always abort uploads that won't be completed
6. **Monitoring**: Use the list endpoints to monitor upload progress

## Differences from S3

While inspired by S3, this implementation has some differences:

1. **No minimum part size**: S3 requires 5MB minimum (except last part)
2. **Simpler ETag**: Uses MD5 hash instead of S3's complex multipart ETag format
3. **No presigned URLs**: Direct upload only
4. **Single storage backend**: No distributed storage or replication
5. **Local filesystem**: Parts and final files stored on local filesystem

## Storage Location

- **Upload parts**: Stored in `/tmp/multipart-uploads/{uploadId}/part-{partNumber}`
- **Metadata**: Stored in `/tmp/multipart-uploads/{uploadId}/metadata.json`
- **Final file**: Stored at the path specified during initiation

## Security Considerations

1. **Path validation**: File paths are validated to prevent directory traversal
2. **Permissions**: Support for Unix file permissions (default 0644)
3. **Resource limits**: Maximum 10,000 parts per upload
4. **Cleanup**: Implement periodic cleanup of abandoned uploads
5. **Storage quotas**: Consider implementing storage quotas if needed

