#!/usr/bin/env bash
# Läuft auf dem alten Labor-Server: legt allgemeine Beispielprojekte an, die
# dieselben Muster wie ein echter Server abdecken (Web mit Datenbank, Bind und
# Volume, statische Seite, Einmal-Job, Verwalter mit Docker-Socket, Dienst
# ohne Caddy, Variable aus einem Start-Skript, wackliger Container), dazu
# Caddy und die Server-Ebene. Mehrfach aufrufbar.
set -euo pipefail
R=public.ecr.aws/docker/library
P=/opt/projekte
mkdir -p $P

wait_docker() { for i in $(seq 1 60); do docker info >/dev/null 2>&1 && return; sleep 1; done; echo "Docker startet nicht" >&2; exit 1; }
wait_docker

pull() { docker image inspect "$1" >/dev/null 2>&1 && return; for i in 1 2 3 4 5; do docker pull -q "$1" >/dev/null 2>&1 && return; sleep $((i * 3)); done; docker pull "$1"; }
for img in nginx:alpine postgres:17-alpine mariadb:lts busybox:1.37 alpine:3.22; do
  docker image inspect "$R/$img" >/dev/null 2>&1 || pull "$R/$img"
done

# app: Web (selbst gebautes Image) + PostgreSQL, Daten als Bind-Ordner.
mkdir -p $P/app/web $P/app/data
cat > $P/app/web/Dockerfile <<EOF
FROM $R/nginx:alpine
RUN echo '<h1>app</h1>' > /usr/share/nginx/html/index.html
EOF
cat > $P/app/docker-compose.yml <<EOF
services:
  web:
    build: ./web
    image: labor/app-web:1
    restart: unless-stopped
    ports: ["127.0.0.1:8081:80"]
    depends_on: [db]
  db:
    image: $R/postgres:17-alpine
    restart: unless-stopped
    environment:
      POSTGRES_PASSWORD: labor-app
    volumes: ["./data:/var/lib/postgresql/data"]
EOF

# shop: Web + MariaDB, Daten im Volume.
mkdir -p $P/shop
cat > $P/shop/docker-compose.yml <<EOF
services:
  web:
    image: $R/nginx:alpine
    restart: unless-stopped
    ports: ["127.0.0.1:8082:80"]
  db:
    image: $R/mariadb:lts
    restart: unless-stopped
    environment:
      MARIADB_ROOT_PASSWORD: labor-shop
    volumes: ["shopdb:/var/lib/mysql"]
volumes:
  shopdb: {}
EOF

# blog: statische Seite (wird im Drehbuch „von dir gestoppt“).
mkdir -p $P/blog/html
echo '<h1>blog</h1>' > $P/blog/html/index.html
cat > $P/blog/docker-compose.yml <<EOF
services:
  web:
    image: $R/nginx:alpine
    restart: unless-stopped
    ports: ["127.0.0.1:8083:80"]
    volumes: ["./html:/usr/share/nginx/html:ro"]
EOF

# aufgabe: Einmal-Job (Code 0, Neustart „no“).
mkdir -p $P/aufgabe
cat > $P/aufgabe/docker-compose.yml <<EOF
services:
  job:
    image: $R/busybox:1.37
    restart: "no"
    command: ["sh", "-c", "echo erledigt"]
EOF

# spiel: TCP-Dienst ohne Caddy; spiel.beispiel.test steht nur im DNS.
mkdir -p $P/spiel
cat > $P/spiel/docker-compose.yml <<EOF
services:
  server:
    image: $R/busybox:1.37
    restart: unless-stopped
    ports: ["25565:25565"]
    command: ["sh", "-c", "while true; do echo spiel | nc -l -p 25565; done"]
EOF

# start: eine Variable kommt nur aus einem Start-Skript (Fall vom 05.11.).
mkdir -p $P/start
cat > $P/start/docker-compose.yml <<EOF
services:
  web:
    image: $R/busybox:1.37
    restart: unless-stopped
    environment:
      MODUS: \${START_MODUS:-}
    command: ["sh", "-c", "echo modus=\$\$MODUS; sleep 1000000"]
EOF
cat > $P/start/start.sh <<'EOF'
#!/bin/sh
export START_MODUS=produktiv
cd "$(dirname "$0")" && docker compose up -d
EOF
chmod +x $P/start/start.sh

# wackel: Healthcheck und Neustart-Schleife per Schalterdatei.
mkdir -p $P/wackel/schalter
cat > $P/wackel/docker-compose.yml <<EOF
services:
  dienst:
    image: $R/busybox:1.37
    restart: unless-stopped
    volumes: ["./schalter:/schalter"]
    command: ["sh", "-c", "if [ -f /schalter/absturz ]; then exit 3; fi; sleep 1000000"]
    healthcheck:
      test: ["CMD-SHELL", "[ ! -f /schalter/krank ]"]
      interval: 10s
      retries: 2
EOF

for p in app shop blog aufgabe spiel wackel; do (cd $P/$p && docker compose up -d --quiet-pull >/dev/null 2>&1 || docker compose up -d); done
$P/start/start.sh >/dev/null 2>&1

# panel: Verwalter mit Docker-Socket, legt zwei Container unter seinem
# Ordner an (Muster eines Spiele-Panels mit Wings).
mkdir -p /srv/panel/volumes/0f3a9c1e-1111-4a6b-9c1d-000000000001 /srv/panel/volumes/0f3a9c1e-2222-4a6b-9c1d-000000000002
mkdir -p $P/panel
cat > $P/panel/docker-compose.yml <<EOF
services:
  wings:
    image: $R/alpine:3.22
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /srv/panel:/srv/panel
    command: ["sleep", "1000000"]
EOF
(cd $P/panel && docker compose up -d >/dev/null 2>&1)
for id in 0f3a9c1e-1111-4a6b-9c1d-000000000001 0f3a9c1e-2222-4a6b-9c1d-000000000002; do
  docker container inspect "$id" >/dev/null 2>&1 || docker run -d --name "$id" --restart unless-stopped -v "/srv/panel/volumes/$id:/home/container" $R/busybox:1.37 sleep 1000000 >/dev/null
done

# Caddy mit eigener Zertifikatsstelle des Labors; dazu eine Route ins Leere.
# Nur beim ersten Mal: Später hängen HostDeck (Freischalten) und die Drehbücher an.
if ! grep -q 'acme_ca https://pebble' /etc/caddy/Caddyfile; then
cat > /etc/caddy/Caddyfile <<'EOF'
{
	acme_ca https://pebble:14000/dir
	email labor@beispiel.test
}

app.beispiel.test {
	reverse_proxy 127.0.0.1:8081
}

shop.beispiel.test {
	reverse_proxy 127.0.0.1:8082
}

blog.beispiel.test {
	reverse_proxy 127.0.0.1:8083
}

leer.beispiel.test {
	reverse_proxy 127.0.0.1:8099
}
EOF
systemctl reload caddy || systemctl restart caddy
fi

# Server-Ebene: Cron sichert app, Timer für shop, Cron für ein archiviertes
# Projekt, SSH-Schlüssel, Anmeldung an einer erfundenen Registry.
cat > /etc/cron.d/app-sicherung <<'EOF'
15 2 * * * root tar czf /root/app-daten.tgz -C /opt/projekte/app data
EOF
cat > /etc/cron.d/altprojekt <<'EOF'
0 4 * * 0 root test -d /opt/archiv/altprojekt && find /opt/archiv/altprojekt -name '*.log' -delete
EOF
mkdir -p /opt/archiv/altprojekt && echo alt > /opt/archiv/altprojekt/LIESMICH
cat > /etc/systemd/system/shop-aufraeumen.service <<'EOF'
[Unit]
Description=Shop aufräumen
[Service]
Type=oneshot
ExecStart=/usr/bin/docker exec shop-web-1 sh -c 'rm -rf /tmp/*'
EOF
cat > /etc/systemd/system/shop-aufraeumen.timer <<'EOF'
[Unit]
Description=Shop täglich aufräumen
[Timer]
OnCalendar=daily
[Install]
WantedBy=timers.target
EOF
systemctl daemon-reload && systemctl enable --now shop-aufraeumen.timer >/dev/null 2>&1
mkdir -p /root/.ssh && chmod 700 /root/.ssh
grep -q labor-schluessel /root/.ssh/authorized_keys 2>/dev/null || echo "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ1abore0000000000000000000000000000000000000 labor-schluessel" >> /root/.ssh/authorized_keys
mkdir -p /root/.docker
[ -f /root/.docker/config.json ] || echo '{"auths":{"registry.beispiel.test":{"auth":"bGFib3I6bGFib3I="}}}' > /root/.docker/config.json
echo "alt eingerichtet: $(docker ps -q | wc -l) Container laufen"
