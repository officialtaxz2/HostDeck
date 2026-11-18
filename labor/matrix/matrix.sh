#!/usr/bin/env bash
# Prüft Installationsskript und Deinstallieren auf frischen Servern:
# Debian 12 (mit /tmp ohne Ausführrecht) und 13 (nur wget), Ubuntu 22.04 und
# 24.04 ohne Docker, einmal neben nginx, und Ubuntu 24.04 mit Docker, einem
# Compose-Projekt und Caddy.
#
#   labor/matrix/matrix.sh RELEASEORDNER [NAME …]
#   HOSTDECK_ALT=ÄLTERESRELEASE labor/matrix/matrix.sh RELEASEORDNER [NAME …]
#
# RELEASEORDNER kommt von release/bauen.sh. Ohne NAME laufen alle Server.
# Mit HOSTDECK_ALT (ein älteres Release, ebenfalls von release/bauen.sh)
# prüft es zusätzlich das Aktualisieren über eine laufende Installation und
# das Zurückgehen auf die ältere Version.
# Das Skript lädt das Release über HOSTDECK_BASIS von diesem Rechner, nicht
# von GitHub. Ergebnis je Server in labor/lauf/matrix/NAME.log.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
LAUF=$HERE/../lauf/matrix
NET=hostdeckmatrix
SUB=10.78.0
PORT=8099
mkdir -p "$LAUF"

[[ $# -ge 1 && -f "$1/SHA256SUMS" ]] || { echo "Aufruf: labor/matrix/matrix.sh RELEASEORDNER [NAME …]" >&2; exit 2; }
REL=$(cd "$1" && pwd)
shift
relver() { grep -oE 'hostdeck-[^ ]+-linux-amd64$' "$1/SHA256SUMS" | head -n1 | sed 's/^hostdeck-//; s/-linux-amd64$//'; }
VER=$(relver "$REL")
ALT=""
ALTVER=""
if [[ -n "${HOSTDECK_ALT:-}" ]]; then
  ALT=$(cd "$HOSTDECK_ALT" && pwd)
  ALTVER=$(relver "$ALT")
  [[ "$ALTVER" != "$VER" ]] || { echo "HOSTDECK_ALT hat dieselbe Version $VER." >&2; exit 2; }
fi

# name | Image | Pakete | Ladeprogramm
ALLE="
debian12|public.ecr.aws/docker/library/debian:12|curl openssh-client|curl
debian13|public.ecr.aws/docker/library/debian:13|wget openssh-client|wget
ubuntu2204|public.ecr.aws/docker/library/ubuntu:22.04|curl openssh-client|curl
ubuntu2404|public.ecr.aws/docker/library/ubuntu:24.04|curl openssh-client nginx|curl
ubuntu2404docker|public.ecr.aws/docker/library/ubuntu:24.04|curl openssh-client docker.io docker-compose-v2 caddy|curl
"
# Abbild für das Compose-Projekt auf dem Server mit Docker; es kommt von
# diesem Rechner, damit der Server nichts aus dem Netz laden muss.
PROJEKTBILD=${LAB_PROJEKTBILD:-public.ecr.aws/docker/library/alpine:3.22}
want=("$@")

EXTRA_CA=${LAB_CA:-}
if [[ -z "$EXTRA_CA" && -f /root/.ccr/ca-bundle.crt ]]; then EXTRA_CA=/root/.ccr/ca-bundle.crt; fi
if [[ -n "$EXTRA_CA" ]]; then cp "$EXTRA_CA" "$HERE/extra-ca.crt"; else : > "$HERE/extra-ca.crt"; fi

docker network inspect "$NET" >/dev/null 2>&1 || docker network create --subnet "$SUB.0/24" --gateway "$SUB.1" "$NET" >/dev/null

# Das Release und eine verfälschte Kopie davon (für die Prüfung der Signatur).
WEB=$LAUF/web
rm -rf "$WEB"; mkdir -p "$WEB/falsch"
ln -s "$REL" "$WEB/echt"
cp "$REL"/* "$WEB/falsch/"
if [[ -n "$ALT" ]]; then
  # Das ältere Release mit dem install.sh von heute.
  mkdir -p "$WEB/alt"
  cp "$ALT"/* "$WEB/alt/"
  cp "$REL/install.sh" "$WEB/alt/install.sh"
fi
sed -i '1s/^./0/' "$WEB/falsch/SHA256SUMS"
pkill -f "http.server $PORT --bind $SUB.1" 2>/dev/null || true
nohup python3 -m http.server "$PORT" --bind "$SUB.1" --directory "$WEB" >"$LAUF/web.log" 2>&1 &
WEBPID=$!
trap 'kill $WEBPID 2>/dev/null || true' EXIT
sleep 1
BASIS="http://$SUB.1:$PORT/echt"

fails=0
check() { # Beschreibung, Befehl …
  local what=$1; shift
  if "$@" >>"$LOG" 2>&1; then printf '  ok    %s\n' "$what" | tee -a "$LOG"
  else printf '  FEHLT %s\n' "$what" | tee -a "$LOG"; fails=$((fails + 1)); fi
}
x() { docker exec "$C" bash -c "$1"; }
# projekt_anlegen startet auf dem Server ein Compose-Projekt /srv/web1.
projekt_anlegen() {
  docker save "$PROJEKTBILD" | docker exec -i "$C" docker load >/dev/null
  printf 'services:\n  app:\n    image: %s\n    command: ["sleep", "1000000"]\n    restart: unless-stopped\n' "$PROJEKTBILD" |
    docker exec -i "$C" bash -c 'mkdir -p /srv/web1 && cat > /srv/web1/compose.yaml'
  # shellcheck disable=SC2016 # läuft auf dem Server
  x 'cd /srv/web1 && docker compose up -d --pull never && test "$(docker compose ps -q --status running | wc -l)" = 1'
}

ip=10
echo "$ALLE" | while IFS='|' read -r name img pkgs get; do
  [[ -z "$name" ]] && continue
  ip=$((ip + 1))
  if [[ ${#want[@]} -gt 0 && ! " ${want[*]} " == *" $name "* ]]; then continue; fi
  C=hostdeckmatrix-$name
  LOG=$LAUF/$name.log
  : > "$LOG"
  echo "== $name ($img, $pkgs)" | tee -a "$LOG"
  docker build -q --network host --build-arg BASE="$img" --build-arg PAKETE="$pkgs" \
    --build-arg HTTPS_PROXY="${HTTPS_PROXY:-}" -t "hostdeckmatrix-$name" "$HERE" >>"$LOG" 2>&1
  docker rm -f "$C" >/dev/null 2>&1 || true
  extra=()
  # Gehärtete Server hängen /tmp oft ohne Ausführrecht ein.
  [[ "$name" == debian12 ]] && extra+=(--tmpfs /tmp:noexec)
  if [[ "$pkgs" == *docker.io* ]]; then
    # Docker im Container braucht ein eigenes Volume (kein overlay auf overlay).
    docker volume rm -f "$C-docker" "$C-containerd" >/dev/null 2>&1 || true
    extra+=(-v "$C-docker:/var/lib/docker" -v "$C-containerd:/var/lib/containerd")
  fi
  docker run -d --name "$C" --hostname "$name" --network "$NET" --ip "$SUB.$ip" \
    --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock "${extra[@]}" "hostdeckmatrix-$name" >/dev/null
  for _ in $(seq 1 60); do
    s=$(docker exec "$C" systemctl is-system-running 2>/dev/null || true)
    [[ "$s" == running || "$s" == degraded ]] && break
    sleep 1
  done
  if [[ "$get" == curl ]]; then load="curl -fsSL"; else load="wget -qO-"; fi
  if [[ "$pkgs" == *nginx* ]]; then
    # Im Labor gibt es kein IPv6; die Standardseite will auch [::]:80.
    check "nginx läuft vorher" x "sed -i '/listen \[::\]/d' /etc/nginx/sites-enabled/default && systemctl restart nginx && curl -fsS -o /dev/null http://127.0.0.1/"
  fi
  if [[ "$name" == debian12 ]]; then
    check "/tmp ist ohne Ausführrecht eingehängt" x "findmnt -no OPTIONS /tmp | grep -q noexec"
  fi
  if [[ "$pkgs" == *docker.io* ]]; then
    check "Docker läuft vorher" x "for i in \$(seq 1 30); do docker info >/dev/null 2>&1 && exit 0; sleep 1; done; exit 1"
    check "Compose-Projekt web1 läuft vorher" projekt_anlegen
    check "Caddy läuft vorher" x "systemctl is-active caddy && caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1 && sha256sum /etc/caddy/Caddyfile > /root/caddyfile.vorher"
  fi

  check "verfälschte Prüfsummen werden abgelehnt, nichts geändert" \
    x "out=\$($load http://$SUB.1:$PORT/falsch/install.sh | HOSTDECK_BASIS=http://$SUB.1:$PORT/falsch bash -s -- --ja 2>&1); echo \"\$out\"; echo \"\$out\" | grep -q 'Signatur passt nicht' && ! test -e /usr/local/lib/hostdeck"
  check "Hilfe über die Pipe (--help)" \
    x "$load $BASIS/install.sh | bash -s -- --help | grep -q -- '--version'"
  check "zu wenig Platz: abgelehnt, nichts geändert" \
    x "mount -t tmpfs -o size=50m tmpfs /usr/local/lib && out=\$($load $BASIS/install.sh | HOSTDECK_BASIS=$BASIS bash -s -- --ja 2>&1); rc=\$?; umount /usr/local/lib; echo \"\$out\"; test \$rc -ne 0 && echo \"\$out\" | grep -q 'MB frei' && ! test -e /usr/local/bin/hostdeck && ! test -e /usr/local/lib/hostdeck"
  check "Installieren mit --ja ($get)" \
    x "set -o pipefail; $load $BASIS/install.sh | HOSTDECK_BASIS=$BASIS bash -s -- --ja"
  check "Dienste laufen" x "systemctl is-active hostdeck-dienst.service hostdeck-web.service"
  check "Befehl hostdeck meldet $VER" x "test \"\$(hostdeck version)\" = 'hostdeck $VER'"
  if [[ "$get" == curl ]]; then
    check "Leitstand antwortet am Notausgang" x "for i in \$(seq 1 20); do c=\$(curl -sS -o /dev/null -w '%{http_code}' --unix-socket /run/hostdeck-web/notausgang.sock http://leitstand/); [[ \$c =~ ^[1-5][0-9][0-9]\$ ]] && exit 0; sleep 1; done; exit 1"
  else
    check "Leitstand wartet am Notausgang" x "test -S /run/hostdeck-web/notausgang.sock"
  fi
  check "Hinweis auf neue Versionen ist an (--ja)" x "grep -q '\"an\": *true' /var/lib/hostdeck/versionshinweis.json"
  check "zweiter Aufruf ändert nichts" x "$load $BASIS/install.sh | HOSTDECK_BASIS=$BASIS bash -s -- --ja | grep -q 'schon eingerichtet'"
  check "kein Arbeitsordner des Skripts bleibt liegen" x "! ls -d /usr/local/lib/hostdeck-install.* 2>/dev/null"
  if [[ "$pkgs" == *docker.io* ]]; then
    check "Bestandsaufnahme findet das Projekt web1" x "grep -q '\"web1\"' /var/lib/hostdeck/ergebnis/inventory.json && ! grep -q '\"fehlt\": *true' /var/lib/hostdeck/ergebnis/inventory.json"
    check "Caddy und Caddyfile unverändert" x "systemctl is-active caddy && sha256sum -c --status /root/caddyfile.vorher"
    check "Projekt läuft weiter" x "cd /srv/web1 && test \"\$(docker compose ps -q --status running | wc -l)\" = 1"
  fi
  if [[ "$pkgs" == *nginx* ]]; then
    check "nginx läuft unverändert weiter" x "curl -fsS -o /dev/null http://127.0.0.1/ && test -z \"\$(find /etc/nginx -newer /usr/local/lib/hostdeck/hostdeck -type f)\""
  fi
  check "Deinstallieren mit --ja" x "hostdeck deinstallieren --ja"
  check "Dienste, Programm, Befehl und Benutzer sind weg" x "! ls /etc/systemd/system/hostdeck-* 2>/dev/null && ! test -e /usr/local/lib/hostdeck && ! test -e /usr/local/bin/hostdeck && ! id hostdeck-web 2>/dev/null && ! test -e /run/hostdeck"
  check "Daten bleiben" x "test -d /var/lib/hostdeck"
  check "wieder installieren übernimmt die Daten" \
    x "$load $BASIS/install.sh | HOSTDECK_BASIS=$BASIS bash -s -- --ja && systemctl is-active hostdeck-dienst"
  check "Deinstallieren mit Daten" x "/usr/local/bin/hostdeck deinstallieren --ja --daten-loeschen && ! test -e /var/lib/hostdeck && ! test -e /root/.hostdeck"
  if [[ "$pkgs" == *nginx* ]]; then
    check "nginx läuft danach weiter" x "systemctl is-active nginx && curl -fsS -o /dev/null http://127.0.0.1/"
  fi
  # Wie ein Mensch am Terminal: mit allen Rückfragen.
  # Mit Caddy bietet die Einrichtung zum Schluss die Freischaltung an.
  freischalten=()
  [[ "$pkgs" == *caddy* ]] && freischalten=('Adresse eingeben, oder n für später=n')
  check "Installieren mit Rückfragen (Passwörter, Versionshinweis aus)" \
    "$HERE/dialog.py" "$C" "$load $BASIS/install.sh | HOSTDECK_BASIS=$BASIS bash" \
    'Weiter\? \[J/n\]=' 'Enter übernimmt den Vorschlag=' 'Hast du den Vorschlag gespeichert\? Dann tippe ja=ja' \
    'Hast du es gespeichert\? Dann tippe ja=ja' 'Einschalten\? \[J/n\]=n' "${freischalten[@]}" 'Fertig\. HostDeck='
  check "Versionshinweis ist aus" x "grep -q '\"an\": *false' /var/lib/hostdeck/versionshinweis.json"
  check "Deinstallieren mit Rückfragen, Daten bleiben" \
    "$HERE/dialog.py" "$C" "hostdeck deinstallieren" 'HostDeck entfernen\? \[j/N\]=j' 'Daten auch löschen\?=' 'HostDeck ist entfernt='
  check "danach: Programm weg, Daten da" x "! test -e /usr/local/lib/hostdeck && test -d /var/lib/hostdeck"
  if [[ "$pkgs" == *docker.io* ]]; then
    check "Projekt und Caddy laufen nach dem Entfernen weiter" x "systemctl is-active caddy && sha256sum -c --status /root/caddyfile.vorher && cd /srv/web1 && test \"\$(docker compose ps -q --status running | wc -l)\" = 1"
  fi
  if [[ -n "$ALT" ]]; then
    ALTBASIS="http://$SUB.1:$PORT/alt"
    inst() { echo "$load $1/install.sh | HOSTDECK_BASIS=$1 bash -s -- --ja ${2:-}"; }
    check "ältere Version $ALTVER einrichten" x "$(inst "$ALTBASIS") && test \"\$(hostdeck version)\" = 'hostdeck $ALTVER'"
    check "Aktualisieren auf $VER geht den Update-Weg" x "out=\$($(inst "$BASIS") 2>&1); echo \"\$out\"; echo \"\$out\" | grep -q 'Update eingespielt: Version $VER (vorher $ALTVER)' && test \"\$(hostdeck version)\" = 'hostdeck $VER' && test -x /usr/local/lib/hostdeck/hostdeck.alt && systemctl is-active hostdeck-dienst hostdeck-web"
    check "Daten bleiben beim Aktualisieren" x "test -s /var/lib/hostdeck/versionshinweis.json && grep -q 'Update eingespielt: Version $VER' /var/lib/hostdeck/protokoll*"
    check "ältere Version ohne --version: abgelehnt, nichts geändert" x "out=\$($(inst "$ALTBASIS") 2>&1); rc=\$?; echo \"\$out\"; test \$rc -ne 0 && echo \"\$out\" | grep -q 'ältere Version $ALTVER' && test \"\$(hostdeck version)\" = 'hostdeck $VER'"
    check "ältere Version mit --version $ALTVER: geht zurück" x "$(inst "$ALTBASIS" "--version $ALTVER") && test \"\$(hostdeck version)\" = 'hostdeck $ALTVER' && systemctl is-active hostdeck-dienst"
    check "und wieder auf $VER" x "$(inst "$BASIS") && test \"\$(hostdeck version)\" = 'hostdeck $VER'"
    check "Aufräumen" x "hostdeck deinstallieren --ja --daten-loeschen"
  fi
  echo "$fails" > "$LAUF/$name.fails"
done

total=0
for f in "$LAUF"/*.fails; do total=$((total + $(cat "$f"))); done
rm -f "$LAUF"/*.fails
echo
if [[ $total -eq 0 ]]; then echo "Alle Prüfungen bestanden."; else echo "$total Prüfungen fehlgeschlagen; Einzelheiten in $LAUF/*.log"; exit 1; fi
