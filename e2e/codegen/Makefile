build:
	docker build --platform=linux/amd64 -t blaxel-sandbox-claude-code .

run:
	docker rm -f blaxel-sandbox-claude-code || true
	docker run --platform=linux/amd64 -p 8080:8080 -p 3000:3000 --name blaxel-sandbox-claude-code blaxel-sandbox-claude-code
