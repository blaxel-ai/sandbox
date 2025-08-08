FROM node:23-slim

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
    gcc \
    libc6-dev \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Go 1.23.3 manually (detecting architecture)
RUN ARCH=$(dpkg --print-architecture) && \
    if [ "$ARCH" = "amd64" ]; then \
        GO_ARCH="amd64"; \
    elif [ "$ARCH" = "arm64" ]; then \
        GO_ARCH="arm64"; \
    else \
        echo "Unsupported architecture: $ARCH" && exit 1; \
    fi && \
    wget https://go.dev/dl/go1.23.3.linux-${GO_ARCH}.tar.gz && \
    tar -C /usr/local -xzf go1.23.3.linux-${GO_ARCH}.tar.gz && \
    rm go1.23.3.linux-${GO_ARCH}.tar.gz

ENV PATH=/usr/local/go/bin:$PATH
ENV GOBIN=/usr/local/bin

WORKDIR /home/user

RUN update-ca-certificates
RUN npm i -g typescript ts-node @types/node dotenv webpack webpack-cli

# Install code-server
RUN curl -fsSL https://code-server.dev/install.sh | sh

# Create /blaxel directory and Next.js app for testing
RUN mkdir -p /blaxel && \
    npx create-next-app@latest /blaxel/app --use-npm --typescript --eslint --tailwind --src-dir --app --import-alias "@/*" --no-git --yes --no-turbopack

# Copy and build sandbox-api from local source
COPY sandbox-api /home/user/sandbox-api
# Build with CGO disabled but with specific tags to avoid FD leaks
# The osusergo and netgo tags use pure Go implementations that handle FDs better
RUN cd /home/user/sandbox-api && go build -o /usr/local/bin/sandbox-api

# Copy entrypoint script
COPY entrypoint.sh /home/user/entrypoint.sh
RUN chmod +x /home/user/entrypoint.sh

ENTRYPOINT ["/home/user/entrypoint.sh"]