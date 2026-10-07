#!/bin/sh
# Swagger 2 has one response schema for all produced media types. Correct
# the converted OpenAPI media schemas without describing a stream as one result.
yq eval '.paths."/process".post.responses."200".content."application/x-ndjson".schema = {"type": "string", "description": "Newline-delimited JSON event objects. Each record has type (stdout, stderr, result, error, or keepalive) and optional string data. Result data is a JSON-encoded ProcessResponse; output data preserves raw chunks."} | del(.paths."/process".post.responses."200".content."text/event-stream")' -i openapi.yml

# Only the generic file/directory read has polymorphic responses. Downloads
# may use the file's MIME type, so retain its raw-file alternative for each media.
# Tree reads and writes always return Directory.
yq eval '.paths."/filesystem/{path}".get.responses."200".content[].schema = {"oneOf": [{"$ref": "#/components/schemas/Directory"}, {"$ref": "#/components/schemas/FileWithContent"}, {"type": "string", "format": "binary"}]}' -i openapi.yml

# swag (Swagger 2.0) cannot document a JSON body and form fields on the same operation
yq eval '.paths["/filesystem/{path}"].put.requestBody.content["multipart/form-data"].schema = {
  "type": "object",
  "required": ["file"],
  "properties": {
    "file": {"type": "string", "format": "binary", "description": "File content"},
    "permissions": {"type": "string", "example": "0755", "description": "Octal mode applied when the file is created (default 0644); an existing file keeps its mode"},
    "path": {"type": "string", "description": "Ignored: the target is always the URL path"}
  }
}' -i openapi.yml

# A streaming response is a sequence of records, not one JSON object.
yq eval '.paths."/process/{identifier}/logs/stream".get.responses."200".content."application/x-ndjson".schema = {"type": "string", "description": "Newline-delimited JSON records with type (stdout, stderr, keepalive, restart, truncated, error), optional data, and optional encoding=base64 for non-UTF-8 chunks. Decode each output record before concatenating bytes by source. No result record."}' -i openapi.yml

# Errors before streaming use the regular JSON response regardless of Accept.
for status in 400 404 409 500; do
  STATUS="$status" yq eval '.paths."/process/{identifier}/logs/stream".get.responses[strenv(STATUS)].content = {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorResponse"}}}' -i openapi.yml
done
