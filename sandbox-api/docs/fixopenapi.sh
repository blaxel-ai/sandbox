#!/bin/sh
sed -i.bak '/^                $ref: "#\/components\/schemas\/Directory"/{
  s/.*/                oneOf:\
                    - $ref: "#\/components\/schemas\/Directory"\
                    - $ref: "#\/components\/schemas\/FileWithContent"\
                    - type: string\
                      format: binary/
}' openapi.yml
rm openapi.yml.bak
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
