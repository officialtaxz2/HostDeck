#!/usr/bin/env bash
# DNS im Prüflabor: Die Zone beispiel.test liegt in lauf/zone.txt (eine Zeile
# je Eintrag: name typ wert [ttl]); CoreDNS lädt sie alle 2 Sekunden neu.
# „DNS umstellen“ im Drehbuch heißt: hier einen Eintrag ändern.
#
#   labor/dns.sh zeigen
#   labor/dns.sh setzen <name> <wert> [typ=A] [ttl=60]
#     (Werte mit Leerzeichen, etwa SRV oder MX, mit | statt Leerzeichen:
#      labor/dns.sh setzen _minecraft._tcp '0|5|25565|spiel.beispiel.test.' SRV)
#   labor/dns.sh loeschen <name> [typ]
#   labor/dns.sh schreiben            (Zonendatei neu erzeugen)
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
LAUF=$HERE/lauf
Z=$LAUF/zone.txt
mkdir -p "$LAUF/zone"
schreiben() {
  local out=$LAUF/zone/beispiel.test.zone.neu
  # CoreDNS lädt nur bei höherer Seriennummer neu; mehrere Änderungen in
  # derselben Sekunde brauchen deshalb trotzdem eine neue Nummer.
  local serial last
  serial=$(date +%s)
  last=$(cat "$LAUF/zone/serial" 2>/dev/null || echo 0)
  (( serial > last )) || serial=$((last + 1))
  echo "$serial" > "$LAUF/zone/serial"
  {
    echo '$ORIGIN beispiel.test.'
    echo '$TTL 60'
    echo "@ IN SOA ns1.beispiel.test. labor.beispiel.test. ( $serial 60 60 600 60 )"
    echo '@ IN NS ns1.beispiel.test.'
    while read -r name typ wert ttl; do
      [[ -z "${name:-}" || "$name" == \#* ]] && continue
      echo "$name ${ttl:-60} IN $typ ${wert//|/ }"
    done < "$Z"
  } > "$out"
  chmod 644 "$out"
  mv "$out" "$LAUF/zone/beispiel.test.zone"
}
case "${1:-zeigen}" in
  zeigen) cat "$Z" ;;
  schreiben) schreiben ;;
  setzen)
    name=$2 wert=$3 typ=${4:-A} ttl=${5:-}
    grep -v -E "^$name $typ " "$Z" > "$Z.neu" || true
    echo "$name $typ $wert $ttl" >> "$Z.neu"
    mv "$Z.neu" "$Z"; schreiben ;;
  loeschen)
    name=$2 typ=${3:-}
    if [[ -n "$typ" ]]; then grep -v -E "^$name $typ " "$Z" > "$Z.neu" || true; else grep -v -E "^$name " "$Z" > "$Z.neu" || true; fi
    mv "$Z.neu" "$Z"; schreiben ;;
  *) echo "unbekannt: $1" >&2; exit 2 ;;
esac
