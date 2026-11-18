# Neuer Server im Prüflabor: Debian 13 mit systemd und sshd, ohne Docker
# und Caddy; beides installiert HostDeck wie beim echten Umzug.
FROM public.ecr.aws/docker/library/debian:trixie
ARG HTTPS_PROXY
COPY extra-ca.crt /usr/local/share/ca-certificates/labor-extra.crt
RUN set -e; if [ -s /usr/local/share/ca-certificates/labor-extra.crt ]; then mkdir -p /etc/ssl/certs; cp /usr/local/share/ca-certificates/labor-extra.crt /etc/ssl/certs/ca-certificates.crt; fi; \
    sed -i 's#http://#https://#g' /etc/apt/sources.list.d/debian.sources; \
    if [ -n "$HTTPS_PROXY" ]; then printf 'Acquire::https::Proxy "%s";\nAcquire::https::CAInfo "/etc/ssl/certs/ca-certificates.crt";\n' "$HTTPS_PROXY" > /etc/apt/apt.conf.d/90labor-proxy; fi; \
    apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates systemd systemd-sysv dbus openssh-server iproute2 procps; \
    update-ca-certificates; rm -f /etc/apt/apt.conf.d/90labor-proxy; apt-get clean; rm -rf /var/lib/apt/lists/*
RUN systemctl enable ssh; \
    sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config; \
    rm -f /etc/machine-id; touch /etc/machine-id
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
