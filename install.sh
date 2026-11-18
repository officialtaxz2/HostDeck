#!/usr/bin/env bash
# HostDeck installieren: lädt das Programm von GitHub, prüft die Signatur und
# richtet HostDeck als Dienst ein. Danach geht alles Weitere im Browser.
#
#   curl -fsSL https://github.com/officialtaxz2/HostDeck/releases/latest/download/install.sh | sudo bash
#
# Wer das Skript erst lesen will:
#   curl -fsSLO https://github.com/officialtaxz2/HostDeck/releases/latest/download/install.sh
#   less install.sh && sudo bash install.sh
#
# Optionen (hinter „sudo bash -s --“ bzw. „sudo bash install.sh“):
#   --ja             keine Rückfragen; der Hinweis auf neue Versionen ist an
#   --version V      eine bestimmte Version statt der neuesten (auch HOSTDECK_VERSION=V)
#   --trotzdem       auch auf einem System, das HostDeck nicht offiziell unterstützt
#
# Das Skript ändert nichts, bevor du „Weiter?“ bestätigt hast. Alles steht in
# einer Funktion, damit ein abgebrochener Download nichts halb ausführt.

main() {
  set -euo pipefail

  local REPO="officialtaxz2/HostDeck"
  # Öffentlicher Schlüssel der Releases (derselbe wie für die Update-Dateien).
  local SIGNERS='hostdeck namespaces="hostdeck-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKWjAPn14E+qo8aDeTH0gzaR/xekPR2T6/z+72464ZaP'
  # Nur für Tests: von hier statt von GitHub laden.
  local BASE="${HOSTDECK_BASIS:-}"

  local yes=0 anyway=0 want="${HOSTDECK_VERSION:-}"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --ja) yes=1 ;;
      --trotzdem) anyway=1 ;;
      --version)
        [[ $# -ge 2 && -n "$2" && "$2" != -* ]] || fail "Hinter --version fehlt die Versionsnummer, etwa: --version 1.0.0"
        shift; want="$1" ;;
      --version=*) want="${1#--version=}"; [[ -n "$want" ]] || fail "Hinter --version fehlt die Versionsnummer, etwa: --version 1.0.0" ;;
      -h|--help) sed -n '2,17p' "$0" 2>/dev/null || true; return 0 ;;
      *) fail "Unbekannte Angabe: $1 (möglich: --ja, --version V, --trotzdem)" ;;
    esac
    shift
  done
  want="${want#v}"

  say "HostDeck installieren"
  echo

  # --- Vorprüfung: Hier ändert sich noch nichts. ---
  [[ "$(uname -s)" == Linux ]] || fail "HostDeck läuft nur auf Linux-Servern."
  [[ "$(id -u)" == 0 ]] || fail "Bitte mit sudo starten, etwa: curl -fsSL https://github.com/$REPO/releases/latest/download/install.sh | sudo bash"
  [[ -d /run/systemd/system ]] || fail "Auf diesem Server läuft kein systemd. HostDeck braucht systemd."

  local arch
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail "Prozessor $(uname -m) wird nicht unterstützt (nur amd64 und arm64)." ;;
  esac

  local os_id="" os_ver="" os_name="unbekannt"
  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    os_id=$(. /etc/os-release && echo "${ID:-}")
    # shellcheck disable=SC1091
    os_ver=$(. /etc/os-release && echo "${VERSION_ID:-}")
    # shellcheck disable=SC1091
    os_name=$(. /etc/os-release && echo "${PRETTY_NAME:-${ID:-} ${VERSION_ID:-}}")
  fi
  local supported=0
  case "$os_id:$os_ver" in
    debian:12|debian:13|ubuntu:22.04|ubuntu:24.04) supported=1 ;;
  esac
  if [[ $supported == 0 ]]; then
    warn "$os_name ist nicht geprüft. Unterstützt sind Debian 12 und 13 sowie Ubuntu 22.04 und 24.04."
    if [[ $anyway == 0 ]]; then
      if [[ $yes == 1 ]] || ! ask "Trotzdem versuchen? [j/N] " n; then
        fail "Abgebrochen; nichts geändert. (Mit --trotzdem geht es ohne diese Frage weiter.)"
      fi
    fi
  fi

  local get
  if command -v curl >/dev/null 2>&1; then
    get=curl
  elif command -v wget >/dev/null 2>&1; then
    get=wget
  else
    fail "Zum Laden fehlt curl oder wget. Installieren: sudo apt install curl"
  fi
  command -v ssh-keygen >/dev/null 2>&1 || fail "Zum Prüfen der Signatur fehlt ssh-keygen. Installieren: sudo apt install openssh-client"
  command -v sha256sum >/dev/null 2>&1 || fail "sha256sum fehlt (Paket coreutils)."

  # Der Arbeitsordner liegt nicht unter /tmp: Auf gehärteten Servern darf
  # dort nichts ausgeführt werden (noexec), und das geladene Programm muss
  # einmal laufen, um sich einzurichten.
  local tmp
  mkdir -p /usr/local/lib
  tmp=$(mktemp -d /usr/local/lib/hostdeck-install.XXXXXX) || fail "Konnte keinen Arbeitsordner unter /usr/local/lib anlegen."
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT

  # --- Prüfsummen laden und prüfen: Sie sagen auch, welche Version es ist. ---
  local url
  if [[ -n "$BASE" ]]; then
    url="$BASE"
  elif [[ -n "$want" ]]; then
    url="https://github.com/$REPO/releases/download/v$want"
  else
    url="https://github.com/$REPO/releases/latest/download"
  fi
  fetch "$get" "$url/SHA256SUMS" "$tmp/SHA256SUMS" || fail "Die Liste der Dateien ließ sich nicht laden ($url/SHA256SUMS).$( [[ -n "$want" ]] && echo " Gibt es die Version $want?")"
  fetch "$get" "$url/SHA256SUMS.sig" "$tmp/SHA256SUMS.sig" || fail "Die Signatur ließ sich nicht laden ($url/SHA256SUMS.sig)."
  printf '%s\n' "$SIGNERS" > "$tmp/allowed_signers"
  if ! ssh-keygen -Y verify -f "$tmp/allowed_signers" -I hostdeck -n hostdeck-release -s "$tmp/SHA256SUMS.sig" < "$tmp/SHA256SUMS" >/dev/null 2>&1; then
    fail "Die Signatur passt nicht. Die Dateien stammen nicht aus einem Release von HostDeck oder wurden verändert. Nichts geändert."
  fi

  local line file version
  line=$(grep -E "^[0-9a-f]{64}  hostdeck-[0-9A-Za-z.+-]+-linux-$arch\$" "$tmp/SHA256SUMS" | head -n 1 || true)
  [[ -n "$line" ]] || fail "Im Release fehlt das Programm für linux-$arch."
  file=${line#*  }
  version=${file#hostdeck-}
  version=${version%-linux-"$arch"}
  if [[ -n "$want" && "$version" != "$want" ]]; then
    fail "Das Release enthält Version $version statt $want."
  fi

  local now=""
  if [[ -x /usr/local/lib/hostdeck/hostdeck ]]; then
    now=$(/usr/local/lib/hostdeck/hostdeck version 2>/dev/null | awk '{print $2}' || true)
  fi
  if [[ -n "$now" && "$now" == "$version" ]]; then
    say "HostDeck $version ist hier schon eingerichtet. Nichts zu tun."
    echo "Den Leitstand öffnest du wie gewohnt; ohne Freischaltung mit: sudo hostdeck notausgang"
    return 0
  fi

  # --- Plan zeigen, dann fragen. ---
  echo "  Server:    $os_name ($arch)"
  if [[ -n "$now" ]]; then
    echo "  Version:   $version (jetzt eingerichtet: $now)"
  elif [[ -x /usr/local/lib/sbm/sbm ]]; then
    echo "  Version:   $version (übernimmt die frühere Fassung sbm samt Daten)"
  else
    echo "  Version:   $version"
  fi
  echo "  Quelle:    ${BASE:-github.com/$REPO} (Signatur geprüft)"
  echo
  echo "  Das passiert:"
  echo "   1. Das Programm wird geladen und mit der geprüften Prüfsumme verglichen."
  if [[ -n "$now" ]]; then
    echo "   2. HostDeck spielt die Version ein wie ein Update im Leitstand: Die"
    echo "      bisherige Version $now bleibt als Rückfall liegen. Antwortet die neue"
    echo "      nicht, kommt die alte von selbst zurück."
    echo "   3. Daten, Passwort und Freischaltung bleiben, wie sie sind."
  else
    echo "   2. HostDeck richtet sich als Dienst ein: Programm in /usr/local/lib/hostdeck,"
    echo "      Daten in /var/lib/hostdeck, Dienste hostdeck-dienst und hostdeck-web,"
    echo "      ein Benutzer hostdeck-web ohne Rechte für die Website."
    echo "   3. Danach fragt die Einrichtung nach einem Passwort für den Leitstand"
    echo "      und bietet an, ihn über Caddy von außen erreichbar zu machen."
  fi
  echo
  echo "  An deinen Projekten, Containern, an Caddy oder der Firewall ändert sich"
  echo "  nichts, ohne dass du es im Leitstand bestätigst."
  echo "  Entfernen geht jederzeit mit: sudo hostdeck deinstallieren"
  echo
  if [[ $yes == 0 ]] && ! ask "Weiter? [J/n] " j; then
    fail "Abgebrochen; nichts geändert."
  fi

  # --- Programm laden und prüfen. ---
  say "Lade $file …"
  fetch "$get" "$url/$file" "$tmp/$file" || fail "Das Programm ließ sich nicht laden ($url/$file)."
  (cd "$tmp" && printf '%s\n' "$line" | sha256sum -c --status) || fail "Die Prüfsumme des Programms passt nicht. Nichts geändert."
  chmod 755 "$tmp/$file"
  echo "  Prüfsumme stimmt."
  echo

  # --- Einrichten. ---
  local args=(einrichten)
  [[ $yes == 1 ]] && args+=(--versionshinweis an)
  # Zurück auf eine ältere Version geht ohne Rückfrage nur, wenn sie
  # ausdrücklich genannt ist; sonst fragt die Einrichtung oder bricht ab.
  [[ $yes == 1 && -n "$want" ]] && args+=(--aeltere-version)
  if [[ $yes == 0 ]]; then
    "$tmp/$file" "${args[@]}" < /dev/tty
  else
    "$tmp/$file" "${args[@]}" < /dev/null
  fi
}

say() { printf '\033[1m%s\033[0m\n' "$*"; }
warn() { printf 'Hinweis: %s\n' "$*" >&2; }
fail() { printf '\nFehler: %s\n' "$*" >&2; exit 1; }

# ask fragt am Terminal (auch bei curl | bash). $2: Vorgabe bei Enter (j/n).
ask() {
  local a
  if ! { true < /dev/tty; } 2>/dev/null; then
    fail "Keine Eingabe möglich (kein Terminal). Ohne Rückfragen: curl -fsSL … | sudo bash -s -- --ja"
  fi
  printf '%s' "$1" > /dev/tty
  IFS= read -r a < /dev/tty || a=""
  a=${a:-$2}
  [[ "$a" == [jJyY]* ]]
}

# fetch lädt eine Datei; https ist Pflicht, außer bei HOSTDECK_BASIS.
fetch() {
  local tool=$1 from=$2 to=$3
  if [[ "$tool" == curl ]]; then
    if [[ -n "${HOSTDECK_BASIS:-}" ]]; then
      curl -fsSL --retry 3 -o "$to" "$from"
    else
      curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$to" "$from"
    fi
  else
    if [[ -n "${HOSTDECK_BASIS:-}" ]]; then
      wget -q --tries=3 -O "$to" "$from"
    else
      wget -q --https-only --tries=3 -O "$to" "$from"
    fi
  fi
}

main "$@"
