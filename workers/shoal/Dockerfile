FROM alpine:latest
ARG WORKER_VERSION=v0.1.0
ARG TARGETARCH
RUN mkdir -p /var/log/lunartides && \
    wget -q --no-check-certificate -O /usr/local/bin/shoal \
      "https://github.com/woodpeqr/lunartides-workers-releases/releases/download/${WORKER_VERSION}/shoal_linux_${TARGETARCH}" && \
    chmod +x /usr/local/bin/shoal
EXPOSE 8080
CMD ["/usr/local/bin/shoal"]
