dependencies:
	cd sandbox-api && go install github.com/air-verse/air@latest

api:
	cd sandbox-api && air

test:
	cd sandbox-api && go test -v ./...

mcp:
	cd sandbox-api/mcp-inspect && npm run inspect

reference:
	cd sandbox-api && swag init
	cd sandbox-api/docs && sed -i.bak 's/filesystem\.Directory/Directory/g' swagger.yaml && rm swagger.yaml.bak
	cd sandbox-api/docs && sed -i.bak 's/filesystem\.Directory/Directory/g' docs.go && rm docs.go.bak
	mv sandbox-api/docs/swagger.yaml sandbox-api/docs/swagger.yml
	npx swagger2openapi --yaml --outfile ./sandbox-api/docs/openapi.yml ./sandbox-api/docs/swagger.yml
	rm -rf sandbox-api/docs/swagger.yml
	rm -rf sandbox-api/docs/swagger.json
	cd sandbox-api/docs && sh fixopenapi.sh