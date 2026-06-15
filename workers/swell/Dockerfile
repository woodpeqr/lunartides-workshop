FROM alpine:latest
ARG WORKER_VERSION=v0.1.0
ARG TARGETARCH
RUN mkdir -p /var/log/lunartides && \
    wget -q --no-check-certificate -O /usr/local/bin/swell \
      "https://github.com/woodpeqr/lunartides-workers-releases/releases/download/${WORKER_VERSION}/swell_linux_${TARGETARCH}" && \
    chmod +x /usr/local/bin/swell
EXPOSE 8080
EXPOSE 9090
CMD ["/usr/local/bin/swell"]
