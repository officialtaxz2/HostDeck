#!/usr/bin/env bash
# Schreibt die Lizenztexte aller Go-Bibliotheken, die im Programm HostDeck
# stecken, nach STDOUT (für THIRD_PARTY_LICENSES.txt im Release).
#   release/lizenzen.sh > THIRD_PARTY_LICENSES.txt
set -euo pipefail
cd "$(dirname "$0")/.."
self=$(go list -m)
printf 'Lizenzen der Bibliotheken in HostDeck\n=====================================\n\n'
printf 'HostDeck selbst steht unter der MIT-Lizenz (LICENSE). Die Schriften Outfit und\n'
printf 'JetBrains Mono stehen unter der SIL Open Font License 1.1; ihre Texte liegen\n'
printf 'in internal/web/static/fonts. restic lädt HostDeck bei Bedarf als eigenes\n'
printf 'Programm von GitHub (BSD-2-Clause), es steckt nicht in HostDeck.\n\n'
printf -- '------------------------------------------------------------------------\nGo (Standardbibliothek und Laufzeit) %s\n------------------------------------------------------------------------\n\n' "$(go env GOVERSION)"
cat "$(go env GOROOT)/LICENSE"; printf '\n'
GOOS=linux go list -deps -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}} {{.Module.Dir}}{{end}}' ./cmd/hostdeck | sort -u |
while read -r path ver dir; do
  [[ "$path" == "$self" ]] && continue
  lic=$(find "$dir" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) | sort)
  if [[ -z "$lic" ]]; then echo "Keine Lizenzdatei für $path" >&2; exit 1; fi
  printf -- '------------------------------------------------------------------------\n%s %s\n------------------------------------------------------------------------\n\n' "$path" "$ver"
  for f in $lic; do cat "$f"; printf '\n'; done
done
