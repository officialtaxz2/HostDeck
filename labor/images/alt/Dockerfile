# Alter Server im Prüflabor: Ubuntu 24.04 mit systemd, sshd, Docker und
# Caddy als Systemdienst. Baut start.sh; extra-ca.crt ist leer oder die
# Zertifikatsstelle eines Proxys der Bauumgebung (nie im Repo).
FROM public.ecr.aws/docker/library/ubuntu:24.04
ARG HTTPS_PROXY
COPY extra-ca.crt /usr/local/share/ca-certificates/labor-extra.crt
RUN set -e; if [ -s /usr/local/share/ca-certificates/labor-extra.crt ]; then mkdir -p /etc/ssl/certs; cp /usr/local/share/ca-certificates/labor-extra.crt /etc/ssl/certs/ca-certificates.crt; fi; \
    sed -i 's#http://#https://#g' /etc/apt/sources.list.d/ubuntu.sources; \
    if [ -n "$HTTPS_PROXY" ]; then printf 'Acquire::https::Proxy "%s";\nAcquire::https::CAInfo "/etc/ssl/certs/ca-certificates.crt";\n' "$HTTPS_PROXY" > /etc/apt/apt.conf.d/90labor-proxy; fi; \
    apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates systemd systemd-sysv dbus openssh-server curl gnupg cron iproute2 procps sudo less python3 dnsutils; \
    update-ca-certificates
RUN curl -fsSL https://get.docker.com -o /tmp/get-docker.sh && sh /tmp/get-docker.sh && rm /tmp/get-docker.sh
RUN set -e; curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg; \
    echo "deb [signed-by=/usr/share/keyrings/caddy-stable-archive-keyring.gpg] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main" > /etc/apt/sources.list.d/caddy-stable.list; \
    apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y caddy; \
    rm -f /etc/apt/apt.conf.d/90labor-proxy; apt-get clean; rm -rf /var/lib/apt/lists/*
RUN systemctl enable ssh docker caddy cron; \
    sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config; \
    rm -f /etc/machine-id; touch /etc/machine-id
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
