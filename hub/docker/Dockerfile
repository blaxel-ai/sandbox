FROM alpine

RUN apk update && apk add --no-cache \
  git \
  curl \
  docker \
  docker-cli-compose \
  netcat-openbsd \
  iptables \
  iptables-legacy \
  zsh \
  make \
  jq \
  && rm -rf /var/cache/apk/*

# The sandbox kernel doesn't support nftables, so switch to iptables-legacy
RUN ln -sf /usr/sbin/iptables-legacy /usr/sbin/iptables \
  && ln -sf /usr/sbin/ip6tables-legacy /usr/sbin/ip6tables

# Install oh-my-zsh with docker plugin
RUN sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)" "" --unattended \
  && sed -i 's/plugins=(git)/plugins=(git docker)/' /root/.zshrc \
  && echo 'zstyle ":completion:*" rehash true' >> /root/.zshrc

WORKDIR /app

COPY --from=ghcr.io/blaxel-ai/sandbox:latest /sandbox-api /usr/local/bin/sandbox-api

COPY hub/docker/example /app/example

COPY hub/docker/entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]