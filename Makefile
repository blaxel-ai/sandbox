dependencies:
	cd sandbox-api && \
		go install github.com/air-verse/air@latest && \
		go install github.com/swaggo/swag/cmd/swag@latest && \
		brew install yq


api:
	cd sandbox-api && air

docker-build:
	docker build -t blaxel/sandbox-api .

docker-run:
	docker run -p 8080:8080 -p 3000:3000 --rm --name sandbox-dev -v ./sandbox-api:/blaxel/sandbox-api -v ./tmp:/blaxel/tmp blaxel/sandbox-api:latest

test:
	cd sandbox-api && go test -v ./...

benchmark:
	cd sandbox-api && go test -bench=. -benchmem ./src/handler/process

codspeed:
	@if ! command -v codspeed > /dev/null 2>&1; then \
		echo "Installing CodSpeed runner..."; \
		curl -fsSL https://github.com/CodSpeedHQ/runner/releases/latest/download/codspeed-runner-installer.sh | bash; \
	fi
	cd sandbox-api && codspeed run --skip-upload -- go test -bench=. ./src/handler/process

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

deploy-simple-custom-sandbox:
	cd sandbox-api && GOOS=linux GOARCH=amd64 go build -o ../e2e/simple-custom-sandbox/sandbox-api
	cd e2e/simple-custom-sandbox && bl deploy && rm sandbox-api

build-custom-sandbox:
	cp -r sandbox-api e2e/custom-sandbox
	cd e2e/custom-sandbox && docker build -t custom-sandbox:latest . && rm -rf sandbox-api

run-custom-sandbox:
	docker run -d -p 8080:8080 -p 8081:8081 -p 8082:8082 -p 8083:8083 --rm --name sandbox-dev custom-sandbox:latest

test-custom-sandbox:
	@echo "Waiting for custom-sandbox to be deployed..."
	@while [ "$$(bl get sbx custom-sandbox -ojson | jq -r '.[].status')" != "DEPLOYED" ]; do \
		echo "Status: $$(bl get sbx custom-sandbox -ojson | jq -r '.[].status') - waiting..."; \
		sleep 5; \
	done
	@echo "custom-sandbox is DEPLOYED! Running e2e tests..."
	cd e2e/scripts && npm run test:local

e2e:
	@docker rm -f sandbox-dev &&  make build-custom-sandbox && make run-custom-sandbox
	@sleep 5
	@echo "Number of FD before test"
	@docker exec sandbox-dev ls /proc/$$(docker exec sandbox-dev pgrep -f sandbox-api)/fd | wc -l

	make test-custom-sandbox
	sleep 1

	echo "Number of FD after test"
	@docker exec sandbox-dev ls /proc/$$(docker exec sandbox-dev pgrep -f sandbox-api)/fd | wc -l

.PHONY: e2e

mr_develop:
	$(eval BRANCH_NAME := $(shell git rev-parse --abbrev-ref HEAD))
	gh pr create --base develop --head $(BRANCH_NAME) --title "$(BRANCH_NAME)" --body "Merge request from $(BRANCH_NAME) to develop"