#!/usr/bin/env bash
# AIO CPU-Messung: Geekbench 6 + Sysbench (1 Thread / alle vCPUs) + mpstat.
# Zusaetzlich: vmstat 1 30 waehrend der Multi-Core-Last, ZIP mit Supportdaten.
# Standard: 120 Sekunden pro Sysbench-Test, 5 Sekunden Vor-/Nachlauf.
# Debian/Ubuntu: installiert fehlende Pakete und Geekbench 6.7.1 bei Bedarf automatisch.
# GEEKBENCH=0 laesst Geekbench weg (kein Download, kein Upload; auch fuer arm64).
set -Eeuo pipefail
umask 077
export LC_ALL=C

SYSBENCH_SECONDS="${SYSBENCH_SECONDS:-120}"
SYSBENCH_THREADS="${SYSBENCH_THREADS:-}"
SYSBENCH_PRIME="${SYSBENCH_PRIME:-20000}"
BASELINE_SECONDS="${BASELINE_SECONDS:-5}"
AFTER_SECONDS="${AFTER_SECONDS:-5}"
PAUSE_SECONDS="${PAUSE_SECONDS:-10}"
GEEKBENCH="${GEEKBENCH:-1}"

for v in SYSBENCH_SECONDS SYSBENCH_PRIME; do
    if ! [[ ${!v} =~ ^[1-9][0-9]*$ ]]; then
        printf 'Fehler: %s muss eine positive ganze Zahl sein.\n' "$v" >&2
        exit 2
    fi
done
for v in BASELINE_SECONDS AFTER_SECONDS PAUSE_SECONDS; do
    if ! [[ ${!v} =~ ^[0-9]+$ ]]; then
        printf 'Fehler: %s muss eine nichtnegative ganze Zahl sein.\n' "$v" >&2
        exit 2
    fi
done
if ! [[ $GEEKBENCH =~ ^[01]$ ]]; then
    printf 'Fehler: GEEKBENCH muss 0 oder 1 sein.\n' >&2
    exit 2
fi

# Einrichtung: nur fehlende Programme aus den DISTRIBUTIONS-Paketquellen.
# Keine Repository-Skripte Dritter und kein ungefragtes Upgrade vorhandener Pakete.
NEEDED_PACKAGES=()
add_package_if_missing() {
    local command_name="$1" package_name="$2" existing
    if command -v "$command_name" >/dev/null 2>&1; then return; fi
    for existing in "${NEEDED_PACKAGES[@]}"; do
        [[ "$existing" == "$package_name" ]] && return
    done
    NEEDED_PACKAGES+=("$package_name")
}
add_package_if_missing sysbench sysbench
add_package_if_missing mpstat sysstat
add_package_if_missing vmstat procps
add_package_if_missing zip zip
add_package_if_missing unzip unzip
add_package_if_missing curl curl
add_package_if_missing tar tar
add_package_if_missing lscpu util-linux
add_package_if_missing nproc coreutils
add_package_if_missing stdbuf coreutils
add_package_if_missing tee coreutils
add_package_if_missing mktemp coreutils
add_package_if_missing awk mawk
add_package_if_missing sed sed
add_package_if_missing grep grep
# Fuer den verifizierten HTTPS-Download auf sehr schlanken Server-Images.
if [[ ! -s /etc/ssl/certs/ca-certificates.crt ]]; then
    NEEDED_PACKAGES+=(ca-certificates)
fi

if (( ${#NEEDED_PACKAGES[@]} > 0 )); then
    if ! command -v apt-get >/dev/null 2>&1; then
        printf 'Fehler: Zur automatischen Installation wird apt-get (Debian/Ubuntu) benoetigt. Fehlende Pakete: %s\n' "${NEEDED_PACKAGES[*]}" >&2
        exit 1
    fi
    if (( EUID != 0 )) && ! command -v sudo >/dev/null 2>&1; then
        printf 'Fehler: Fuer die fehlenden Pakete wird root oder sudo benoetigt: %s\n' "${NEEDED_PACKAGES[*]}" >&2
        exit 1
    fi
    printf 'Installiere fehlende Systempakete: %s\n' "${NEEDED_PACKAGES[*]}"
    if (( EUID == 0 )); then
        apt-get update
        if ! DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${NEEDED_PACKAGES[@]}"; then
            echo 'APT-Installation fehlgeschlagen. Bitte Paketquellen pruefen (unter Ubuntu ggf. universe fuer sysbench).' >&2
            exit 1
        fi
    else
        sudo apt-get update
        if ! sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${NEEDED_PACKAGES[@]}"; then
            echo 'APT-Installation fehlgeschlagen. Bitte Paketquellen pruefen (unter Ubuntu ggf. universe fuer sysbench).' >&2
            exit 1
        fi
    fi
fi

# Acht Threads auf dem bisherigen VPS; auf anderen VPS automatisch die dort
# verfuegbare Anzahl logischer CPUs nutzen.
SYSBENCH_THREADS="${SYSBENCH_THREADS:-$(nproc)}"
if ! [[ "$SYSBENCH_THREADS" =~ ^[1-9][0-9]*$ ]]; then
    printf 'Fehler: SYSBENCH_THREADS muss eine positive ganze Zahl sein.\n' >&2
    exit 2
fi

# Geekbench 6.7.1 absichtlich festgepinnt: Vergleichbarkeit mit bisherigen Logs.
# Quelle ist die OFFIZIELLE Geekbench-6-Linux-Verlinkung auf geekbench.com/legacy/.
GEEKBENCH_VERSION='6.7.1'
GEEKBENCH_ARCHIVE_URL='https://geekbench-cdn.b-cdn.net/Geekbench-6.7.1-Linux.tar.gz'
GEEKBENCH_INSTALL_ROOT="$HOME/benchmarks"
GEEKBENCH_INSTALL_DIR="$GEEKBENCH_INSTALL_ROOT/Geekbench-${GEEKBENCH_VERSION}-Linux"
GEEKBENCH_SETUP='existing'

if (( ! GEEKBENCH )); then
    GEEKBENCH_SETUP='skipped'
    GB_BIN=''
elif [[ -n ${GEEKBENCH_BIN:-} ]]; then
    GB_BIN="$GEEKBENCH_BIN"
elif [[ -x "$GEEKBENCH_INSTALL_DIR/geekbench6" ]]; then
    GB_BIN="$GEEKBENCH_INSTALL_DIR/geekbench6"
elif [[ -x "$(dirname "${BASH_SOURCE[0]}")/geekbench6" ]]; then
    GB_BIN="$(dirname "${BASH_SOURCE[0]}")/geekbench6"
else
    if [[ "$(uname -m)" != 'x86_64' ]]; then
        echo 'Fehler: Der automatische Geekbench-6.7.1-Download ist fuer x86_64. Fuer andere Architekturen bitte GEEKBENCH_BIN setzen.' >&2
        exit 1
    fi
    if [[ -e "$GEEKBENCH_INSTALL_DIR" ]]; then
        printf 'Fehler: Geekbench-Verzeichnis existiert, aber geekbench6 fehlt/ist nicht ausfuehrbar: %s\n' "$GEEKBENCH_INSTALL_DIR" >&2
        echo 'Bitte manuell pruefen, bevor es ersetzt wird (keine automatische Loeschung).' >&2
        exit 1
    fi
    mkdir -p "$GEEKBENCH_INSTALL_ROOT"
    STAGING="$(mktemp -d "$GEEKBENCH_INSTALL_ROOT/.geekbench-setup.XXXXXXXX")"
    printf 'Lade Geekbench %s von Primate Labs herunter ...\n' "$GEEKBENCH_VERSION"
    if ! curl --fail --location --show-error --progress-bar --retry 3 \
        --proto '=https' --tlsv1.2 -o "$STAGING/geekbench.tar.gz" "$GEEKBENCH_ARCHIVE_URL"; then
        rm -rf -- "$STAGING"
        echo 'Geekbench-Download fehlgeschlagen. Netzwerk und TLS-Zertifikate pruefen.' >&2
        exit 1
    fi
    # Keine absoluten Pfade/Traversal-Eintraege oder fremden Top-Level-Pfade entpacken.
    if ! tar -tzf "$STAGING/geekbench.tar.gz" | awk '
        { name=$0; sub(/^\.\//,"",name) }
        name ~ /^\// || name ~ /(^|\/)\.\.(\/|$)/ || name !~ /^Geekbench-6\.7\.1-Linux(\/|$)/ { bad=1 }
        END { exit bad }
    '; then
        rm -rf -- "$STAGING"
        echo 'Unerwartete Dateinamen im Geekbench-Archiv; Installation abgebrochen.' >&2
        exit 1
    fi
    if ! tar -xzf "$STAGING/geekbench.tar.gz" -C "$STAGING"; then
        rm -rf -- "$STAGING"
        echo 'Geekbench-Archiv konnte nicht entpackt werden.' >&2
        exit 1
    fi
    if [[ ! -x "$STAGING/Geekbench-${GEEKBENCH_VERSION}-Linux/geekbench6" ]]; then
        rm -rf -- "$STAGING"
        echo 'Geekbench-Archiv enthaelt keine ausfuehrbare geekbench6-Datei.' >&2
        exit 1
    fi
    mv -- "$STAGING/Geekbench-${GEEKBENCH_VERSION}-Linux" "$GEEKBENCH_INSTALL_DIR"
    rm -rf -- "$STAGING"
    GEEKBENCH_SETUP='downloaded'
    GB_BIN="$GEEKBENCH_INSTALL_DIR/geekbench6"
fi
if (( GEEKBENCH )); then
    if [[ ! -x "$GB_BIN" ]]; then
        printf 'Geekbench ist nicht ausfuehrbar: %s\n' "$GB_BIN" >&2
        exit 1
    fi
    GB_DIR="$(cd "$(dirname "$GB_BIN")" && pwd -P)"
    GB_BIN="$GB_DIR/$(basename "$GB_BIN")"
fi

STAMP="$(date +%Y%m%d-%H%M%S)"
LOGDIR="$HOME/vps-cpu-evidence-$STAMP"
ARCHIVE="$LOGDIR.zip"
if [[ -e "$LOGDIR" || -e "$ARCHIVE" ]]; then
    echo 'Fehler: Bereits eine Messung mit diesem Zeitstempel vorhanden; bitte erneut starten.' >&2
    exit 1
fi
mkdir -p "$LOGDIR"/{01-geekbench,02-sysbench}
MONITOR_PID=''
VMSTAT_PID=''
RAW_GEEK=''
OVERALL_FAILED=0

stamp() { printf '%s | %s\n' "$(date -Is)" "$*" | tee -a "$LOGDIR/.timeline.tmp"; }

stop_monitor() {
    if [[ -n "$MONITOR_PID" ]]; then
        kill "$MONITOR_PID" 2>/dev/null || true
        wait "$MONITOR_PID" 2>/dev/null || true
        MONITOR_PID=''
    fi
}
start_monitor() {
    local output_file="$1"
    # Jede Sekunde: CPU all und alle einzelnen vCPUs, inklusive %steal.
    S_TIME_FORMAT=ISO stdbuf -oL mpstat -P ALL 1 > "$output_file" 2>&1 &
    MONITOR_PID=$!
}
# Zwei cgroup-Momentaufnahmen in EINER Systemdatei statt separater Zwischenlogs.
append_cgroup_snapshot() {
    local phase="$1"
    {
        printf '\nCgroup CPU (%s; %s):\n' "$phase" "$(date -Is)"
        if [[ -r /sys/fs/cgroup/cpu.max ]]; then
            echo 'cpu.max:'; cat /sys/fs/cgroup/cpu.max
        fi
        if [[ -r /sys/fs/cgroup/cpu.stat ]]; then
            echo 'cpu.stat:'; cat /sys/fs/cgroup/cpu.stat
        fi
    } >> "$LOGDIR/system.txt"
}

start_vmstat() {
    local samples=30
    # Bei kuerzeren (optional eingestellten) Sysbench-Laeufen nicht laenger messen.
    if (( SYSBENCH_SECONDS < samples )); then samples="$SYSBENCH_SECONDS"; fi
    # -y entfernt den seit Boot aggregierten ersten Wert, -t gibt Uhrzeiten aus.
    vmstat -n -t -y 1 "$samples" > "$LOGDIR/02-sysbench/multi-core-vmstat-1s.log" 2>&1 &
    VMSTAT_PID=$!
}

wait_vmstat() {
    if [[ -n "$VMSTAT_PID" ]]; then
        if ! wait "$VMSTAT_PID"; then
            echo 'Warnung: vmstat-Messung fehlgeschlagen.' >&2
            OVERALL_FAILED=1
        fi
        VMSTAT_PID=''
    fi
}

stop_vmstat() {
    if [[ -n "$VMSTAT_PID" ]]; then
        kill "$VMSTAT_PID" 2>/dev/null || true
        wait "$VMSTAT_PID" 2>/dev/null || true
        VMSTAT_PID=''
    fi
}

# CPU-Steal: Auswertung ALLER Sekunden inkl. 5 Sekunden Vor-/Nachlauf.
# Zusaetzlich stark belastete Sekunden mit usr + sys >= 20 %.
analyze_mpstat() {
    local src="$1" label="$2" active_threshold="${3:-20}"
    echo "[$label]"
    if [[ ! -s "$src" ]]; then echo 'Keine mpstat-Daten'; return 0; fi
    awk -v threshold="$active_threshold" '
      $0 ~ /%steal/ && $0 ~ /CPU/ {
        cpuCol=0; stealCol=0; usrCol=0; sysCol=0;
        for(i=1;i<=NF;i++) {
          if($i=="CPU") cpuCol=i;
          if($i=="%steal") stealCol=i;
          if($i=="%usr") usrCol=i;
          if($i=="%sys") sysCol=i;
        }
        next
      }
      cpuCol>0 && stealCol>0 && $cpuCol ~ /^[0-9]+$/ && $stealCol ~ /^[0-9]+([.][0-9]+)?$/ {
        one=$stealCol+0;
        if(one>oneMax) { oneMax=one; oneCpu=$cpuCol }
      }
      cpuCol>0 && stealCol>0 && $cpuCol=="all" && $stealCol ~ /^[0-9]+([.][0-9]+)?$/ {
        s=$stealCol+0; n++; sum+=s;
        if(s>max) max=s;
        if(s>=10) n10++;
        if(s>=20) n20++;
        if(usrCol>0 && sysCol>0 && ($usrCol+$sysCol)>=threshold) {
          loadn++; loadsum+=s;
          if(s>loadmax) loadmax=s
        }
      }
      END {
        if(!n) { print "Keine lesbaren Messintervalle"; exit }
        printf "Sekundenintervalle: %d\n",n;
        printf "Steal, Mittel aller Intervalle: %.2f %%\n",sum/n;
        printf "Steal, Maximum (CPU all): %.2f %%\n",max;
        if(oneMax>0) printf "Maximaler Steal einer einzelnen vCPU: %.2f %% (vCPU %s)\n",oneMax,oneCpu;
        printf "Intervalle mit >=10 %% / >=20 %% Steal: %d / %d\n",n10,n20;
        if(loadn) printf "Steal bei CPU-Arbeit >=%.0f %% (n=%d): Mittel %.2f %%, Max %.2f %%\n",threshold,loadn,loadsum/loadn,loadmax;
        else printf "Keine Intervalle mit CPU-Arbeit >=%.0f %% beobachtet\n",threshold;
      }
    ' "$src"
}

finish() {
    local status="$1" archive_status=0
    trap - EXIT INT TERM
    stop_monitor
    stop_vmstat
    if [[ -n "$RAW_GEEK" ]]; then rm -f "$RAW_GEEK"; fi
    if (( status != 0 || OVERALL_FAILED != 0 )); then
        echo "$(date -Is) | Status: Unvollstaendig (mindestens eine Phase fehlgeschlagen)" >> "$LOGDIR/.timeline.tmp"
    else
        if (( GEEKBENCH )); then
            echo "$(date -Is) | Status: Alle drei Benchmark-Phasen abgeschlossen" >> "$LOGDIR/.timeline.tmp"
        else
            echo "$(date -Is) | Status: Beide Sysbench-Phasen abgeschlossen (Geekbench ausgelassen)" >> "$LOGDIR/.timeline.tmp"
        fi
    fi
    {
        echo 'CPU-PERFORMANCEMESSUNG - UEBERSICHT'
        echo "Messung erstellt: $(date -Is)"
        echo 'Zeitzone und UTC-Offset sind in den Zeitstempeln angegeben.'
        echo
        echo '1. Benchmark-Konfiguration'
        if (( GEEKBENCH )); then
            printf 'Geekbench: %s (Single-Core und Multi-Core)\n' "$GEEKBENCH_VERSION"
        else
            echo 'Geekbench: ausgelassen (GEEKBENCH=0)'
        fi
        printf 'Sysbench: CPU-Primzahltest, Maximalwert %s, je %s s\n' "$SYSBENCH_PRIME" "$SYSBENCH_SECONDS"
        printf 'Sysbench: 1 Thread und %s Threads\n' "$SYSBENCH_THREADS"
        printf 'mpstat: 1-s-Intervalle mit %s s Vor- und %s s Nachlauf\n' "$BASELINE_SECONDS" "$AFTER_SECONDS"
        echo 'vmstat: 1-s-Intervalle, 30 Stichproben waehrend des Multi-Core-Tests (sofern dieser >=30 s dauert)'
        echo
        echo '2. Exakte Zeitpunkte'
        cat "$LOGDIR/.timeline.tmp"
        echo
        echo '3. Benchmark-Ergebnisse'
        for name in single-core multi-core; do
            printf 'Sysbench %s: ' "$name"
            grep -m 1 -E 'events per second:' "$LOGDIR/02-sysbench/${name}-sysbench.log" 2>/dev/null || echo 'Keine Ergebniszeile vorhanden'
        done
        echo 'Geekbench-Ergebnis (oeffentliche URL, sofern vorhanden):'
        grep -m 1 -Eo 'https://browser\.geekbench\.com/v6/cpu/[0-9]+' "$LOGDIR/01-geekbench/geekbench.log" 2>/dev/null || true
        echo
        echo '4. CPU-Steal-Time (mpstat)'
        echo 'Mittelwerte ueber gesamte Monitorlaufzeit einschliesslich kurzer Vor-/Nachmessungen.'
        echo 'Die jeweils angegebenen Lastintervalle basieren auf usr+sys-Schwellenwerten.'
        echo
        analyze_mpstat "$LOGDIR/01-geekbench/mpstat-1s.log" 'Geekbench 6'
        echo
        analyze_mpstat "$LOGDIR/02-sysbench/single-core-mpstat-1s.log" 'Sysbench 1 Thread' 5
        echo
        analyze_mpstat "$LOGDIR/02-sysbench/multi-core-mpstat-1s.log" "Sysbench $SYSBENCH_THREADS Threads"
        echo
        echo '5. Messprotokolle'
        echo '01-geekbench/: Geekbench-Ausgabe und mpstat pro vCPU'
        echo '02-sysbench/: beide CPU-Tests mit Sysbench- und mpstat-Protokollen'
        echo '02-sysbench/multi-core-vmstat-1s.log: angeforderte vmstat-Messung unter Multi-Core-Last'
        echo 'system.txt: CPU-/Systeminformationen und cgroup-CPU-Status vor/nach der Messung'
        echo
        echo 'Interpretation: %steal zeigt Wartezeit auf Rechenzeit des Hypervisors.'
        echo 'Die Werte allein belegen keine bestimmte Ursache (z. B. kein Overselling-Nachweis).'
        echo 'Ein privater Geekbench-Claim-Link wird nicht mitgeliefert.'
    } > "$LOGDIR/00-Messuebersicht.txt"
    rm -f -- "$LOGDIR/.timeline.tmp"
    if ! ( cd "$(dirname "$LOGDIR")" && zip -q -r "$ARCHIVE" "$(basename "$LOGDIR")" ); then
        archive_status=1
        echo 'Fehler: ZIP-Archiv konnte nicht erstellt werden; Messdateien bleiben erhalten.' >&2
    elif ! unzip -tqq "$ARCHIVE"; then
        archive_status=1
        echo 'Fehler: ZIP-Archiv konnte nicht erfolgreich geprueft werden.' >&2
    fi
    printf '\nErgebnisse: %s\n' "$LOGDIR"
    if (( archive_status == 0 )); then printf 'Support-ZIP: %s\n' "$ARCHIVE"; fi
    if (( status != 0 )); then exit "$status"; fi
    if (( OVERALL_FAILED != 0 || archive_status != 0 )); then exit 1; fi
    exit 0
}
trap 'finish $?' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

{
    echo 'SYSTEM- UND CPU-INFORMATIONEN'
    printf 'Start: %s\n' "$(date -Is)"
    echo 'Betriebssystem:'
    if [[ -r /etc/os-release ]]; then grep -m 1 '^PRETTY_NAME=' /etc/os-release; fi
    echo 'Kernel:'
    uname -sr
    echo 'CPU-Topologie:'
    lscpu
    echo 'Verfuegbare vCPUs:'
    nproc
    echo 'Benchmark-Versionen:'
    sysbench --version
    mpstat -V
    vmstat -V
    echo 'Benchmark-Einstellungen:'
    printf 'Sysbench time=%s seconds, threads=%s, prime=%s\n' "$SYSBENCH_SECONDS" "$SYSBENCH_THREADS" "$SYSBENCH_PRIME"
    printf 'mpstat pre/post=%s/%s seconds, between phases=%s seconds\n' "$BASELINE_SECONDS" "$AFTER_SECONDS" "$PAUSE_SECONDS"
} > "$LOGDIR/system.txt" 2>&1
append_cgroup_snapshot 'Vor Gesamtmessung'

stamp 'START Gesamtmessung'
if (( GEEKBENCH )); then
    stamp 'START Monitor Geekbench'
    start_monitor "$LOGDIR/01-geekbench/mpstat-1s.log"
    sleep "$BASELINE_SECONDS"
    stamp 'START Geekbench 6 (Single-Core + Multi-Core, unveraendert)'
    # Raw-Ausgabe wird nur temporaer gespeichert, um die private Claim-URL zu sichern.
    # Im Support-Archiv steht ausschliesslich die bereinigte Geekbench-Ausgabe.
    RAW_GEEK="$(mktemp)"
    if ( cd "$GB_DIR" && "$GB_BIN" ) 2>&1 | tee "$RAW_GEEK" \
          | sed -u -E 's@https://browser\.geekbench\.com/v[0-9]+/cpu/[0-9]+/claim\?key=[^[:space:]]+@[PRIVATE CLAIM LINK REDACTED]@g' \
          | tee "$LOGDIR/01-geekbench/geekbench.log"; then
        stamp 'END Geekbench 6, exit=0'
    else
        stamp 'END Geekbench 6, ERROR'
        OVERALL_FAILED=1
    fi
    if grep -Eq 'https://browser\.geekbench\.com/v[0-9]+/cpu/[0-9]+/claim\?key=' "$RAW_GEEK"; then
        CLAIM_FILE="$HOME/geekbench-private-claim-$STAMP.txt"
        grep -Eo 'https://browser\.geekbench\.com/v[0-9]+/cpu/[0-9]+/claim\?key=[^[:space:]]+' "$RAW_GEEK" > "$CLAIM_FILE"
        chmod 600 "$CLAIM_FILE"
        echo "Privater Claim-Link (NICHT an Support schicken): $CLAIM_FILE"
    fi
    rm -f "$RAW_GEEK"; RAW_GEEK=''
    sleep "$AFTER_SECONDS"
    stop_monitor
    stamp 'STOP Monitor Geekbench'
else
    stamp 'Geekbench 6 ausgelassen (GEEKBENCH=0)'
fi

run_sysbench_phase() {
    local mode="$1" threads="$2" label="$3" phase_dir="$LOGDIR/02-sysbench"
    stamp "START Monitor $label"
    start_monitor "$phase_dir/${mode}-mpstat-1s.log"
    sleep "$BASELINE_SECONDS"
    stamp "START $label, threads=$threads, time=${SYSBENCH_SECONDS}s, prime=$SYSBENCH_PRIME"
    if [[ "$mode" == 'multi-core' ]]; then start_vmstat; fi
    if sysbench cpu --cpu-max-prime="$SYSBENCH_PRIME" --threads="$threads" \
          --time="$SYSBENCH_SECONDS" --events=0 --report-interval=1 run 2>&1 \
          | tee "$phase_dir/${mode}-sysbench.log"; then
        stamp "END $label, exit=0"
    else
        stamp "END $label, ERROR"
        OVERALL_FAILED=1
    fi
    if [[ "$mode" == 'multi-core' ]]; then wait_vmstat; fi
    sleep "$AFTER_SECONDS"
    stop_monitor
    stamp "STOP Monitor $label"
}

stamp "Pause vor Sysbench Single-Core: ${PAUSE_SECONDS}s"
sleep "$PAUSE_SECONDS"
run_sysbench_phase single-core 1 'Sysbench Single-Core'
stamp "Pause vor Sysbench Multi-Core: ${PAUSE_SECONDS}s"
sleep "$PAUSE_SECONDS"
run_sysbench_phase multi-core "$SYSBENCH_THREADS" 'Sysbench Multi-Core'
append_cgroup_snapshot 'Nach Gesamtmessung'
stamp 'ENDE Gesamtmessung'
