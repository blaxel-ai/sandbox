ARG SANDBOX_VERSION=latest
FROM ghcr.io/blaxel-ai/sandbox:${SANDBOX_VERSION} AS sandbox-api
FROM redroid/redroid@sha256:a6c464bbedcf1dcb67dbf91f329fbb19bee5b50631f0ca6bda6ed7c41b0e64e2 AS android

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    runc adb python3 iproute2 iptables util-linux ca-certificates \
    && apt-get clean && rm -rf /var/lib/apt/lists/*
COPY --from=sandbox-api /sandbox-api /usr/local/bin/sandbox-api
# Bake the filesystem into the image: no registry access or extraction at boot.
COPY --from=android / /opt/android/bundle/rootfs/
COPY hub/android/config.json /opt/android/config.json
COPY hub/android/android.py /opt/android/android.py
RUN mkdir -p /var/lib/android/data /var/log/android /run/android /blaxel \
    && chmod +x /usr/local/bin/sandbox-api
WORKDIR /blaxel
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sandbox-api", "--command", "exec python3 /opt/android/android.py"]
