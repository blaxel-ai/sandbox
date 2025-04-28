dependencies:
	cd uvm-api && go install github.com/air-verse/air@latest

api:
	cd uvm-api && air

mcp:
	cd uvm-api/mcp-inspect && npm run inspect

