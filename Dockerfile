FROM --platform=linux/amd64 node:22-alpine

WORKDIR /blaxel/sandbox-api

RUN apk update && apk add --no-cache \
  git \
  go \
  && rm -rf /var/cache/apk/*


ENV HOME=/blaxel
ENV GOBIN=/usr/local/bin
ENV PATH=$PATH:$GOBIN

RUN go install github.com/air-verse/air@v1.62.0
RUN npx create-next-app@latest /blaxel/app --use-npm --typescript --eslint --tailwind --src-dir --app --import-alias "@/*" --no-git --yes --no-turbopack
EXPOSE 8080

ENTRYPOINT ["air"]