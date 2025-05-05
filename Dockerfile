FROM --platform=linux/amd64 golang:1.23.3-bookworm as build

WORKDIR /blaxel/sandbox-api

RUN go install github.com/air-verse/air@latest

EXPOSE 8080

ENV HOME=/blaxel

ENTRYPOINT ["air"]