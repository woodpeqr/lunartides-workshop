FROM alpine:3.21
ARG WORKER_VERSION=v0.1.0
ARG TARGETARCH=amd64
RUN apk add --no-cache curl && \
    mkdir -p /var/log/lunartides && \
    curl -fsSL -o /usr/local/bin/shoal \
      "https://github.com/woodpeqr/lunartides-workers-releases/releases/download/${WORKER_VERSION}/shoal_linux_${TARGETARCH}" && \
    chmod +x /usr/local/bin/shoal && \
    apk del curl
EXPOSE 8080
CMD ["/usr/local/bin/shoal"]
