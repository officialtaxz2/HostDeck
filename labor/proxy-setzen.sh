#!/usr/bin/env bash
# Läuft auf einem Labor-Server: Proxy der Bauumgebung für Dienste, Anmeldungen
# und apt eintragen (nur, wenn es einen gibt). Auf einem echten Server ist
# nichts davon nötig.
set -euo pipefail
P=${1:-}
NP="localhost,127.0.0.1,10.77.0.0/24,beispiel.test,.beispiel.test,pebble"
[[ -z "$P" ]] && exit 0
cat > /etc/environment <<E2
https_proxy=$P
HTTPS_PROXY=$P
no_proxy=$NP
NO_PROXY=$NP
E2
printf 'Acquire::https::Proxy "%s";\nAcquire::https::CAInfo "/etc/ssl/certs/ca-certificates.crt";\n' "$P" > /etc/apt/apt.conf.d/90labor-proxy
mkdir -p /etc/systemd/system.conf.d
printf '[Manager]\nDefaultEnvironment="HTTPS_PROXY=%s" "https_proxy=%s" "NO_PROXY=%s" "no_proxy=%s"\n' "$P" "$P" "$NP" "$NP" > /etc/systemd/system.conf.d/labor-proxy.conf
systemctl daemon-reexec
if systemctl is-active -q docker 2>/dev/null; then systemctl restart docker; fi
