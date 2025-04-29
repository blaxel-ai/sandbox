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
