FROM alpine:latest
ARG WORKER_VERSION=v0.1.0
ARG TARGETARCH
RUN mkdir -p /var/log/lunartides && \
    wget -q --no-check-certificate -O /usr/local/bin/flux \
      "https://github.com/woodpeqr/lunartides-workers-releases/releases/download/${WORKER_VERSION}/flux_linux_${TARGETARCH}" && \
    chmod +x /usr/local/bin/flux
EXPOSE 8080
CMD ["/usr/local/bin/flux"]
