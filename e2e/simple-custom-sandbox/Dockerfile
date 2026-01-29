FROM node:22-slim

RUN apt-get update && apt-get install -y curl git ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY sandbox-api /app/sandbox-api

ENTRYPOINT ["/app/sandbox-api"]