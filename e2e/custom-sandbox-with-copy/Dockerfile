FROM node:24-slim

RUN apt-get update && apt-get install -y \
    curl \
    wget \
    procps \
    jq \
    sed \
    grep \
    nano \
    vim \
    git \
    sudo \
    python3 \
    zip \
    tree \
    unzip \
    ca-certificates \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /home/user

RUN update-ca-certificates
RUN npm i -g typescript ts-node @types/node dotenv webpack webpack-cli

# Install code-server
RUN curl -fsSL https://code-server.dev/install.sh | sh

# Create the Next.js app for testing
RUN mkdir -p /app && \
    npx create-next-app@latest /app --use-npm --typescript --eslint --tailwind --src-dir --app --import-alias "@/*" --no-git --yes --no-turbopack

# Copy sandbox-api binary from registry
COPY --from=docker.io/baseten/sandbox:latest /sandbox-api /usr/local/bin/sandbox-api

# Copy entrypoint script
COPY entrypoint.sh /home/user/entrypoint.sh
RUN chmod +x /home/user/entrypoint.sh

ENTRYPOINT ["/home/user/entrypoint.sh"]