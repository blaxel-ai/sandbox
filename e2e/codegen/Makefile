build:
	docker build --platform=linux/amd64 -t sandbox-claude-code .

run:
	docker rm -f sandbox-claude-code || true
	docker run --platform=linux/amd64 -p 8080:8080 -p 3000:3000 --name sandbox-claude-code sandbox-claude-code
