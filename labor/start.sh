#!/usr/bin/env bash
# Startet das Prüflabor: zwei nachgebaute Server (alt: Ubuntu 24.04 mit
# Docker und Caddy, neu: Debian 13 ohne beides), einen eigenen DNS-Server für
# die Zone beispiel.test und eine eigene Zertifikatsstelle (Pebble).
# Nichts davon spricht mit echten Servern oder echtem DNS. Siehe LIESMICH.md.
#
#   labor/start.sh            alles starten (Images nur bauen, wenn sie fehlen)
#   LAB_REBUILD=1 labor/start.sh   Images neu bauen
#   LAB_HOSTDECK=/pfad/hostdeck labor/start.sh   diese HostDeck-Datei einspielen (sonst go build)
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/.." && pwd)
LAUF=$HERE/lauf
NET=hostdecklab
SUB=10.77.0
mkdir -p "$LAUF"
chmod 700 "$LAUF"
say() { printf '\n== %s\n' "$*"; }

# Proxy der Bauumgebung (etwa in einer Cloud-Sitzung): Die Labor-Server
# erreichen ihn über das Gateway des Labor-Netzes. Ohne Proxy gehen sie direkt
# ins Netz.
PROXY_HOSTPORT=""
if [[ "${HTTPS_PROXY:-}" =~ ^http://(127\.0\.0\.1|localhost):([0-9]+)/?$ ]]; then
  PROXY_HOSTPORT="127.0.0.1:${BASH_REMATCH[2]}"
fi
EXTRA_CA=${LAB_CA:-}
if [[ -z "$EXTRA_CA" && -f /root/.ccr/ca-bundle.crt ]]; then EXTRA_CA=/root/.ccr/ca-bundle.crt; fi

say "Images"
for img in alt neu; do
  if [[ -n "${LAB_REBUILD:-}" ]] || ! docker image inspect "hostdecklab-$img" >/dev/null 2>&1; then
    if [[ -n "$EXTRA_CA" ]]; then cp "$EXTRA_CA" "$HERE/images/$img/extra-ca.crt"; else : > "$HERE/images/$img/extra-ca.crt"; fi
    docker build --network host --build-arg HTTPS_PROXY="${HTTPS_PROXY:-}" -t "hostdecklab-$img" "$HERE/images/$img"
  fi
done

say "Netz $NET ($SUB.0/24)"
docker network inspect "$NET" >/dev/null 2>&1 || docker network create --subnet "$SUB.0/24" --gateway "$SUB.1" "$NET" >/dev/null

LAB_PROXY=""
if [[ -n "$PROXY_HOSTPORT" ]]; then
  if ! pgrep -f "weiterleiter.py $SUB.1 3128" >/dev/null; then
    nohup python3 "$HERE/weiterleiter.py" "$SUB.1" 3128 "${PROXY_HOSTPORT%:*}" "${PROXY_HOSTPORT#*:}" >"$LAUF/weiterleiter.log" 2>&1 &
    sleep 1
  fi
  LAB_PROXY="http://$SUB.1:3128"
fi
echo "$LAB_PROXY" > "$LAUF/proxy"

say "DNS (CoreDNS, Zone beispiel.test)"
if [[ ! -f "$LAUF/zone.txt" ]]; then
  cat > "$LAUF/zone.txt" <<EOF
ns1 A $SUB.53
hostdeck A $SUB.10
app A $SUB.10
shop A $SUB.10
blog A $SUB.10
spiel A $SUB.10
@ A $SUB.10
mail A $SUB.10
@ MX 10|mail.beispiel.test.
_minecraft._tcp SRV 0|5|25565|spiel.beispiel.test.
EOF
fi
cat > "$LAUF/Corefile" <<'EOF'
beispiel.test:53 {
    file /zone/beispiel.test.zone {
        reload 2s
    }
    log
}
.:53 {
    forward . 127.0.0.11
}
EOF
"$HERE/dns.sh" schreiben
# Nach einem Neustart des Rechners stehen die Container noch, aber gestoppt.
weckt() { [[ "$(docker container inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" == false ]] && docker start "$1" >/dev/null; return 0; }
weckt hostdecklab-dns
if ! docker container inspect hostdecklab-dns >/dev/null 2>&1; then
  docker run -d --name hostdecklab-dns --network "$NET" --ip "$SUB.53" \
    -v "$LAUF/Corefile:/Corefile:ro" -v "$LAUF/zone:/zone:ro" \
    mirror.gcr.io/coredns/coredns:1.12.1 -conf /Corefile >/dev/null
fi

say "Zertifikatsstelle (Pebble)"
# Nur das Profil mit 90 Tagen: Caddy wählt sonst das kurzlebige, und jede
# Seite meldete „Zertifikat läuft in 5 Tagen ab“ (Labor-Artefakt).
cat > "$LAUF/pebble-config.json" <<'EOF'
{"pebble": {"listenAddress": "0.0.0.0:14000", "managementListenAddress": "0.0.0.0:15000",
  "certificate": "test/certs/localhost/cert.pem", "privateKey": "test/certs/localhost/key.pem",
  "httpPort": 5002, "tlsPort": 5001, "ocspResponderURL": "", "externalAccountBindingRequired": false,
  "retryAfter": {"authz": 3, "order": 5},
  "profiles": {"default": {"description": "90 Tage", "validityPeriod": 7776000}}}}
EOF
chmod 644 "$LAUF/pebble-config.json"
weckt hostdecklab-pebble
if ! docker container inspect hostdecklab-pebble >/dev/null 2>&1; then
  docker run -d --name hostdecklab-pebble --network "$NET" --ip "$SUB.60" \
    -e PEBBLE_VA_ALWAYS_VALID=1 -e PEBBLE_VA_NOSLEEP=1 -v "$LAUF/pebble-config.json:/labor/pebble.json:ro" \
    ghcr.io/letsencrypt/pebble:2.7.0 -config /labor/pebble.json -dnsserver "$SUB.53:53" >/dev/null
  sleep 2
fi
docker cp hostdecklab-pebble:/test/certs/pebble.minica.pem "$LAUF/pebble-minica.pem" >/dev/null
# Wurzel, mit der Pebble die Zertifikate ausstellt (neu bei jedem Start).
for i in $(seq 1 20); do
  if curl -fsS --noproxy '*' --cacert "$LAUF/pebble-minica.pem" --resolve "pebble:15000:$SUB.60" https://pebble:15000/roots/0 -o "$LAUF/pebble-root.pem"; then break; fi
  sleep 1
done
# Frühere Wurzeln bleiben vertraut: Nach einem Neustart von Pebble tragen die
# schon ausgestellten Zertifikate noch die alte.
touch "$LAUF/labor-ca.pem"
for f in "$LAUF/pebble-root.pem" "$LAUF/pebble-minica.pem"; do
  grep -qF "$(sed -n 2p "$f")" "$LAUF/labor-ca.pem" || cat "$f" >> "$LAUF/labor-ca.pem"
done

WIEDER=""
server() { # name ip image
  local name=$1 ip=$2 img=$3
  if docker container inspect "hostdecklab-$name" >/dev/null 2>&1; then weckt "hostdecklab-$name"; WIEDER=1; return; fi
  docker run -d --name "hostdecklab-$name" --hostname "$name" --network "$NET" --ip "$ip" --dns "$SUB.53" \
    --add-host pebble:"$SUB.60" \
    --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock \
    --tmpfs /srv/daten:size=64m \
    -v "hostdecklab-$name-docker:/var/lib/docker" -v "hostdecklab-$name-containerd:/var/lib/containerd" \
    "hostdecklab-$img" >/dev/null
}
say "Server alt ($SUB.10) und neu ($SUB.20)"
server alt "$SUB.10" alt
server neu "$SUB.20" neu
for n in alt neu; do
  for i in $(seq 1 60); do
    s=$(docker exec "hostdecklab-$n" systemctl is-system-running 2>/dev/null || true)
    [[ "$s" == running || "$s" == degraded ]] && break
    sleep 1
  done
  echo "$n: systemd $s"
done

# Gemeinsam: Wurzel von Pebble vertrauen, Proxy (falls nötig), root-Passwort.
for n in alt neu; do
  docker cp "$LAUF/labor-ca.pem" "hostdecklab-$n:/usr/local/share/ca-certificates/labor-ca.crt"
  docker exec "hostdecklab-$n" update-ca-certificates >/dev/null 2>&1
  [[ -f "$LAUF/root-$n" ]] || openssl rand -base64 18 | tr -d '/+=' > "$LAUF/root-$n"
  docker exec -i "hostdecklab-$n" chpasswd <<<"root:$(cat "$LAUF/root-$n")"
  docker exec -i "hostdecklab-$n" bash -s "$LAB_PROXY" < "$HERE/proxy-setzen.sh"
done

if [[ -n "$WIEDER" && -z "${LAB_EINRICHTEN:-}" ]]; then
  say "Labor läuft wieder (Stand von vorher; neu einrichten mit LAB_EINRICHTEN=1)"
  exit 0
fi

say "alt einrichten (Beispielprojekte, Caddy, Server-Ebene)"
docker cp "$HERE/alt/." hostdecklab-alt:/root/labor/
docker exec hostdecklab-alt bash /root/labor/einrichten.sh

say "HostDeck auf alt"
HOSTDECK=${LAB_HOSTDECK:-}
if [[ -z "$HOSTDECK" ]]; then
  HOSTDECK="$LAUF/hostdeck"
  (cd "$REPO" && CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=labor-$(git rev-parse --short HEAD)" -o "$HOSTDECK" ./cmd/hostdeck)
fi
"$HERE/hostdeck-einspielen.sh" alt "$HOSTDECK"

say "Fertig"
cat <<EOF
Leitstand alt:   https://hostdeck.beispiel.test  (Passwort: labor/lauf/leitstand-alt)
neuer Server:    $SUB.20, root, Passwort in labor/lauf/root-neu
DNS ändern:      labor/dns.sh setzen <name> <ip>
Browser:         node labor/browser/... (siehe LIESMICH.md)
EOF
