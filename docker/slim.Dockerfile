FROM golang:1.25.0-bookworm as build

WORKDIR /src/sandbox-api

COPY sandbox-api/ /src/sandbox-api/

RUN go build \
  -v \
  -buildmode=pie \
  -ldflags "-linkmode external -extldflags -static-pie" \
  -tags netgo \
  -o sandbox-api .

FROM python:3.12-slim

WORKDIR /workspace

COPY --from=build /src/sandbox-api/sandbox-api /usr/local/bin/sandbox-api

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sandbox-api"]
