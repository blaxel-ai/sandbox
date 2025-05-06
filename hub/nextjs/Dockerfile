FROM --platform=linux/amd64 golang:1.23.3-bookworm as build

WORKDIR /blaxel/sandbox-api

COPY ./sandbox-api /blaxel/sandbox-api

RUN set -xe; \
    go build \
    -v \
    -buildmode=pie \
    -ldflags "-linkmode external -extldflags -static-pie" \
    -tags netgo \
    -o sandbox-api-binary . \
    ;

FROM --platform=linux/amd64 node:22-alpine

WORKDIR /blaxel

COPY --from=build /blaxel/sandbox-api/sandbox-api-binary /usr/local/bin/sandbox-api


# Option 1: Start with a fresh Next.js project
RUN mkdir -p /blaxel/app 

RUN npx create-next-app@latest /blaxel/app --use-npm --typescript --eslint --tailwind --src-dir --app --import-alias "@/*" --no-git --yes

# Expose ports for Next.js dev server
EXPOSE 3000

ENTRYPOINT ["/usr/local/bin/sandbox-api", "-c", "cd /blaxel/app && npm run dev -- -p 3000"]