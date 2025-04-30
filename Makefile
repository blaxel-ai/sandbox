dependencies:
	cd uvm-api && go install github.com/air-verse/air@latest

api:
	cd uvm-api && air

test:
	cd uvm-api && go test -v ./...

mcp:
	cd uvm-api/mcp-inspect && npm run inspect

swagger:
	cd uvm-api && swag init
	cd uvm-api/docs && sed -i.bak 's/filesystem\.Directory/Directory/g' swagger.json && rm swagger.json.bak
	cd uvm-api/docs && sed -i.bak 's/filesystem\.Directory/Directory/g' swagger.yaml && rm swagger.yaml.bak
	cd uvm-api/docs && sed -i.bak 's/filesystem\.Directory/Directory/g' docs.go && rm docs.go.bak