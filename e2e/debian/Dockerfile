FROM node:23-slim

RUN apt-get update && apt-get install -y \
    curl \
    git \
    jq \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /home/user

COPY --from=ghcr.io/blaxel-ai/sandbox:develop /sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080 3000 8081
ENTRYPOINT ["/usr/local/bin/sandbox-api"]

