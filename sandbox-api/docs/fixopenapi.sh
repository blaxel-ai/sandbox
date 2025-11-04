#!/bin/sh
sed -i.bak '/^                $ref: "#\/components\/schemas\/Directory"/{
  s/.*/                oneOf:\
                    - $ref: "#\/components\/schemas\/Directory"\
                    - $ref: "#\/components\/schemas\/FileWithContent"\
                    - type: string\
                      format: binary/
}' openapi.yml
rm openapi.yml.bak