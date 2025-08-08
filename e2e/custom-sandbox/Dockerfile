FROM node:22-alpine

WORKDIR /blaxel

RUN apk update && apk add --no-cache \
  git \
  curl \
  go \
  netcat-openbsd \
  && rm -rf /var/cache/apk/*

ENV HOME=/blaxel
ENV GOBIN=/usr/local/bin
ENV PATH=$PATH:$GOBIN

COPY sandbox-api /blaxel/sandbox-api

RUN cd /blaxel/sandbox-api && go build -o /usr/local/bin/sandbox-api

ENTRYPOINT ["/usr/local/bin/sandbox-api"]