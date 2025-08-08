dependencies:
	cd sandbox-api && go install github.com/air-verse/air@latest

api:
	cd sandbox-api && air

docker-build:
	docker build -t blaxel/sandbox-api .

docker-run:
	docker run -p 8080:8080 -p 3000:3000 --rm --name sandbox-dev -v ./sandbox-api:/blaxel/sandbox-api -v ./tmp:/blaxel/tmp localhost/blaxel/sandbox-api:latest

test:
	cd sandbox-api && go test -v ./...

integration-test:
	cd sandbox-api/integration-tests && ./run_tests.sh

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
	# Add security configuration
	yq eval '.security = [{"BearerAuth": []}]' -i sandbox-api/docs/openapi.yml
	yq eval '.components.securitySchemes.BearerAuth = {"type": "http", "scheme": "bearer", "bearerFormat": "JWT"}' -i sandbox-api/docs/openapi.yml
	cd sandbox-api/docs && sh fixopenapi.sh

deploy-custom-sandbox:
	cp -r sandbox-api e2e/custom-sandbox
	cd e2e/custom-sandbox && bl deploy && rm -rf sandbox-api
