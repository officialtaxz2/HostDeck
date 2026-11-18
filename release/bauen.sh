#!/usr/bin/env bash
# Baut ein Release von HostDeck und signiert es.
#
#   release/bauen.sh VERSION SCHLUESSEL [ORDNER]
#
# VERSION nach SemVer ohne „v“, etwa 1.0.0 oder 1.0.0-rc.1. SCHLUESSEL ist der
# private Schlüssel (nie ins Repository). ORDNER ist das Ziel (Standard:
# dist/VERSION). Danach liegen dort alle Dateien für das GitHub-Release:
#
#   hostdeck-VERSION-linux-amd64, hostdeck-VERSION-linux-arm64   Programm
#   hostdeck-VERSION-linux-ARCH.hdu   Update-Datei für den Leitstand
#   install.sh                        Installationsskript
#   THIRD_PARTY_LICENSES.txt, LICENSE
#   SHA256SUMS, SHA256SUMS.sig        Prüfsummen und ihre Signatur
#
# Das Skript prüft am Ende alles so, wie es ein Server prüfen würde.
set -euo pipefail
cd "$(dirname "$0")/.."

fail() { printf 'Fehler: %s\n' "$*" >&2; exit 1; }

[[ $# -ge 2 && $# -le 3 ]] || fail "Aufruf: release/bauen.sh VERSION SCHLUESSEL [ORDNER]"
ver=$1 key=$2 out=${3:-dist/$1}
[[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || fail "Version $ver ist keine SemVer-Version wie 1.0.0 oder 1.0.0-rc.1."
[[ -r "$key" ]] || fail "Schlüssel $key nicht lesbar."
command -v ssh-keygen >/dev/null || fail "ssh-keygen fehlt (Paket openssh-client)."
if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
  fail "Es gibt nicht eingecheckte Änderungen. Ein Release kommt nur aus einem sauberen Stand."
fi
[[ -e "$out" ]] && fail "$out gibt es schon."

# Die Lizenzliste muss zum Stand passen, sonst fehlt eine Bibliothek.
lic=$(mktemp)
trap 'rm -f "$lic"' EXIT
release/lizenzen.sh > "$lic"
if ! cmp -s "$lic" THIRD_PARTY_LICENSES.txt; then
  fail "THIRD_PARTY_LICENSES.txt ist veraltet. Neu schreiben: release/lizenzen.sh > THIRD_PARTY_LICENSES.txt, einchecken, dann noch einmal."
fi

mkdir -p "$out"
commit=$(git rev-parse --short=7 HEAD)
echo "Baue HostDeck $ver (Stand $commit) nach $out …"
sign() { go run ./cmd/hostdeck-signieren "$@"; }
for arch in amd64 arm64; do
  bin="hostdeck-$ver-linux-$arch"
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.version=$ver" -o "$out/$bin" ./cmd/hostdeck
  sign paket -schluessel "$key" -version "$ver" -os linux -arch "$arch" "$out/$bin" "$out/$bin.hdu"
done
cp install.sh LICENSE THIRD_PARTY_LICENSES.txt "$out/"
chmod 755 "$out"/hostdeck-*-linux-amd64 "$out"/hostdeck-*-linux-arm64 "$out/install.sh"
(cd "$out" && sha256sum hostdeck-* install.sh LICENSE THIRD_PARTY_LICENSES.txt > SHA256SUMS)
sign summen -schluessel "$key" "$out/SHA256SUMS"

# Gegenprobe wie auf einem Server.
allowed=$(mktemp)
trap 'rm -f "$lic" "$allowed"' EXIT
sign pruefzeile > "$allowed"
ssh-keygen -Y verify -f "$allowed" -I hostdeck -n hostdeck-release \
  -s "$out/SHA256SUMS.sig" < "$out/SHA256SUMS" >/dev/null || fail "Die Signatur der Prüfsummen lässt sich nicht prüfen."
(cd "$out" && sha256sum -c --quiet SHA256SUMS) || fail "Prüfsummen stimmen nicht."
if [[ "$(uname -m)" == x86_64 ]]; then
  got=$("$out/hostdeck-$ver-linux-amd64" version | awk '{print $2}')
  [[ "$got" == "$ver" ]] || fail "Das Programm meldet Version $got statt $ver."
fi

echo
echo "Fertig. Für das GitHub-Release (Tag v$ver) alle Dateien aus $out hochladen:"
(cd "$out" && ls -1)
