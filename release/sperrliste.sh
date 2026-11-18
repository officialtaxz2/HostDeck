#!/usr/bin/env bash
# Sucht vor dem Hochladen nach Dingen, die nicht öffentlich werden sollen:
# eigene Domains, IP-Adressen, Namen, Mailadressen. Die Liste dazu liegt
# außerhalb des Repositorys, sonst stünde gerade das darin, was geheim
# bleiben soll.
#
#   release/sperrliste.sh LISTE                 Arbeitsstand prüfen
#   release/sperrliste.sh LISTE VON..BIS        jeden Commit im Bereich prüfen
#   release/sperrliste.sh --einrichten LISTE    vor jedem git push prüfen
#
# LISTE: eine Zeichenkette je Zeile, Groß-/Kleinschreibung egal, Zeilen mit #
# und leere Zeilen zählen nicht. Geprüft werden Dateiinhalte und Dateinamen,
# bei Commits auch Commit-Text, Autor und Committer. Private Schlüssel fallen
# immer auf, auch ohne Eintrag in der Liste.
#
# --einrichten legt einen pre-push-Hook an (nur in dieser Kopie, nicht im
# Repository). Danach bricht git push ab, wenn ein neuer Commit etwas aus der
# Liste enthält.
set -euo pipefail
cd "$(dirname "$0")/.."

fail() { printf 'Fehler: %s\n' "$*" >&2; exit 1; }
usage() { fail "Aufruf: release/sperrliste.sh LISTE [VON..BIS] | --einrichten LISTE | --push"; }

top=$(pwd -P)

# load prüft LISTE und schreibt die Suchbegriffe nach $pats.
pats=$(mktemp)
trap 'rm -f "$pats"' EXIT
load() {
  local f=$1 abs
  [[ -r "$f" ]] || fail "Sperrliste $f nicht lesbar."
  abs=$(cd "$(dirname "$f")" && pwd -P)/$(basename "$f")
  [[ "$abs" == "$top"/* ]] && fail "Die Sperrliste liegt im Repository ($f). Sie gehört nach außerhalb."
  grep -vE '^[[:space:]]*(#|$)' "$f" | sed 's/[[:space:]]*$//' > "$pats" || true
  [[ -s "$pats" ]] || fail "Sperrliste $f ist leer."
  # Private Schlüssel immer, auch ohne Listeneintrag.
  printf '%s %s\n' PRIVATE 'KEY-----' >> "$pats"
}

found=0
hit() { found=1; printf '%s\n' "$@" >&2; }

# tree prüft einen Stand: Inhalte und Pfade. Ohne Commit den Arbeitsstand
# (eingecheckt und neu, ohne Ignoriertes).
tree() {
  local rev=${1:-} out
  if [[ -n "$rev" ]]; then
    out=$(git grep -I -i -n -F -f "$pats" "$rev" -- . || true)
    [[ -n "$out" ]] && hit "$out"
    out=$(git ls-tree -r --name-only "$rev" | grep -i -F -f "$pats" || true)
  else
    out=$(git grep -I -i -n -F -f "$pats" --untracked -- . || true)
    [[ -n "$out" ]] && hit "$out"
    out=$(git ls-files --cached --others --exclude-standard | grep -i -F -f "$pats" || true)
  fi
  [[ -n "$out" ]] && hit "Dateiname: $out"
  return 0
}

# commits prüft jeden Commit eines Bereichs, auch Text und Autor.
commits() {
  local c out
  for c in $(git rev-list "$@"); do
    tree "$c"
    out=$(git log -1 --format='%an <%ae>%n%cn <%ce>%n%B' "$c" | grep -i -F -f "$pats" || true)
    [[ -n "$out" ]] && hit "Commit $(git rev-parse --short=7 "$c"), Text oder Autor: $out"
  done
  return 0
}

verdict() {
  if [[ $found -ne 0 ]]; then
    fail "Fundstellen aus der Sperrliste (siehe oben). Nichts hochladen, erst bereinigen."
  fi
  echo "Sperrliste: nichts gefunden ($1)."
}

case "${1:-}" in
  --einrichten)
    [[ $# -eq 2 ]] || usage
    load "$2"
    abs=$(cd "$(dirname "$2")" && pwd -P)/$(basename "$2")
    git config hostdeck.sperrliste "$abs"
    hook=$(git rev-parse --git-path hooks)/pre-push
    [[ -e "$hook" ]] && ! grep -q 'release/sperrliste.sh' "$hook" && fail "$hook gibt es schon und ist nicht von hier."
    # shellcheck disable=SC2016 # \$(...) soll erst im Hook laufen.
    printf '#!/bin/sh\n# Angelegt von release/sperrliste.sh --einrichten.\nexec "$(git rev-parse --show-toplevel)/release/sperrliste.sh" --push\n' > "$hook"
    chmod 755 "$hook"
    echo "Eingerichtet: git push prüft jetzt gegen $abs."
    ;;
  --push)
    # Als pre-push-Hook: auf stdin je Ref „lokal sha entfernt sha“.
    list=$(git config hostdeck.sperrliste || true)
    [[ -n "$list" ]] || fail "Keine Sperrliste eingerichtet (release/sperrliste.sh --einrichten LISTE)."
    load "$list"
    zero=0000000000000000000000000000000000000000
    while read -r _ lsha _ rsha; do
      [[ "$lsha" == "$zero" ]] && continue
      if [[ "$rsha" == "$zero" ]]; then
        commits "$lsha" --not --remotes
      else
        commits "$rsha..$lsha"
      fi
    done
    verdict "neue Commits"
    ;;
  ''|-*) usage ;;
  *)
    [[ $# -le 2 ]] || usage
    load "$1"
    if [[ $# -eq 2 ]]; then
      commits "$2"
      verdict "Commits $2"
    else
      tree
      verdict "Arbeitsstand"
    fi
    ;;
esac
