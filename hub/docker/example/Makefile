.PHONY: up down add list test

up:
	docker compose up -d --build

down:
	docker compose down

add:
	curl -s -X POST http://localhost:3000/notes \
		-H "Content-Type: application/json" \
		-d '{"content": "Hello from Makefile!"}' | jq

list:
	curl -s http://localhost:3000/notes | jq

test: add list
