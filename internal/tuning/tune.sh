#!/usr/bin/env bash
# vps-tune 2.3 Lean – Performance-Tuning für Linux-KVM-VPS (Debian/Ubuntu mit GRUB).
#
# Grundsätze:
#   * Persistenz nur über Standard-Mechanismen des Systems (grub.d, sysctl.d,
#     modules-load.d, tmpfiles.d, udev, systemd-Drop-ins). Einziger eigener
#     Dienst nur bei ausdrücklichem --zram on: vps-tune-zram.service.
#   * Keine sysstat-Sammlung und keine APT-Wartungstimer-Eingriffe im Betrieb.
#     Beim Upgrade werden zuvor von v2.2 angelegte Drop-ins entfernt.
#   * Jede Laufzeitänderung sichert vorher den Originalwert. "restore" setzt
#     genau diese Werte zurück und entfernt alle eigenen Dateien.
#   * Fremde Dateien (z. B. /etc/default/grub) werden nie verändert.
#     Einzige Ausnahme: ENABLED= in /etc/default/sysstat, der von Debian
#     vorgesehene Schalter für sysstat (Originalwert wird gesichert).
set -Eeuo pipefail
umask 022

VERSION='2.3.0-lean'
P="${VT_PREFIX:-}"                     # nur für Tests: Wurzel einer Testumgebung
ETC="$P/etc"; SYS="$P/sys"; PROC="$P/proc"; BOOT="$P/boot"
STATE="$P/var/lib/vps-tune"; BASE="$STATE/baseline"; APPLIED="$STATE/applied.conf"
PRIO_LIST="$STATE/prio.list"; PRIO_BAK="$STATE/prio-backup"
GRUB_SNIPPET="$ETC/default/grub.d/99-vps-tune.cfg"
SYSCTL_FILE="$ETC/sysctl.d/90-vps-tune.conf"
MODLOAD_FILE="$ETC/modules-load.d/vps-tune.conf"
TMPFILES_FILE="$ETC/tmpfiles.d/vps-tune.conf"
UDEV_FILE="$ETC/udev/rules.d/90-vps-tune-rps.rules"
SD_DIR="$ETC/systemd/system"
ZRAM_UNIT="$SD_DIR/vps-tune-zram.service"
SAR_DROPIN="$SD_DIR/sysstat-collect.timer.d/90-vps-tune.conf"
SYSSTAT_DEFAULT="$ETC/default/sysstat"
APT_TIMERS=(apt-daily apt-daily-upgrade)
APT_TIMES=('05:00' '05:30')            # feste Uhrzeit (Serverzeitzone) statt Zufallsfenster
THP_DIR="$SYS/kernel/mm/transparent_hugepage"
PREEMPT_FILE="$SYS/kernel/debug/sched/preempt"
KREL="${VT_KREL:-$(uname -r)}"
MARK='# Verwaltet von vps-tune – wird bei "vps-tune restore" entfernt.'

# sysctl-Werte: Schlüssel, Wert, Gruppe (Schalter), 1 = nur anheben, nie absenken
SC_KEYS=(net.core.default_qdisc net.ipv4.tcp_congestion_control net.ipv4.tcp_slow_start_after_idle
         net.core.rmem_max net.core.wmem_max net.ipv4.tcp_mtu_probing)
SC_VALS=(fq bbr 0 7500000 7500000 1)
SC_GRP=(net net net udp udp mtu)
SC_RAISE=(0 0 0 1 1 0)
DIRTY_BG=134217728                     # 128 MiB: ab hier schreibt der Kernel im Hintergrund
DIRTY_FG=536870912                     # 512 MiB: ab hier werden schreibende Prozesse gebremst

DRY=0; YES=0; WARNS=0; REBOOT=0; FAILED=0; MF_CHANGED=0
PRESET=''; O_PREEMPT=''; O_HALTPOLL=''; O_MITIG=''; O_HARDEN=''; O_NET=''
O_THP=''; O_DEFRAG=''; O_RPS=''; O_IFACE='auto'
O_IO=''; O_UDP=''; O_MTU=''; O_ZRAM=''
PARAMS=(); HALTPOLL_MODULE=0

trap 'printf "[FEHLER] Abbruch in Zeile %s. Mit \"vps-tune restore\" lässt sich alles zurücksetzen.\n" "$LINENO" >&2' ERR

info() { printf '[INFO] %s\n' "$*"; }
plan() { printf '[PLAN] %s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*" >&2; WARNS=$((WARNS + 1)); }
die()  { printf '[FEHLER] %s\n' "$*" >&2; exit 1; }
need_root() { [[ -n "$P" ]] || (( EUID == 0 )) || die 'Bitte mit sudo/root ausführen.'; }

usage() {
cat <<'EOF'
vps-tune 2.3 Lean – Performance-Tuning für Linux-KVM-VPS

  vps-tune status                      Ist-Zustand (ohne Sampling oder Änderungen)
  vps-tune apply <preset> [Schalter]   Preset anwenden (vorher mit --dry-run ansehen)
  vps-tune prio <unit> high|normal|low CPU-Vorrang eines systemd-Dienstes (CPUWeight)
  vps-tune restore [--dry-run]         Alles zurück auf den Zustand vor vps-tune

Presets:
  mixed       Gameserver + Web + Streaming (empfohlen): preempt=full, Guest-Haltpoll,
              mitigations=off, init_on_alloc=0, randomize_kstack_offset=off,
              BBR+fq, THP-Defrag defer+madvise, RPS unverändert
  throughput  Wie mixed, aber preempt=none und ohne Haltpoll (Batch, Rechenjobs)
  safe        Nur BBR+fq und THP-Defrag; keine Sicherheits- oder Kerneländerung
  Alle Presets zusätzlich: Schreibpuffer 128/512 MB, zram-Swap (25 % RAM),
              APT-Wartung fest 05:00/05:30, sysstat jede Minute,
              UDP-Puffer 7,5 MB, TCP-MTU-Probing

Schalter (überschreiben das Preset):
  --preempt none|voluntary|full|keep   --haltpoll on|off
  --mitigations off|keep               --hardening off|keep  (init_on_alloc, kstack-Random.)
  --net on|off                         --thp always|madvise|never|keep
  --defrag always|defer|defer+madvise|madvise|never|keep
  --rps all|nocpu0|off|leave           --iface <name>
  --udp on|off      UDP-Puffer         --mtu on|off       TCP-MTU-Probing
  --io on|off       Schreibpuffer (opt-in)  --zram on|off    zram (opt-in)
  Alte v2.2-Einstellungen für sysstat und APT-Timer werden nur zurückgebaut;
  neue Überwachung oder Wartungs-Timer werden nicht eingerichtet.
  --dry-run   nur anzeigen            --yes   Sicherheitsabfrage überspringen

Bootparameter wirken erst nach einem Neustart, alles andere sofort.
EOF
}

# ---------------------------------------------------------------- Hilfen
is_iface() { [[ "$1" =~ ^[A-Za-z0-9_.-]{1,15}$ ]]; }

in_list() { local x="$1"; shift; local y; for y in "$@"; do [[ "$x" == "$y" ]] && return 0; done; return 1; }

kcfg() {   # Wert einer Kernel-Option: y, m, n oder ? (unbekannt)
    local f="$BOOT/config-$KREL" v=''
    if [[ -r "$f" ]]; then
        v=$(grep -m1 "^$1=" "$f" | cut -d= -f2 || true)
    elif [[ -r "$PROC/config.gz" ]] && command -v zcat >/dev/null 2>&1; then
        v=$(zcat "$PROC/config.gz" | grep -m1 "^$1=" | cut -d= -f2 || true)
    else
        printf '?\n'; return 0
    fi
    printf '%s\n' "${v:-n}"
}

virt() { if [[ -n "${VT_VIRT:-}" ]]; then printf '%s\n' "$VT_VIRT"; else systemd-detect-virt 2>/dev/null || true; fi; }

active_value() {   # "[always] madvise never" -> always; ohne Klammern: ganzer Inhalt
    local c; c=$(<"$1")
    if [[ "$c" =~ \[([^]]+)\] ]]; then printf '%s\n' "${BASH_REMATCH[1]}"; else printf '%s\n' "$c"; fi
}

preempt_modes() {   # verfügbare Modi, z. B. "none voluntary full"
    if (( ! DRY )) && [[ ! -r "$PREEMPT_FILE" && -z "$P" && -d /sys/kernel/debug ]] && ! mountpoint -q /sys/kernel/debug; then
        mount -t debugfs debugfs /sys/kernel/debug 2>/dev/null || true
    fi
    [[ -r "$PREEMPT_FILE" ]] || return 1
    local c; c=$(<"$PREEMPT_FILE"); c=${c//[()]/}
    printf '%s\n' "$c"
}

default_iface() {
    local r='' i='' n cand='' cnt=0
    if [[ "$O_IFACE" != auto ]]; then printf '%s\n' "$O_IFACE"; return 0; fi
    if command -v ip >/dev/null 2>&1; then
        r=$(ip -4 route show default 2>/dev/null | head -n1 || true)
        [[ "$r" == *" dev "* ]] || r=$(ip -6 route show default 2>/dev/null | head -n1 || true)
        if [[ "$r" =~ [[:space:]]dev[[:space:]]+([^[:space:]]+) ]]; then
            i=${BASH_REMATCH[1]}
            if is_iface "$i" && [[ -d "$SYS/class/net/$i" ]]; then printf '%s\n' "$i"; return 0; fi
        fi
    fi
    for n in "$SYS"/class/net/*; do
        [[ -e "$n/device" ]] || continue
        i=${n##*/}; is_iface "$i" || continue
        cand=$i; cnt=$((cnt + 1))
    done
    if (( cnt == 1 )); then printf '%s\n' "$cand"; return 0; fi
    return 1
}

online_cpus() {   # "0-3,6" -> "0 1 2 3 6"
    local list part a b out=()
    list=$(<"$SYS/devices/system/cpu/online")
    IFS=',' read -r -a parts <<< "$list"
    for part in "${parts[@]}"; do
        if [[ "$part" =~ ^([0-9]+)-([0-9]+)$ ]]; then
            for ((a=BASH_REMATCH[1], b=BASH_REMATCH[2]; a<=b; a++)); do out+=("$a"); done
        elif [[ "$part" =~ ^[0-9]+$ ]]; then out+=("$part")
        fi
    done
    printf '%s\n' "${out[*]}"
}

cpu_mask() {   # CPU-Nummern -> Kernel-Maske, 32-Bit-Gruppen mit Komma
    local c hi=-1 i s res='' ; local -a g=()
    for c in "$@"; do
        g[c/32]=$(( ${g[c/32]:-0} | (1 << (c % 32)) ))
        (( c/32 > hi )) && hi=$((c/32))
    done
    (( hi >= 0 )) || { printf '0\n'; return 0; }
    for ((i=hi; i>=0; i--)); do
        if (( i == hi )); then printf -v s '%x' "${g[i]:-0}"; res=$s
        else printf -v s '%08x' "${g[i]:-0}"; res+=",$s"; fi
    done
    printf '%s\n' "$res"
}

mask_norm() { local m="${1//,/}"; m=${m,,}; while [[ "$m" == 0?* ]]; do m=${m#0}; done; printf '%s\n' "${m:-0}"; }

# ---------------------------------------------------------------- Laufzeitwerte mit Sicherung
# knob <id> <pfad> <wunsch|keep> <art: plain|bracket|mask>
knob() {
    local id="$1" path="$2" want="$3" kind="$4" base="$BASE/$1" cur same=0 back=0
    if [[ ! -e "$path" ]]; then
        [[ "$want" == keep ]] || warn "$id: nicht verfügbar ($path)"
        return 0
    fi
    if [[ "$kind" == bracket ]]; then cur=$(active_value "$path"); else cur=$(<"$path"); fi
    if [[ "$want" == keep ]]; then
        [[ -f "$base" ]] || return 0          # nie von uns geändert
        want=$(<"$base"); back=1
        if (( DRY )); then plan "$id: zurück auf $want"; return 0; fi
    elif [[ ! -f "$base" ]] && (( ! DRY )); then
        mkdir -p "$BASE"; printf '%s\n' "$cur" > "$base"
    fi
    if [[ "$kind" == mask ]]; then [[ "$(mask_norm "$cur")" == "$(mask_norm "$want")" ]] && same=1
    else [[ "$cur" == "$want" ]] && same=1; fi
    if (( same )); then
        info "$id: bereits $want"
    elif (( DRY )); then
        plan "$id: $cur -> $want"; return 0
    elif printf '%s\n' "$want" > "$path" 2>/dev/null; then
        info "$id: $cur -> $want"
    else
        warn "$id: konnte '$want' nicht setzen"
        (( back )) && warn "$id: Originalwert bleibt in $base gesichert."
        return 0
    fi
    if (( back )); then rm -f "$base"; fi
    return 0
}

sysctl_path() { printf '%s/sys/%s\n' "$PROC" "${1//.//}"; }

qdisc_root() { tc qdisc show dev "$1" root 2>/dev/null | awk 'NR==1 {print $2}' || true; }

# qdisc <iface> <fq|keep>
qdisc() {
    local ifc="$1" want="$2" base="$BASE/qdisc@$1" cur back=0
    command -v tc >/dev/null 2>&1 || { warn 'tc (iproute2) fehlt; Queue-Disziplin erst nach Neustart aktiv.'; return 0; }
    cur=$(qdisc_root "$ifc")
    if [[ "$want" == keep ]]; then
        [[ -f "$base" ]] || return 0
        want=$(<"$base"); back=1
        if (( DRY )); then plan "qdisc $ifc: zurück auf $want"; return 0; fi
    elif [[ ! -f "$base" ]] && (( ! DRY )); then
        mkdir -p "$BASE"; printf '%s\n' "$cur" > "$base"
    fi
    if [[ -z "$want" || "$cur" == "$want" ]]; then
        info "qdisc $ifc: bereits ${cur:-?}"
    elif [[ "$cur" == mq || "$want" == mq ]]; then
        info "qdisc $ifc: Multiqueue-Gerät, neue Disziplin greift nach Neustart."
    elif (( DRY )); then
        plan "qdisc $ifc: $cur -> $want"; return 0
    elif tc qdisc replace dev "$ifc" root "$want" 2>/dev/null; then
        info "qdisc $ifc: $cur -> $want"
    else
        warn "qdisc $ifc: Wechsel auf $want fehlgeschlagen"
        (( back )) && warn "qdisc $ifc: Originalwert bleibt in $base gesichert."
        return 0
    fi
    if (( back )); then rm -f "$base"; fi
    return 0
}

# Verwaltete Datei schreiben (leer = entfernen). Fremde Dateien bleiben unangetastet.
managed_file() {
    local path="$1" body="$2" content
    if [[ -e "$path" ]] && ! grep -qF "$MARK" "$path"; then
        warn "$path gehört nicht zu vps-tune; wird nicht verändert."; return 1
    fi
    if [[ -z "$body" ]]; then
        [[ -e "$path" ]] || return 0
        if (( DRY )); then plan "entfernen: $path"; return 0; fi
        rm -f "$path"; info "entfernt: $path"; MF_CHANGED=1
        [[ "$(dirname "$path")" == *.timer.d ]] && { rmdir "$(dirname "$path")" 2>/dev/null || true; }
        return 0
    fi
    content="$MARK"$'\n'"$body"
    if [[ -f "$path" && "$(<"$path")" == "$content" ]]; then return 0; fi
    if (( DRY )); then plan "schreiben: $path"; printf '%s\n' "$body" | sed 's/^/         /'; return 0; fi
    mkdir -p "$(dirname "$path")"
    printf '%s\n' "$content" > "$path"
    info "geschrieben: $path"; MF_CHANGED=1
}

sd() { systemctl "$@" >/dev/null 2>&1; }   # systemctl ohne Ausgabe; Rückgabewert zählt

# ---------------------------------------------------------------- Schreibpuffer (vm.dirty_*)
# Bytes- und Prozentwerte schließen sich aus: Wer *_bytes schreibt, setzt *_ratio auf 0
# und umgekehrt (mm/page-writeback.c). Darum wird das Quartett gemeinsam gesichert und
# beim Zurücksetzen der jeweils aktive Wert (Prozent oder Bytes) wieder geschrieben.
dirty_desc() { if (( $3 > 0 )); then printf '%s MB' $(( $3 / 1048576 )); else printf '%s %%' "$1"; fi; }

dirty_set() {   # on|keep
    local base="$BASE/vm@dirty" bgr r bgb b obgr or obgb ob ok=1
    local p_bgr p_r p_bgb p_b
    p_bgr=$(sysctl_path vm.dirty_background_ratio); p_r=$(sysctl_path vm.dirty_ratio)
    p_bgb=$(sysctl_path vm.dirty_background_bytes); p_b=$(sysctl_path vm.dirty_bytes)
    if [[ ! -e "$p_b" || ! -e "$p_bgb" ]]; then
        [[ "$1" == keep ]] || warn 'Schreibpuffer: vm.dirty_* nicht verfügbar.'
        return 0
    fi
    bgr=$(<"$p_bgr"); r=$(<"$p_r"); bgb=$(<"$p_bgb"); b=$(<"$p_b")
    if [[ "$1" == on ]]; then
        if [[ "$bgb" == "$DIRTY_BG" && "$b" == "$DIRTY_FG" ]]; then info 'Schreibpuffer: bereits 128/512 MB'; return 0; fi
        if (( DRY )); then
            plan "Schreibpuffer: $(dirty_desc "$bgr" "$r" "$bgb") / $(dirty_desc "$r" "$r" "$b") -> 128 MB / 512 MB"; return 0
        fi
        [[ -f "$base" ]] || { mkdir -p "$BASE"; printf '%s %s %s %s\n' "$bgr" "$r" "$bgb" "$b" > "$base"; }
        if printf '%s\n' "$DIRTY_BG" > "$p_bgb" 2>/dev/null && printf '%s\n' "$DIRTY_FG" > "$p_b" 2>/dev/null; then
            info "Schreibpuffer: Hintergrund ab 128 MB, Bremse ab 512 MB"
        else
            warn 'Schreibpuffer: Werte konnten nicht gesetzt werden.'
        fi
        return 0
    fi
    [[ -f "$base" ]] || return 0
    read -r obgr or obgb ob < "$base"
    if (( DRY )); then plan "Schreibpuffer: zurück auf $(dirty_desc "$obgr" "$or" "$obgb") / $(dirty_desc "$or" "$or" "$ob")"; return 0; fi
    if (( obgb > 0 )); then printf '%s\n' "$obgb" > "$p_bgb" 2>/dev/null || ok=0
    else printf '%s\n' "$obgr" > "$p_bgr" 2>/dev/null || ok=0; fi
    if (( ob > 0 )); then printf '%s\n' "$ob" > "$p_b" 2>/dev/null || ok=0
    else printf '%s\n' "$or" > "$p_r" 2>/dev/null || ok=0; fi
    if (( ok )); then rm -f "$base"; info "Schreibpuffer: zurück auf $(dirty_desc "$obgr" "$or" "$obgb") / $(dirty_desc "$or" "$or" "$ob")"
    else warn "Schreibpuffer: Zurücksetzen fehlgeschlagen; Originalwerte bleiben in $base gesichert."; fi
    return 0
}

# ---------------------------------------------------------------- zram-Swap
zram_set() {   # on|off
    local kb mb alg body c
    if [[ "$1" == off ]]; then
        [[ -f "$ZRAM_UNIT" ]] || return 0
        if ! grep -qF "$MARK" "$ZRAM_UNIT"; then
            warn "zram: fremde Datei $ZRAM_UNIT; wird nicht gestoppt oder gelöscht."
            return 0
        fi
        if (( DRY )); then plan "zram-Swap abschalten und $ZRAM_UNIT entfernen"; return 0; fi
        if ! sd disable --now vps-tune-zram.service; then
            warn 'zram: Stop/Swapoff fehlgeschlagen. Unit bleibt bestehen, nichts löschen; RAM-Verfügbarkeit prüfen.'
            FAILED=1
            return 0
        fi
        MF_CHANGED=0; managed_file "$ZRAM_UNIT" '' || true
        (( MF_CHANGED )) && { sd daemon-reload || warn 'zram: daemon-reload fehlgeschlagen.'; }
        return 0
    fi
    if [[ ! -f "$ZRAM_UNIT" ]] && grep -q '^/dev/zram' "$PROC/swaps" 2>/dev/null; then
        info 'zram: Es läuft bereits ein zram-Swap (nicht von vps-tune) – übersprungen.'; return 0
    fi
    c=$(kcfg CONFIG_ZRAM)
    if [[ "$c" == n ]]; then warn 'zram: Kernel ohne CONFIG_ZRAM – übersprungen.'; return 0; fi
    for c in zramctl mkswap swapon; do
        command -v "$c" >/dev/null 2>&1 || { warn "zram: $c fehlt (Paket util-linux) – übersprungen."; return 0; }
    done
    kb=$(awk '/^MemTotal:/ {print $2}' "$PROC/meminfo" 2>/dev/null || true)
    [[ "$kb" =~ ^[0-9]+$ ]] || { warn 'zram: MemTotal nicht lesbar – übersprungen.'; return 0; }
    mb=$(( kb / 4 / 1024 ))
    alg=lz4; [[ "$(kcfg CONFIG_ZRAM_BACKEND_ZSTD)" == y ]] && alg=zstd   # Fallback im Dienst: Kernel-Standard
    body="[Unit]
Description=vps-tune: zram-Swap (${mb} MB, $alg) als RAM-Notreserve
After=systemd-modules-load.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'modprobe zram; { d=\$\$(zramctl --find --size ${mb}M --algorithm $alg 2>/dev/null) || d=\$\$(zramctl --find --size ${mb}M); } && mkswap \"\$\$d\" >/dev/null && swapon -p 100 \"\$\$d\" && echo \"\$\$d\" > /run/vps-tune-zram'
ExecStop=/bin/sh -c 'd=\$\$(cat /run/vps-tune-zram 2>/dev/null) || exit 0; swapoff \"\$\$d\"; zramctl --reset \"\$\$d\"; rm -f /run/vps-tune-zram'

[Install]
WantedBy=multi-user.target"
    MF_CHANGED=0
    managed_file "$ZRAM_UNIT" "$body" || return 0
    (( DRY )) && return 0
    if (( MF_CHANGED )); then
        sd daemon-reload || true
        sd enable vps-tune-zram.service || warn 'zram: enable fehlgeschlagen.'
        if sd restart vps-tune-zram.service; then info "zram: ${mb} MB ($alg) aktiv, Vorrang vor anderem Swap"
        else warn 'zram: Start fehlgeschlagen (journalctl -u vps-tune-zram).'; fi
    elif sd enable --now vps-tune-zram.service; then
        info "zram: bereits eingerichtet (${mb} MB, $alg)"
    else
        warn 'zram: Start fehlgeschlagen (journalctl -u vps-tune-zram).'
    fi
    return 0
}

# ---------------------------------------------------------------- v2.2-Migration (nur Abbau, niemals neue Timer)
legacy_cleanup() {
    local t f old base="$BASE/sysstat@enabled" changed=0
    for t in "${APT_TIMERS[@]}"; do
        f="$SD_DIR/$t.timer.d/90-vps-tune.conf"
        MF_CHANGED=0
        managed_file "$f" '' || warn "APT: $f nicht entfernbar."
        (( MF_CHANGED )) && changed=1
    done
    MF_CHANGED=0
    managed_file "$SAR_DROPIN" '' || warn "sysstat: Fremd-Drop-in nicht entfernt."
    (( MF_CHANGED )) && changed=1
    if (( changed && ! DRY )); then
        sd daemon-reload || warn 'systemd daemon-reload fehlgeschlagen.'
        for t in "${APT_TIMERS[@]}"; do sd try-restart "$t.timer" || true; done
        sd try-restart sysstat-collect.timer || true
    fi
    [[ -f "$base" ]] || return 0
    old=$(<"$base")
    if (( DRY )); then plan "sysstat: ENABLED in $SYSSTAT_DEFAULT zurück auf $old"; return 0; fi
    if [[ ! -f "$SYSSTAT_DEFAULT" ]]; then
        warn "sysstat: $SYSSTAT_DEFAULT fehlt, Baseline bleibt bestehen."
        FAILED=1; return 0
    fi
    if grep -q '^ENABLED=' "$SYSSTAT_DEFAULT"; then
        sed -i "s/^ENABLED=.*/ENABLED=\"$old\"/" "$SYSSTAT_DEFAULT"
    else
        printf 'ENABLED="%s"\n' "$old" >> "$SYSSTAT_DEFAULT"
    fi
    if ! DEBIAN_FRONTEND=noninteractive dpkg-reconfigure -f noninteractive sysstat >/dev/null 2>&1; then
        warn 'sysstat: dpkg-reconfigure fehlgeschlagen, Baseline bleibt bestehen.'
        FAILED=1; return 0
    fi
    if [[ "$old" == false ]]; then
        sd disable --now sysstat-collect.timer sysstat-summary.timer || warn 'sysstat: Timer nicht vollständig deaktivierbar.'
    fi
    rm -f "$base"
    info 'sysstat: v2.2-Änderung entfernt, ursprünglicher ENABLED-Wert wiederhergestellt.'
}

# ---------------------------------------------------------------- Bootparameter (GRUB)
grub_cfg_path() { local f; for f in "$BOOT/grub/grub.cfg" "$BOOT/grub2/grub.cfg"; do [[ -f "$f" ]] && { printf '%s\n' "$f"; return 0; }; done; return 1; }

run_update_grub() {
    mkdir -p "$STATE"
    update-grub > "$STATE/update-grub.log" 2>&1
}

build_params() {
    local v modes
    PARAMS=(); HALTPOLL_MODULE=0
    [[ "$O_MITIG" == off ]] && PARAMS+=(mitigations=off)
    if [[ "$O_HARDEN" == off ]]; then
        if [[ "$(kcfg CONFIG_INIT_ON_ALLOC_DEFAULT_ON)" != n ]]; then PARAMS+=(init_on_alloc=0); fi
        if [[ "$(kcfg CONFIG_RANDOMIZE_KSTACK_OFFSET_DEFAULT)" != n ]]; then PARAMS+=(randomize_kstack_offset=off); fi
    fi
    if [[ "$O_PREEMPT" != keep ]]; then
        if [[ "$(kcfg CONFIG_PREEMPT_DYNAMIC)" == n ]]; then
            warn "preempt=$O_PREEMPT übersprungen: Kernel ohne CONFIG_PREEMPT_DYNAMIC."
        elif modes=$(preempt_modes) && ! in_list "$O_PREEMPT" $modes; then
            warn "preempt=$O_PREEMPT übersprungen: dieser Kernel bietet nur: $modes"
        else
            PARAMS+=("preempt=$O_PREEMPT")
        fi
    fi
    if [[ "$O_HALTPOLL" == on ]]; then
        v=$(kcfg CONFIG_HALTPOLL_CPUIDLE)
        if [[ "$(virt)" != kvm ]]; then
            warn 'Haltpoll übersprungen: kein KVM-Gast.'
        elif [[ "$v" == n ]]; then
            warn 'Haltpoll übersprungen: Kernel ohne CONFIG_HALTPOLL_CPUIDLE.'
        else
            PARAMS+=(cpuidle_haltpoll.force=1)
            [[ "$v" == m ]] && HALTPOLL_MODULE=1
        fi
    fi
    return 0
}

apply_grub() {
    local body='' old='' cfg p missing=()
    (( ${#PARAMS[@]} )) && body="GRUB_CMDLINE_LINUX_DEFAULT=\"\${GRUB_CMDLINE_LINUX_DEFAULT} ${PARAMS[*]}\""
    if [[ -f "$GRUB_SNIPPET" ]]; then old=$(<"$GRUB_SNIPPET"); fi
    if [[ -n "$body" && "$old" == "$MARK"$'\n'"$body" ]]; then info "Bootparameter bereits eingetragen: ${PARAMS[*]}"; return 0; fi
    if [[ -z "$body" && ! -f "$GRUB_SNIPPET" ]]; then return 0; fi
    if [[ ! -f "$ETC/default/grub" ]] || ! command -v update-grub >/dev/null 2>&1; then
        warn "Kein Debian/Ubuntu-GRUB gefunden. Bootparameter bitte manuell setzen: ${PARAMS[*]:-(keine)}"
        PARAMS=()
        return 0
    fi
    if (( DRY )); then
        plan "Bootparameter über $GRUB_SNIPPET: ${PARAMS[*]:-(keine, Datei entfernen)} + update-grub"
        return 0
    fi
    managed_file "$GRUB_SNIPPET" "$body" || return 1
    if ! run_update_grub; then
        printf '[FEHLER] update-grub fehlgeschlagen (siehe %s). Vorheriger Stand wird wiederhergestellt.\n' "$STATE/update-grub.log" >&2
        if [[ -n "$old" ]]; then printf '%s\n' "$old" > "$GRUB_SNIPPET"; else rm -f "$GRUB_SNIPPET"; fi
        run_update_grub || printf '[FEHLER] Auch die Wiederherstellung von grub.cfg schlug fehl – vor einem Neustart prüfen!\n' >&2
        return 1
    fi
    REBOOT=1
    if (( ${#PARAMS[@]} )); then
        if ! cfg=$(grub_cfg_path); then
            printf '[FEHLER] update-grub lief, aber keine grub.cfg gefunden – Bootloader prüfen.\n' >&2
            return 1
        fi
        for p in "${PARAMS[@]}"; do grep -qF -- "$p" "$cfg" || missing+=("$p"); done
        if (( ${#missing[@]} )); then
            printf '[FEHLER] Nicht in %s angekommen: %s – ein späteres grub.d-Snippet überschreibt sie oder ein anderer Bootloader ist aktiv. Snippet wird zurückgenommen.\n' "$cfg" "${missing[*]}" >&2
            if [[ -n "$old" ]]; then printf '%s\n' "$old" > "$GRUB_SNIPPET"; else rm -f "$GRUB_SNIPPET"; fi
            run_update_grub || printf '[FEHLER] update-grub nach dem Zurücknehmen fehlgeschlagen – vor einem Neustart prüfen!\n' >&2
            REBOOT=0
            return 1
        fi
        info "GRUB aktualisiert: ${PARAMS[*]}"
    else
        info 'Bootparameter von vps-tune entfernt.'
    fi
}

# ---------------------------------------------------------------- apply
preset_defaults() {
    case "$1" in
        mixed)      O_PREEMPT=full; O_HALTPOLL=on;  O_MITIG=off;  O_HARDEN=off;  O_NET=on ;;
        throughput) O_PREEMPT=none; O_HALTPOLL=off; O_MITIG=off;  O_HARDEN=off;  O_NET=on ;;
        safe)       O_PREEMPT=keep; O_HALTPOLL=off; O_MITIG=keep; O_HARDEN=keep; O_NET=on ;;
        *) die "Unbekanntes Preset: $1 (mixed|throughput|safe)" ;;
    esac
    O_THP=keep; O_DEFRAG=defer+madvise; O_RPS=leave
    # Schlankes, serverunabhängiges Performance-Preset: keine zusätzlichen
    # zeitgesteuerten Dienste, Swap-Geräte oder Writeback-Beschränkungen.
    O_IO=off; O_UDP=on; O_MTU=on; O_ZRAM=off
}

sc_group_on() {
    case "$1" in
        net) [[ "$O_NET" == on ]] ;;
        udp) [[ "$O_UDP" == on ]] ;;
        mtu) [[ "$O_MTU" == on ]] ;;
        *)   return 1 ;;
    esac
}

confirm_risk() {
    local p risky=()
    for p in "${PARAMS[@]}"; do
        case "$p" in mitigations=off|init_on_alloc=0|randomize_kstack_offset=off) risky+=("$p") ;; esac
    done
    (( ${#risky[@]} && ! DRY && ! YES )) || return 0
    printf '\nACHTUNG: %s schalten Schutzmechanismen des Gastkernels ab.\n' "${risky[*]}"
    printf 'Prozesse in dieser VM können sich dadurch leichter gegenseitig oder den Kernel angreifen.\n'
    [[ -t 0 ]] || die 'Nicht interaktiv: zum Bestätigen --yes angeben.'
    local a; read -r -p 'Fortfahren? Exakt "ja" eingeben: ' a
    [[ "$a" == ja ]] || die 'Abgebrochen.'
}

cmd_apply() {
    need_root
    local ifc rx q mask i want body='' mods=() tmpf='' udev='' cpus path cur
    build_params
    confirm_risk

    # Netzwerk (sofort + sysctl.d)
    if [[ "$O_NET" == on ]] && ! grep -qw bbr "$(sysctl_path net.ipv4.tcp_available_congestion_control)" 2>/dev/null; then
        if (( ! DRY )) && command -v modprobe >/dev/null 2>&1; then modprobe tcp_bbr 2>/dev/null || true; fi
        if ! grep -qw bbr "$(sysctl_path net.ipv4.tcp_available_congestion_control)" 2>/dev/null && (( ! DRY )); then
            warn 'BBR nicht verfügbar; Netzwerk-Tuning übersprungen.'; O_NET=off
        fi
    fi
    for i in "${!SC_KEYS[@]}"; do
        path=$(sysctl_path "${SC_KEYS[i]}"); want=keep
        if sc_group_on "${SC_GRP[i]}"; then
            want=${SC_VALS[i]}
            if (( SC_RAISE[i] )) && [[ -r "$path" ]]; then
                cur=$(<"$path")
                # Hochkonfigurierte, bestehende Werte auch bei wiederholtem apply
                # nicht absenken. Den tatsächlich ausgewählten Wert persistieren.
                if [[ "$cur" =~ ^[0-9]+$ ]] && (( cur > want )); then want=$cur; fi
            fi
        fi
        knob "sysctl@${SC_KEYS[i]}" "$path" "$want" plain
        [[ "$want" != keep && -e "$path" ]] && body+="${SC_KEYS[i]} = $want"$'\n'
    done
    if [[ "$O_IO" == on ]]; then
        dirty_set on
        [[ -e "$(sysctl_path vm.dirty_bytes)" ]] && body+="vm.dirty_background_bytes = $DIRTY_BG"$'\n'"vm.dirty_bytes = $DIRTY_FG"$'\n'
    else
        dirty_set keep
    fi
    managed_file "$SYSCTL_FILE" "${body%$'\n'}" || true
    ifc=$(default_iface || true)
    if [[ -n "$ifc" ]]; then
        if [[ "$O_NET" == on ]]; then qdisc "$ifc" fq; else qdisc "$ifc" keep; fi
    else
        warn 'Kein Standard-Netzwerkinterface erkannt; qdisc/RPS übersprungen (--iface angeben).'
    fi
    [[ "$O_NET" == on ]] && mods+=(tcp_bbr)
    (( HALTPOLL_MODULE )) && mods+=(cpuidle-haltpoll)

    # Transparent Huge Pages (sofort + tmpfiles.d)
    knob thp@enabled "$THP_DIR/enabled" "$O_THP" bracket
    knob thp@defrag  "$THP_DIR/defrag"  "$O_DEFRAG" bracket
    [[ "$O_THP" != keep && -e "$THP_DIR/enabled" ]] && tmpf+="w /sys/kernel/mm/transparent_hugepage/enabled - - - - $O_THP"$'\n'
    [[ "$O_DEFRAG" != keep && -e "$THP_DIR/defrag" ]] && tmpf+="w /sys/kernel/mm/transparent_hugepage/defrag - - - - $O_DEFRAG"$'\n'

    # RPS (sofort + udev)
    if [[ -n "$ifc" ]]; then
        cpus=$(online_cpus)
        case "$O_RPS" in
            all)    mask=$(cpu_mask $cpus) ;;
            nocpu0) if [[ "$cpus" == "0 "* ]]; then mask=$(cpu_mask ${cpus#0 }); else warn 'RPS nocpu0: braucht CPU 0 plus weitere CPUs.'; mask=keep; fi ;;
            off)    mask=0 ;;
            *)      mask=keep ;;
        esac
        for rx in "$SYS/class/net/$ifc/queues"/rx-*; do
            [[ -e "$rx/rps_cpus" ]] || continue
            q=${rx##*/}
            knob "rps@$ifc@$q" "$rx/rps_cpus" "$mask" mask
            [[ "$mask" != keep ]] && udev+="ACTION==\"add\", SUBSYSTEM==\"net\", KERNEL==\"$ifc\", ATTR{queues/$q/rps_cpus}=\"$mask\""$'\n'
        done
    fi

    managed_file "$MODLOAD_FILE" "$(printf '%s\n' "${mods[@]}")" || true
    managed_file "$TMPFILES_FILE" "${tmpf%$'\n'}" || true
    managed_file "$UDEV_FILE" "${udev%$'\n'}" || true

    # Optionaler zram-Swap; v2.2-Timer/Datensammlung ausschließlich entfernen.
    zram_set "$O_ZRAM"
    legacy_cleanup

    if ! apply_grub; then FAILED=1; PARAMS=(); fi

    if (( ! DRY )); then
        mkdir -p "$STATE"
        printf 'preset=%s preempt=%s haltpoll=%s mitigations=%s hardening=%s net=%s thp=%s defrag=%s rps=%s io=%s udp=%s mtu=%s zram=%s\nboot=%s\n' \
            "$PRESET" "$O_PREEMPT" "$O_HALTPOLL" "$O_MITIG" "$O_HARDEN" "$O_NET" "$O_THP" "$O_DEFRAG" "$O_RPS" \
            "$O_IO" "$O_UDP" "$O_MTU" "$O_ZRAM" "${PARAMS[*]:-}" > "$APPLIED"
    fi
    printf '\n'
    if (( FAILED )); then
        printf '[FEHLER] Mindestens eine Änderung oder Rücknahme ist fehlgeschlagen. Ausgabe prüfen; nicht ungeprüft neu starten.\n' >&2
        exit 1
    elif (( DRY )); then info 'Nur Vorschau – nichts geändert.'
    elif (( REBOOT )); then info 'Fertig. Bootparameter wirken nach einem NEUSTART; danach "vps-tune status" prüfen.'
    else info 'Fertig.'; fi
}

# ---------------------------------------------------------------- prio
cmd_prio() {
    need_root
    local unit="${1:-}" lvl="${2:-}" w dir
    [[ "$unit" =~ ^[A-Za-z0-9@_.:-]+$ ]] || die 'Syntax: vps-tune prio <unit> high|normal|low'
    [[ "$unit" == *.* ]] || unit="$unit.service"
    case "$lvl" in high) w=1000 ;; low) w=20 ;; normal) w='' ;; *) die 'Stufe: high|normal|low' ;; esac
    systemctl cat "$unit" >/dev/null 2>&1 || die "systemd-Unit $unit nicht gefunden."
    if (( DRY )); then plan "$unit: CPUWeight=${w:-Standard}"; return 0; fi
    mkdir -p "$STATE"; touch "$PRIO_LIST"
    if [[ -n "$w" ]]; then
        dir="$ETC/systemd/system.control/$unit.d"
        if ! grep -qxF "$unit" "$PRIO_LIST" && [[ -f "$dir/50-CPUWeight.conf" ]]; then
            mkdir -p "$PRIO_BAK"; cp -p "$dir/50-CPUWeight.conf" "$PRIO_BAK/$unit.conf"
        fi
        systemctl set-property "$unit" CPUWeight="$w"
        grep -qxF "$unit" "$PRIO_LIST" || printf '%s\n' "$unit" >> "$PRIO_LIST"
        info "$unit: CPUWeight=$w (Standard 100; wirkt nur, wenn Dienste um CPU konkurrieren)."
    else
        prio_reset "$unit"
        grep -vxF "$unit" "$PRIO_LIST" > "$PRIO_LIST.tmp" || true; mv -f "$PRIO_LIST.tmp" "$PRIO_LIST"
    fi
}

prio_reset() {   # stellt den Zustand vor dem ersten "prio" für diese Unit wieder her
    local unit="$1" dir="$ETC/systemd/system.control/$1.d" bak="$PRIO_BAK/$1.conf"
    if [[ -f "$bak" ]]; then
        cp -p "$bak" "$dir/50-CPUWeight.conf" && rm -f "$bak"
        info "$unit: vorheriges CPUWeight-Drop-in wiederhergestellt."
    else
        rm -f "$dir/50-CPUWeight.conf"
        rmdir "$dir" 2>/dev/null || true
        info "$unit: CPUWeight-Drop-in von vps-tune entfernt."
    fi
    systemctl daemon-reload
    info "$unit: Der ursprüngliche Wert gilt sicher nach \"systemctl restart $unit\" oder einem Neustart."
}

# ---------------------------------------------------------------- restore
cmd_restore() {
    need_root
    local b id rest path f u
    if [[ -d "$BASE" ]]; then
        for b in "$BASE"/*; do
            [[ -f "$b" ]] || continue
            id=${b##*/}
            case "$id" in
                sysctl@*) knob "$id" "$(sysctl_path "${id#sysctl@}")" keep plain ;;
                thp@enabled|thp@defrag) knob "$id" "$THP_DIR/${id#thp@}" keep bracket ;;
                rps@*@rx-*)
                    rest=${id#rps@}
                    is_iface "${rest%@*}" || continue
                    knob "$id" "$SYS/class/net/${rest%@*}/queues/${rest#*@}/rps_cpus" keep mask ;;
                qdisc@*) is_iface "${id#qdisc@}" && qdisc "${id#qdisc@}" keep ;;
                vm@dirty) dirty_set keep ;;
            esac
        done
    fi
    zram_set off
    legacy_cleanup
    for f in "$SYSCTL_FILE" "$MODLOAD_FILE" "$TMPFILES_FILE" "$UDEV_FILE"; do managed_file "$f" '' || true; done
    PARAMS=(); if ! apply_grub; then FAILED=1; fi
    if [[ -f "$PRIO_LIST" ]]; then
        while IFS= read -r u; do
            [[ -n "$u" ]] || continue
            if (( DRY )); then plan "$u: CPUWeight zurück"; else prio_reset "$u"; fi
        done < "$PRIO_LIST"
        (( DRY )) || rm -f "$PRIO_LIST"
    fi
    if (( ! DRY && ! FAILED )); then
        rm -f "$APPLIED" "$STATE/update-grub.log"
        rmdir "$PRIO_BAK" "$BASE" "$STATE" 2>/dev/null || true
    fi
    if (( FAILED )); then
        printf '[FEHLER] Restore unvollständig (siehe Meldungen). Zustand manuell prüfen, bevor neu gestartet wird.\n' >&2
        exit 1
    elif (( DRY )); then info 'Nur Vorschau – nichts geändert.'
    elif (( REBOOT )); then info 'Zurückgesetzt. Für die Bootparameter ist ein NEUSTART nötig.'
    else info 'Zurückgesetzt.'; fi
}

# ---------------------------------------------------------------- status
row() { printf '  %s%*s %s\n' "$1" $((28 - ${#1})) '' "$2"; }

cmd_status() {
    local cmd p ifc rx v drv mode
    cmd=$(<"$PROC/cmdline")
    printf '\nvps-tune %s – Status\n%s\n' "$VERSION" '------------------------------------------------'
    printf '%-24s %s\n' 'Kernel:' "$KREL" 'Virtualisierung:' "$(virt)" 'Online-CPUs:' "$(online_cpus | wc -w)"
    if [[ -f "$APPLIED" ]]; then printf '%-24s %s\n' 'Angewendet:' "$(head -n1 "$APPLIED")"
    else printf '%-24s %s\n' 'Angewendet:' 'nichts (vps-tune nicht aktiv)'; fi
    printf '\nBootparameter (vps-tune):\n'
    if [[ -f "$GRUB_SNIPPET" ]]; then
        for p in $(sed -n 's/.*GRUB_CMDLINE_LINUX_DEFAULT} \([^"]*\)".*/\1/p' "$GRUB_SNIPPET"); do
            if [[ " $cmd " == *" $p "* ]]; then printf '  %-30s aktiv\n' "$p"; else printf '  %-30s NACH NEUSTART\n' "$p"; fi
        done
    else printf '  keine\n'; fi
    if [[ -f "$ETC/default/grub" ]] && grep -qE '^[^#]*mitigations=' "$ETC/default/grub"; then
        printf '  Hinweis: /etc/default/grub enthält selbst einen mitigations-Parameter (nicht von vps-tune).\n'
    fi
    printf '\nWirksamkeit:\n'
    drv=$(cat "$SYS/devices/system/cpu/cpuidle/current_driver" 2>/dev/null || printf '?')
    row 'cpuidle-Treiber:' "$drv"
    if [[ "$drv" == haltpoll ]]; then
        v=$( (dmesg 2>/dev/null || journalctl -k -b --no-pager 2>/dev/null || true) | grep -ci 'host does not support poll control' || true)
        if [[ "$v" =~ ^[1-9] ]]; then v='nein – Host pollt weiter, Haltpoll bringt wenig (--haltpoll off erwägen)'
        elif (dmesg >/dev/null 2>&1 || journalctl -k -b -n1 --no-pager >/dev/null 2>&1); then v='keine Negativmeldung (Host-Feature nicht direkt geprüft)'
        else v='unbekannt (Kernel-Log nicht lesbar, mit sudo ausführen)'; fi
        row 'Host-Poll-Control:' "$v"
    fi
    mode=''; if [[ -r "$PREEMPT_FILE" ]]; then mode=$(<"$PREEMPT_FILE"); fi
    row 'Preemption:' "${mode:-nicht lesbar}"
    v=$( (dmesg 2>/dev/null || journalctl -k -b --no-pager 2>/dev/null || true) | grep -m1 -o 'mem auto-init:.*' || true)
    row 'Speicher-Init:' "${v:-nicht lesbar}"
    v=$(grep -l 'Vulnerable' "$SYS"/devices/system/cpu/vulnerabilities/* 2>/dev/null | wc -l || true)
    row 'CPU-Schwachstellen offen:' "${v:-0}"
    row 'THP enabled / defrag:' "$(active_value "$THP_DIR/enabled" 2>/dev/null || printf '?') / $(active_value "$THP_DIR/defrag" 2>/dev/null || printf '?')"
    row 'TCP-Congestion-Control:' "$(cat "$(sysctl_path net.ipv4.tcp_congestion_control)" 2>/dev/null || printf '?')"
    row 'tcp_slow_start_after_idle:' "$(cat "$(sysctl_path net.ipv4.tcp_slow_start_after_idle)" 2>/dev/null || printf '?')"
    if ifc=$(default_iface); then
        row 'Interface:' "$ifc (qdisc $(command -v tc >/dev/null 2>&1 && qdisc_root "$ifc" || printf '?'))"
        for rx in "$SYS/class/net/$ifc/queues"/rx-*; do
            [[ -e "$rx/rps_cpus" ]] && row "RPS ${rx##*/}:" "$(<"$rx/rps_cpus")"
        done
    fi
    v=$(cat "$(sysctl_path net.core.rmem_max)" 2>/dev/null || printf '?')
    row 'UDP-Puffer max (rmem):' "$v"
    row 'TCP-MTU-Probing:' "$(cat "$(sysctl_path net.ipv4.tcp_mtu_probing)" 2>/dev/null || printf '?')"
    if [[ -r "$(sysctl_path vm.dirty_bytes)" ]]; then
        row 'Schreibpuffer:' "$(dirty_desc "$(<"$(sysctl_path vm.dirty_background_ratio)")" 0 "$(<"$(sysctl_path vm.dirty_background_bytes)")") Hintergrund / $(dirty_desc "$(<"$(sysctl_path vm.dirty_ratio)")" 0 "$(<"$(sysctl_path vm.dirty_bytes)")") Bremse"
    fi
    v=$(awk '$1 ~ /^\/dev\/zram/ {printf "%s %d/%d MB belegt, Prio %s", $1, $4/1024, $3/1024, $5}' "$PROC/swaps" 2>/dev/null || true)
    row 'zram-Swap:' "${v:-aus}"
    if [[ -s "$PRIO_LIST" ]]; then row 'CPU-Vorrang (prio):' "$(tr '\n' ' ' < "$PRIO_LIST")"; fi
    printf '\nHinweis: Kein eigenes Steal-Time-Sampling; vorhandenes Monitoring bleibt unberührt.\n\n'
}

# ---------------------------------------------------------------- main
main() {
    local cmd="${1:-}"; (( $# )) && shift
    case "$cmd" in
        ''|-h|--help|help) usage; return 0 ;;
        -V|--version) printf '%s\n' "$VERSION"; return 0 ;;
        status) cmd_status; return 0 ;;
        prio) local u="${1:-}" l="${2:-}"; (( $# >= 2 )) && shift 2 || shift $#
              [[ "${1:-}" == --dry-run ]] && DRY=1
              cmd_prio "$u" "$l"; return 0 ;;
        restore) [[ "${1:-}" == --dry-run ]] && DRY=1; cmd_restore
                 (( WARNS == 0 )) || printf '[HINWEIS] %d Warnung(en) – Ausgabe oben prüfen.\n' "$WARNS" >&2
                 return 0 ;;
        apply) (( $# )) || die 'Preset fehlt: mixed|throughput|safe'; PRESET="$1"; shift; preset_defaults "$PRESET" ;;
        *) die "Unbekannter Befehl: $cmd (siehe --help)" ;;
    esac
    while (( $# )); do
        local opt="$1" val="${2:-}"
        case "$opt" in
            --dry-run) DRY=1; shift; continue ;;
            --yes) YES=1; shift; continue ;;
        esac
        (( $# >= 2 )) || die "$opt braucht einen Wert"
        case "$opt" in
            --preempt)     in_list "$val" none voluntary full keep || die '--preempt: none|voluntary|full|keep'; O_PREEMPT=$val ;;
            --haltpoll)    in_list "$val" on off || die '--haltpoll: on|off'; O_HALTPOLL=$val ;;
            --mitigations) in_list "$val" off keep || die '--mitigations: off|keep'; O_MITIG=$val ;;
            --hardening)   in_list "$val" off keep || die '--hardening: off|keep'; O_HARDEN=$val ;;
            --net)         in_list "$val" on off || die '--net: on|off'; O_NET=$val ;;
            --thp)         in_list "$val" always madvise never keep || die '--thp: always|madvise|never|keep'; O_THP=$val ;;
            --defrag)      in_list "$val" always defer defer+madvise madvise never keep || die '--defrag: siehe --help'; O_DEFRAG=$val ;;
            --rps)         in_list "$val" all nocpu0 off leave || die '--rps: all|nocpu0|off|leave'; O_RPS=$val ;;
            --iface)       is_iface "$val" || die 'Ungültiger Interfacename'; O_IFACE=$val ;;
            --io|--udp|--mtu|--zram)
                           in_list "$val" on off || die "$opt: on|off"
                           case "$opt" in
                               --io) O_IO=$val ;; --udp) O_UDP=$val ;; --mtu) O_MTU=$val ;;
                               --zram) O_ZRAM=$val ;;
                           esac ;;
            *) die "Unbekannte Option: $opt" ;;
        esac
        shift 2
    done
    cmd_apply
    (( WARNS == 0 )) || printf '[HINWEIS] %d Warnung(en) – Ausgabe oben prüfen.\n' "$WARNS" >&2
}

main "$@"
