#!/usr/bin/env bash
# Legt den Stand für das öffentliche Repository an: dieselben Dateien wie
# im eingecheckten Stand, ohne das interne Entscheidungsprotokoll und mit
# automatischer CI.
#
#   release/oeffentlich.sh ZIELORDNER
#
# ZIELORDNER muss leer sein oder fehlen. Danach dort: git init, einchecken,
# zum neuen Repository schieben. Die Geschichte des privaten Repositorys
# kommt nicht mit.
set -euo pipefail
cd "$(dirname "$0")/.."

fail() { printf 'Fehler: %s\n' "$*" >&2; exit 1; }

[[ $# -eq 1 ]] || fail "Aufruf: release/oeffentlich.sh ZIELORDNER"
dst=$1
if [[ -e "$dst" ]] && [[ -n "$(ls -A "$dst")" ]]; then
  fail "$dst ist nicht leer."
fi
if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
  fail "Es gibt nicht eingecheckte Änderungen."
fi

# Nur intern: Tagebuch der Entscheidungen und Entwurfsgeschichte der
# Oberfläche, beide mit Namen, Server-Details und Links auf interne Entwürfe.
drop=(docs/entscheidungen.md docs/oberflaeche.md)

mkdir -p "$dst"
git archive --format=tar HEAD | tar -x -C "$dst"
for f in "${drop[@]}"; do rm -f "$dst/$f"; done

# CI läuft öffentlich wieder bei jedem Push und Pull Request.
ci="$dst/.github/workflows/ci.yml"
python3 - "$ci" <<'EOF'
import re, sys
p = sys.argv[1]
s = open(p, encoding="utf-8").read()
head, sep, rest = s.partition("on:\n  workflow_dispatch:\n")
if not sep:
    sys.exit("ci.yml: Abschnitt on: nicht gefunden")
head = re.sub(r"\A(name: CI\n\n)(#.*\n)+", r"\1", head)
s = head + "on:\n  push:\n    branches: [main]\n  pull_request:\n  workflow_dispatch:\n" + rest
open(p, "w", encoding="utf-8").write(s)
EOF

# Nichts darf mehr auf die internen Dateien oder Entwürfe verweisen.
pat='entscheidungen\.md|oberflaeche\.md|claude\.ai/artifact'
if grep -rIlE --exclude=oeffentlich.sh "$pat" "$dst" >/dev/null; then
  grep -rInE --exclude=oeffentlich.sh "$pat" "$dst" >&2
  fail "Verweise auf interne Dateien (siehe oben)."
fi
echo "Öffentlicher Stand liegt in $dst ($(git rev-parse --short=7 HEAD))."
