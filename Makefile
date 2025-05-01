dependencies:
	cd sandbox-api && go install github.com/air-verse/air@latest

api:
	cd sandbox-api && air

test:
	cd sandbox-api && go test -v ./...

mcp:
	cd sandbox-api/mcp-inspect && npm run inspect

swagger:
	cd sandbox-api && swag init
	cd sandbox-api/docs && sed -i.bak 's/filesystem\.Directory/Directory/g' swagger.json && rm swagger.json.bak
	cd sandbox-api/docs && sed -i.bak 's/filesystem\.Directory/Directory/g' swagger.yaml && rm swagger.yaml.bak
	cd sandbox-api/docs && sed -i.bak 's/filesystem\.Directory/Directory/g' docs.go && rm docs.go.bak