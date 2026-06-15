FROM alpine:3.21
ARG WORKER_VERSION=v0.1.0
ARG TARGETARCH=amd64
RUN apk add --no-cache curl && \
    mkdir -p /var/log/lunartides && \
    curl -fsSL -o /usr/local/bin/swell \
      "https://github.com/woodpeqr/lunartides-workers-releases/releases/download/${WORKER_VERSION}/swell_linux_${TARGETARCH}" && \
    chmod +x /usr/local/bin/swell && \
    apk del curl
EXPOSE 8080
EXPOSE 9090
CMD ["/usr/local/bin/swell"]
