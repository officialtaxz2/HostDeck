#!/usr/bin/env bash
# Spielt eine HostDeck-Datei auf einem Labor-Server ein und richtet sie ein (beim
# ersten Mal mit Leitstand-Passwort, Backup-Passwort und Freischaltung unter
# hostdeck.beispiel.test).
#   labor/hostdeck-einspielen.sh <alt|neu> <hostdeck-datei>
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
LAUF=$HERE/lauf
n=$1 bin=$2
docker cp "$bin" "hostdecklab-$n:/root/hostdeck"
docker exec "hostdecklab-$n" chmod 755 /root/hostdeck
first=0
docker exec "hostdecklab-$n" test -x /usr/local/lib/hostdeck/hostdeck || first=1
docker exec "hostdecklab-$n" /root/hostdeck einrichten </dev/null
if [[ $first == 1 ]]; then
  [[ -f "$LAUF/leitstand-$n" ]] || openssl rand -base64 24 | tr -d '/+=' | cut -c1-20 > "$LAUF/leitstand-$n"
  docker exec -i "hostdecklab-$n" hostdeck passwort < "$LAUF/leitstand-$n"
  # Backup-Passwort und Freischaltung fragen nach „ja“ im Terminal.
  docker exec "hostdecklab-$n" bash -c 'printf "ja\n" | script -qec "hostdeck backup-passwort" /dev/null' >/dev/null
  if [[ "$n" == alt ]]; then
    docker exec "hostdecklab-$n" bash -c 'printf "ja\n" | script -qec "hostdeck freischalten hostdeck.beispiel.test" /dev/null' | tail -n 2
  fi
fi
