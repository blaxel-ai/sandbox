ARGS:= $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))

dependencies:
	cd sandbox-api && \
		go install github.com/air-verse/air@latest && \
		go install github.com/swaggo/swag/cmd/swag@latest && \
		brew install yq

api:
	cd sandbox-api && SANDBOX_LOG_DIR=./tmp/log air

docker-build:
	docker build -t sandbox-api .

docker-run:
	docker run -p 8080:8080 -p 3000:3000 --rm --name sandbox-dev -v ./sandbox-api:/src/sandbox-api -v ./tmp:/src/tmp sandbox-api:latest

test:
	cd sandbox-api && go test -v ./...

benchmark:
	cd sandbox-api && go test -bench=. ./src/api

integration-test:
	cd sandbox-api/integration-tests && ./run_tests.sh

# Same suite, against an API that keeps root and scopes the workload to an
# unprivileged user (Dockerfile USER / --user). Needs root.
identity-test:
	cd sandbox-api/integration-tests && sudo ./run_identity_tests.sh

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

build-custom-sandbox:
	cp -r sandbox-api e2e/custom-sandbox
	cd e2e/custom-sandbox && docker build -t custom-sandbox:latest . && rm -rf sandbox-api

run-custom-sandbox:
	docker run -d -p 8080:8080 -p 8081:8081 -p 8082:8082 -p 8083:8083 --rm --name sandbox-dev custom-sandbox:latest

tag:
	git checkout main
	git pull origin main
	git tag -a v$(ARGS) -m "Release v$(ARGS)"
	git push origin v$(ARGS)

%:
	@: