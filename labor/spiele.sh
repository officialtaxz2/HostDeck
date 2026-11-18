#!/usr/bin/env bash
# Legt auf dem alten Labor-Server nachgebaute Spielserver an: drei im Panel-Muster (2 × Minecraft
# Java, 1 × Left 4 Dead 2), einen gestoppten, dessen Container das Panel
# gelöscht hat, und ein Compose-Projekt mit Minecraft. Die Server sind
# labor/spielserver/main.go: Sie beantworten nur die Abfrage der Spiele.
# Mehrfach aufrufbar; „labor/spiele.sh weg“ räumt sie wieder ab.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
LAUF=$HERE/lauf
A=hostdecklab-alt
R=public.ecr.aws/docker/library
V=/srv/panel/volumes
ids=(0f3a9c1e-3333-4a6b-9c1d-000000000003 0f3a9c1e-4444-4a6b-9c1d-000000000004 0f3a9c1e-5555-4a6b-9c1d-000000000005 0f3a9c1e-6666-4a6b-9c1d-000000000006)

if [[ "${1:-}" == weg ]]; then
  docker exec "$A" bash -c "docker rm -f ${ids[*]} >/dev/null 2>&1; (cd /opt/projekte/minecraft 2>/dev/null && docker compose down >/dev/null 2>&1); rm -rf /opt/projekte/minecraft $(printf "$V/%s " "${ids[@]}")" || true
  echo "Spielserver entfernt."
  exit 0
fi

mkdir -p "$LAUF"
(cd "$HERE/.." && CGO_ENABLED=0 go build -o "$LAUF/spielserver" labor/spielserver/main.go)
docker exec "$A" mkdir -p /opt/labor-spiel
docker cp "$LAUF/spielserver" "$A:/opt/labor-spiel/spielserver"
docker exec -i "$A" bash -s -- "${ids[@]}" <<'SH'
set -euo pipefail
R=public.ecr.aws/docker/library
V=/srv/panel/volumes
mc() { # id port motd level
  mkdir -p "$V/$1"
  printf '#Minecraft server properties\nmotd=%s\nserver-port=%s\nlevel-name=%s\nmax-players=20\n' "$3" "$2" "$4" > "$V/$1/server.properties"
}
run() { # id port proto args...
  local id=$1 port=$2 proto=$3; shift 3
  docker container inspect "$id" >/dev/null 2>&1 && return
  # Wie bei Wings liegt alles im Ordner des Servers; nur dieser ist eingebunden.
  cp /opt/labor-spiel/spielserver "$V/$id/spielserver"
  docker run -d --name "$id" --restart unless-stopped -p "$port:$port/$proto" \
    -v "$V/$id:/home/container" $R/busybox:1.37 /home/container/spielserver "$@" >/dev/null
}
mc "$1" 25566 'Survival mit Freunden' welt
run "$1" 25566 tcp -art java -port 25566 -name 'Survival mit Freunden' -spieler 3 -max 20 -version 1.21.4
mc "$2" 25567 'A Minecraft Server' kreativ
run "$2" 25567 tcp -art java -port 25567 -name 'A Minecraft Server' -spieler 0 -max 10 -version 1.20.6
mkdir -p "$V/$3/left4dead2/cfg"
touch "$V/$3/srcds_run" && chmod 755 "$V/$3/srcds_run"
printf '"GameInfo"\n{\n  game "Left 4 Dead 2"\n}\n' > "$V/$3/left4dead2/gameinfo.txt"
printf 'hostname "Labor Coop"\nsv_maxplayers 4\n' > "$V/$3/left4dead2/cfg/server.cfg"
run "$3" 27015 udp -art source -port 27015 -name 'Labor Coop' -spieler 2 -max 4 -karte c1m1_hotel -ordner left4dead2 -spiel 'Left 4 Dead 2' -version 2.2.4.3
# Gestoppt: Das Panel hat den Container beim Stoppen gelöscht, die Daten bleiben.
mc "$4" 25568 'Altes Event' event
docker rm -f "$4" >/dev/null 2>&1 || true
# Compose-Projekt mit Minecraft (Daten unter /data).
mkdir -p /opt/projekte/minecraft/data
printf 'motd=Compose Welt\nserver-port=25570\nlevel-name=world\n' > /opt/projekte/minecraft/data/server.properties
cat > /opt/projekte/minecraft/docker-compose.yml <<YML
services:
  server:
    image: $R/busybox:1.37
    restart: unless-stopped
    ports: ["25570:25570"]
    volumes:
      - ./data:/data
      - /opt/labor-spiel/spielserver:/spielserver:ro
    command: ["/spielserver", "-art", "java", "-port", "25570", "-name", "Compose Welt", "-spieler", "1", "-max", "8", "-version", "1.21.1"]
YML
(cd /opt/projekte/minecraft && docker compose up -d >/dev/null 2>&1 || docker compose up -d)
SH
echo "Spielserver angelegt: 3 im Panel, 1 gestoppt, 1 als Compose-Projekt."
