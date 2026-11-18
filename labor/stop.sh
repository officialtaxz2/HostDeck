#!/usr/bin/env bash
# Räumt das Prüflabor ab: Container, Netz, Volumes, Weiterleiter.
# labor/lauf (Passwörter, Bilder, Berichte) bleibt; mit --alles auch das.
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
for c in hostdecklab-alt hostdecklab-neu hostdecklab-frisch hostdecklab-dns hostdecklab-pebble; do docker rm -f "$c" >/dev/null 2>&1 && echo "weg: $c"; done
for v in hostdecklab-alt-docker hostdecklab-alt-containerd hostdecklab-neu-docker hostdecklab-neu-containerd hostdecklab-frisch-docker hostdecklab-frisch-containerd; do docker volume rm "$v" >/dev/null 2>&1 && echo "weg: Volume $v"; done
docker network rm hostdecklab >/dev/null 2>&1 && echo "weg: Netz hostdecklab"
pkill -f "weiterleiter.py 10.77.0.1 3128" 2>/dev/null && echo "weg: Weiterleiter"
if [[ "${1:-}" == --alles ]]; then rm -rf "$HERE/lauf" && echo "weg: labor/lauf"; fi
exit 0
