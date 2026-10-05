#!/bin/sh
# Swagger 2 has one response schema for all produced media types. Correct
# the converted OpenAPI media schemas without describing a stream as one result.
yq eval '.paths."/process".post.responses."200".content."application/x-ndjson".schema = {"type": "string", "description": "Newline-delimited JSON event objects. Each record has type (stdout, stderr, result, error, or keepalive) and optional string data. Result data is a JSON-encoded ProcessResponse; output data preserves raw chunks."} | del(.paths."/process".post.responses."200".content."text/event-stream")' -i openapi.yml

sed -i.bak '/^                $ref: "#\/components\/schemas\/Directory"/{
  s/.*/                oneOf:\
                    - $ref: "#\/components\/schemas\/Directory"\
                    - $ref: "#\/components\/schemas\/FileWithContent"\
                    - type: string\
                      format: binary/
}' openapi.yml
rm openapi.yml.bak
