#!/usr/bin/env bash
# Stellt im Labor Zustände her, die sonst Tage dauern oder schwer zu
# treffen sind. Projekte stoppt es über die API des
# Leitstands, nie mit docker stop. Zeitabhängiges entsteht durch
# Vordatieren der Zustandsdateien, nie durch Verstellen der Uhr.
#
#   labor/zustand.sh stoppen <projekt>       über den Leitstand (von dir gestoppt)
#   labor/zustand.sh starten <projekt>
#   labor/zustand.sh krank|gesund            wackel: Healthcheck schlägt fehl / wieder gut
#   labor/zustand.sh schleife|ruhe          wackel: Neustart-Schleife an / aus
#   labor/zustand.sh platte-voll|platte-frei|platte-weg  ext4-Testplatte /srv/platte auf alt füllen / leeren / entfernen
#   labor/zustand.sh generalprobe-alt        Generalprobe um 73 Std. vordatieren
#   labor/zustand.sh dienst-weg              hostdeck-dienst auf alt hart beenden (mitten im Lauf)
#
# Übersicht und Überwachung (Zustände Z1 bis Z12):
#   labor/zustand.sh ue-einrichten           Z1 mix (Dienst + erledigter Job), Z2 leerlauf (angelegt),
#                                            Z3 notiz.… als zweite Domain von blog, Z4 doppel.… (zwei Ziele),
#                                            Z5 laden.… als zweiter Host im Block von shop, Z6 dienst.… (Prozess
#                                            ohne Docker); danach neu erfassen
#   labor/zustand.sh alltag|umzug-geplant    Modus des Leitstands (Zustand f)
#   labor/zustand.sh leer-aus|leer-an        Route ins Leere aus Caddy nehmen / wieder eintragen
#   labor/zustand.sh verlauf                 30 Tage Verlauf schreiben (Generator, labor/verlauf), mit Ausfall von shop
#   labor/zustand.sh verlauf-weg             Verlauf und Warnungen leeren (frisch installiert, Zustand d)
#   labor/zustand.sh altbestand              Z10: blog seit 4 Tagen von dir gestoppt, Ausfall wie beim alten Fehler K1
#   labor/zustand.sh warnung-blog            Z9 Teil 1: Warnung „blog antwortet nicht“ läuft seit 30 Min.
#   labor/zustand.sh backup-alt <tage>       Z7: seit <tage> Tagen kein gültiges Backup
#   labor/zustand.sh grenzen-niedrig|grenzen-normal   Z11
#   labor/zustand.sh last <sekunden>         Z12/c: drei Kerne auf alt voll auslasten
#   labor/zustand.sh dienst-neu              hostdeck-dienst neu starten (Neustart mitten im Zeitraum)
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
H=hostdeck.beispiel.test
# SERVER=neu wendet die Zustände auf den neuen Server an (Standard: alt).
ex() { docker exec -i "hostdecklab-${SERVER:-alt}" "$@"; }
case "${1:-}" in
  stoppen) node "$HERE/browser/aktion.js" $H STOP "projekt:$2" >/dev/null && echo "$2 über den Leitstand gestoppt" ;;
  starten) node "$HERE/browser/aktion.js" $H START "projekt:$2" >/dev/null && echo "$2 über den Leitstand gestartet" ;;
  krank) ex touch /opt/projekte/wackel/schalter/krank; echo "wackel wird unhealthy (nach etwa 20 s)" ;;
  gesund) ex rm -f /opt/projekte/wackel/schalter/krank; echo "wackel wieder gesund" ;;
  schleife) ex touch /opt/projekte/wackel/schalter/absturz; ex docker restart wackel-dienst-1 >/dev/null; echo "wackel startet ständig neu" ;;
  ruhe) ex rm -f /opt/projekte/wackel/schalter/absturz; echo "wackel: Schleife aus (startet beim nächsten Versuch normal)" ;;
  # Die Überwachung misst nur echte Dateisysteme (ext4, xfs …), kein tmpfs:
  # darum eine kleine ext4-Platte über ein Loop-Gerät unter /srv/platte.
  # Das Abbild formatiert der Labor-Rechner (Debian 13 hat kein mkfs.ext4).
  platte-voll)
    if ! ex test -f /root/platte.img; then
      img=$(mktemp); fallocate -l 200M "$img"; mkfs.ext4 -q -F "$img"
      docker cp "$img" "hostdecklab-${SERVER:-alt}:/root/platte.img"; rm -f "$img"
    fi
    ex bash -c 'mkdir -p /srv/platte
      if ! mountpoint -q /srv/platte; then
        mount -o loop /root/platte.img /srv/platte
      fi
      fallocate -l 165M /srv/platte/fuellung 2>/dev/null || dd if=/dev/zero of=/srv/platte/fuellung bs=1M count=165 status=none
      df -h /srv/platte | tail -1' ;;
  platte-frei) ex bash -c 'rm -f /srv/platte/fuellung; df -h /srv/platte | tail -1' ;;
  platte-weg) ex bash -c 'umount /srv/platte 2>/dev/null; rm -f /root/platte.img'; echo "Test-Platte entfernt" ;;
  generalprobe-alt)
    ex python3 - <<'PY'
import json, datetime, glob
for p in glob.glob('/var/lib/hostdeck/umzug/generalprobe.json'):
    d = json.load(open(p))
    t = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(hours=73)
    d['am'] = t.strftime('%Y-%m-%dT%H:%M:%SZ')
    json.dump(d, open(p, 'w'), indent=2)
    print('Generalprobe vordatiert auf', d['am'])
PY
    ;;
  dienst-weg) ex systemctl kill -s KILL hostdeck-dienst; echo "hostdeck-dienst hart beendet (systemd startet ihn neu)" ;;
  dienst-neu) ex systemctl restart hostdeck-dienst; sleep 3; echo "hostdeck-dienst neu gestartet" ;;
  ue-einrichten)
    for n in notiz doppel laden dienst; do "$HERE/dns.sh" setzen "$n" 10.77.0.10 >/dev/null; done
    ex bash -s <<'SH'
set -euo pipefail
R=public.ecr.aws/docker/library
P=/opt/projekte
# Z1 mix: ein Dienst und ein Einmal-Job im selben Projekt.
mkdir -p $P/mix
cat > $P/mix/docker-compose.yml <<EOF
services:
  web:
    image: $R/busybox:1.37
    restart: unless-stopped
    command: ["sh", "-c", "sleep 1000000"]
  einmal:
    image: $R/busybox:1.37
    restart: "no"
    command: ["sh", "-c", "echo fertig"]
EOF
(cd $P/mix && docker compose up -d >/dev/null 2>&1)
# Z2 leerlauf: angelegt, nie gestartet (wie ein Projekt nach dem Zurückspielen).
mkdir -p $P/leerlauf
cat > $P/leerlauf/docker-compose.yml <<EOF
services:
  web:
    image: $R/busybox:1.37
    restart: unless-stopped
    command: ["sh", "-c", "sleep 1000000"]
EOF
(cd $P/leerlauf && docker compose create >/dev/null 2>&1)
# Z6: ein Prozess ohne Docker hinter einer Domain.
if [ ! -f /etc/systemd/system/labordienst.service ]; then
  mkdir -p /srv/labordienst && echo '<h1>dienst</h1>' > /srv/labordienst/index.html
  cat > /etc/systemd/system/labordienst.service <<EOF
[Unit]
Description=Labor: Webdienst ohne Docker
[Service]
WorkingDirectory=/srv/labordienst
ExecStart=/usr/bin/python3 -m http.server 9090 --bind 127.0.0.1
Restart=always
[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload && systemctl enable --now labordienst >/dev/null 2>&1
fi
# Z3 notiz: zweite Domain von blog; Z5 laden: zweiter Host im Block von shop;
# Z4 doppel: eine Domain mit zwei Zielen (zwei reverse_proxy).
python3 - <<'PY'
import re
p = '/etc/caddy/Caddyfile'
s = open(p).read()
s = s.replace('shop.beispiel.test {', 'shop.beispiel.test, laden.beispiel.test {')
add = ''
if 'notiz.beispiel.test' not in s:
    add += '\nnotiz.beispiel.test {\n\treverse_proxy 127.0.0.1:8083\n}\n'
if 'doppel.beispiel.test' not in s:
    add += '\ndoppel.beispiel.test {\n\treverse_proxy /api/* 127.0.0.1:8082\n\treverse_proxy 127.0.0.1:8081\n}\n'
if 'dienst.beispiel.test' not in s:
    add += '\ndienst.beispiel.test {\n\treverse_proxy 127.0.0.1:9090\n}\n'
i = s.find('# Leitstand von HostDeck')
s = s[:i] + add.lstrip('\n') + '\n' + s[i:] if i > 0 else s + add
open(p, 'w').write(s)
PY
systemctl reload caddy
SH
    node "$HERE/browser/aktion.js" $H POST /api/bestandsaufnahme '{}' >/dev/null 2>&1 || true
    echo "Z1–Z6 eingerichtet; Bestandsaufnahme angestoßen" ;;
  alltag) node "$HERE/browser/aktion.js" $H POST /api/umzug/modus '{"geplant":false,"trotzdem":true}' >/dev/null && echo "Alltag" ;;
  umzug-geplant) node "$HERE/browser/aktion.js" $H POST /api/umzug/modus '{"geplant":true}' >/dev/null && echo "Umzug geplant" ;;
  leer-aus) ex bash -c "python3 - <<'PY'
import re
p='/etc/caddy/Caddyfile'; s=open(p).read()
s=re.sub(r'\nleer\.beispiel\.test \{[^}]*\}\n', '\n', s); open(p,'w').write(s)
PY
systemctl reload caddy"; echo "leer.beispiel.test aus Caddy genommen"
    # Wie bei offenem Leitstand: Er liest die Domains, die Überwachung prüft
    # gleich neu und beendet eine laufende Warnung zu leer. Gewartet
    # wird höchstens 60 s; steht sie dann noch, meldet es der Prüfer.
    node "$HERE/browser/aktion.js" $H GET /api/domains/zustand >/dev/null || true
    weg=""
    for i in $(seq 0 12); do
      if ! node "$HERE/browser/aktion.js" $H GET /api/ueberwachung 2>/dev/null | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const a=((JSON.parse(s).data||{}).warnungen||{}).aktiv||[];process.exit(a.some(x=>x.schluessel==="seite:leer.beispiel.test")?0:1)})'; then
        weg=1; [ "$i" -gt 0 ] && echo "Warnung zu leer.beispiel.test nach etwa $((i*5)) s beendet"; break
      fi
      sleep 5
    done
    [ -n "$weg" ] || echo "Warnung zu leer.beispiel.test steht nach 60 s noch" ;;
  leer-an) ex bash -c "grep -q leer.beispiel.test /etc/caddy/Caddyfile || python3 - <<'PY'
p='/etc/caddy/Caddyfile'; s=open(p).read(); i=s.find('# Leitstand von HostDeck')
add='leer.beispiel.test {\n\treverse_proxy 127.0.0.1:8099\n}\n\n'
s=s[:i]+add+s[i:] if i>0 else s+add; open(p,'w').write(s)
PY
systemctl reload caddy"; echo "leer.beispiel.test wieder in Caddy" ;;
  verlauf)
    list=$(ex docker ps --format '{{.Names}}={{.Label "com.docker.compose.project"}}' | tr '\n' ',')
    mem=$(ex awk '/MemTotal/ {print $2*1024}' /proc/meminfo)
    # Das Laufwerk, das HostDeck misst (im Labor-Container ist das nicht /).
    disk=$(node "$HERE/browser/aktion.js" $H GET /api/ueberwachung | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]["info"]["laufwerke"][0]; print("%s=%d:%d" % (d["pfad"], d["gesamt"], d["belegt"]))')
    cores=$(ex nproc)
    (cd "$HERE/.." && CGO_ENABLED=0 go build -o "$HERE/lauf/verlauf" ./labor/verlauf)
    docker cp "$HERE/lauf/verlauf" hostdecklab-alt:/usr/local/bin/hostdeck-labor-verlauf
    ex systemctl stop hostdeck-dienst
    ex bash -c 'rm -f /var/lib/hostdeck/ueberwachung/*.jsonl.gz'
    ex hostdeck-labor-verlauf -dir /var/lib/hostdeck/ueberwachung -kerne "$cores" -ram "$mem" -platte "$disk" -container "$list" -viel 0f3a9c1e
    # Seitenprüfung seit 30 Tagen; ein echter Ausfall von shop vor 6 Tagen
    # (45 Minuten) und ein Neustart des Servers vor 12 Tagen.
    ex python3 - <<'PY'
import json, datetime as D
p = '/var/lib/hostdeck/ueberwachung/warnungen.json'
try: s = json.load(open(p))
except Exception: s = {}
now = D.datetime.now(D.timezone.utc)
z = lambda t: t.strftime('%Y-%m-%dT%H:%M:%SZ')
day = lambda n, h, m: (now - D.timedelta(days=n)).replace(hour=h, minute=m, second=0, microsecond=0)
s.setdefault('seiten_seit', {})
for h in list(s['seiten_seit']): s['seiten_seit'][h] = z(now - D.timedelta(days=30))
ev = [e for e in (s.get('verlauf') or []) if not e.get('labor')]
ev += [
 {'zeit': z(day(12, 8, 1)), 'art': 'hinweis', 'schluessel': 'neustart', 'stufe': 'info', 'titel': 'Server neu gestartet', 'text': 'Der Server war kurz aus und läuft wieder; Projekte mit Neustart-Regel starten von selbst.', 'seit': z(day(12, 8, 0)), 'link': '#/ueberwachung', 'labor': True},
 {'zeit': z(day(6, 3, 10)), 'art': 'warnung', 'schluessel': 'seite:shop.beispiel.test', 'stufe': 'err', 'titel': 'shop.beispiel.test antwortet nicht', 'text': 'Seit 2 Prüfungen hintereinander (502 Bad Gateway).', 'link': '#/domains', 'labor': True},
 {'zeit': z(day(6, 4, 5)), 'art': 'entwarnung', 'schluessel': 'seite:shop.beispiel.test', 'stufe': 'info', 'titel': 'shop.beispiel.test antwortet wieder', 'text': 'Die Warnung „shop.beispiel.test antwortet nicht“ lief 45 Minuten.', 'seit': z(day(6, 3, 10)), 'gut_seit': z(day(6, 3, 55)), 'link': '#/domains', 'labor': True},
]
ev.sort(key=lambda e: e['zeit'])
s['verlauf'] = ev
json.dump(s, open(p, 'w'), indent=1, ensure_ascii=False)
PY
    ex systemctl start hostdeck-dienst; sleep 3; echo "Verlauf und Ereignisse geschrieben; Dienst läuft wieder" ;;
  verlauf-weg)
    ex systemctl stop hostdeck-dienst
    ex bash -c 'rm -f /var/lib/hostdeck/ueberwachung/*.jsonl.gz /var/lib/hostdeck/ueberwachung/warnungen.json /var/lib/hostdeck/ueberwachung/grenzen.json'
    ex systemctl start hostdeck-dienst; sleep 3; echo "Verlauf und Warnungen geleert (wie frisch eingerichtet)" ;;
  altbestand)
    # blog seit 4 Tagen von dir gestoppt; dazu ein Ausfall wie beim alten
    # Fehler K1: Warnung vor 3 Tagen, Entwarnung erst nach dem Update vor 2 Tagen.
    node "$HERE/browser/aktion.js" $H STOP projekt:blog >/dev/null || true
    ex systemctl stop hostdeck-dienst
    ex python3 - <<'PY'
import json, datetime as D
now = D.datetime.now(D.timezone.utc)
z = lambda t: t.strftime('%Y-%m-%dT%H:%M:%SZ')
day = lambda n, h, m: (now - D.timedelta(days=n)).replace(hour=h, minute=m, second=0, microsecond=0)
g = '/var/lib/hostdeck/gestoppt.json'
st = json.load(open(g))
for k in list(st):
    if k.startswith('blog-'): st[k] = z(day(4, 18, 0))
json.dump(st, open(g, 'w'), indent=2)
# Seit dem Stopp keine Werte von blog im Verlauf: Der Generator
# schreibt für alle Container, die bei „verlauf“ liefen.
import glob, gzip
cut = day(4, 18, 0).timestamp()
drop = lambda k: k.startswith('c/blog-') or k.startswith('p/blog/')
for f in glob.glob('/var/lib/hostdeck/ueberwachung/*.jsonl.gz'):
    lines, hit = [], False
    for l in gzip.open(f, 'rt'):
        x = json.loads(l)
        if x.get('t', 0) >= cut:
            for part in ('v', 'm'):
                d = x.get(part) or {}
                for k in [k for k in d if drop(k)]:
                    del d[k]; hit = True
        lines.append(json.dumps(x, separators=(',', ':')))
    if hit:
        with gzip.open(f, 'wt') as o: o.write('\n'.join(lines) + '\n')
p = '/var/lib/hostdeck/ueberwachung/warnungen.json'
s = json.load(open(p))
ev = [e for e in (s.get('verlauf') or []) if not e.get('altbestand')]
for h in ('blog.beispiel.test', 'notiz.beispiel.test'):
    ev += [
     {'zeit': z(day(3, 20, 33)), 'art': 'warnung', 'schluessel': 'seite:' + h, 'stufe': 'err', 'titel': h + ' antwortet nicht', 'text': 'Seit 2 Prüfungen hintereinander (502 Bad Gateway).', 'link': '#/domains', 'altbestand': True},
     {'zeit': z(day(2, 20, 19)), 'art': 'entwarnung', 'schluessel': 'seite:' + h, 'stufe': 'info', 'titel': 'Wieder in Ordnung: ' + h + ' antwortet nicht', 'text': 'Seit ' + day(2, 20, 9).strftime('%H:%M') + ' gut; die Warnung lief seit ' + day(3, 20, 33).strftime('%d.%m. %H:%M') + '.', 'link': '#/domains', 'altbestand': True},
    ]
ev.sort(key=lambda e: e['zeit'])
s['verlauf'] = ev
json.dump(s, open(p, 'w'), indent=1, ensure_ascii=False)
PY
    ex systemctl start hostdeck-dienst; sleep 3; echo "Altbestand eingespielt (blog seit 4 Tagen gestoppt, Ausfall vor 3 Tagen)" ;;
  warnung-blog)
    ex systemctl stop hostdeck-dienst
    ex python3 - <<'PY'
import json, datetime as D
now = D.datetime.now(D.timezone.utc)
z = lambda t: t.strftime('%Y-%m-%dT%H:%M:%SZ')
p = '/var/lib/hostdeck/ueberwachung/warnungen.json'
s = json.load(open(p))
h = 'blog.beispiel.test'
t = now - D.timedelta(minutes=30)
s.setdefault('aktiv', {})['seite:' + h] = {'schluessel': 'seite:' + h, 'stufe': 'err', 'symbol': 'unlink', 'titel': h + ' antwortet nicht',
  'text': 'Seit 2 Prüfungen hintereinander (502 Bad Gateway).', 'link': '#/domains', 'knopf': 'Domains', 'seit': z(t), 'gemeldet': z(t)}
s['verlauf'] = (s.get('verlauf') or []) + [{'zeit': z(t), 'art': 'warnung', 'schluessel': 'seite:' + h, 'stufe': 'err', 'titel': h + ' antwortet nicht', 'text': 'Seit 2 Prüfungen hintereinander (502 Bad Gateway).', 'link': '#/domains'}]
json.dump(s, open(p, 'w'), indent=1, ensure_ascii=False)
PY
    ex systemctl start hostdeck-dienst; sleep 3; echo "Warnung für blog läuft seit 30 Minuten" ;;
  backup-alt)
    ex systemctl stop hostdeck-dienst
    ex python3 - "${2:-15}" <<'PY'
import json, sys, datetime as D
p = '/var/lib/hostdeck/ueberwachung/warnungen.json'
s = json.load(open(p))
s['ohne_backup_seit'] = (D.datetime.now(D.timezone.utc) - D.timedelta(days=int(sys.argv[1]), minutes=5)).strftime('%Y-%m-%dT%H:%M:%SZ')
json.dump(s, open(p, 'w'), indent=1, ensure_ascii=False)
PY
    ex systemctl start hostdeck-dienst; sleep 3; echo "seit ${2:-15} Tagen kein gültiges Backup" ;;
  grenzen-niedrig) node "$HERE/browser/aktion.js" $H POST /api/ueberwachung/grenzen '{"grenzen":{"platte_gelb":0.5,"platte_rot":0.6,"speicher_gelb":0.5,"speicher_rot":0.4,"prozessor_gelb":50,"prozessor_minuten":1}}' >/dev/null && echo "Grenzen niedrig" ;;
  grenzen-normal) node "$HERE/browser/aktion.js" $H POST /api/ueberwachung/grenzen '{"voreinstellungen":true}' >/dev/null && echo "Grenzen auf Voreinstellungen" ;;
  last) ex bash -c "for i in 1 2 3; do timeout ${2:-180} sh -c 'while :; do :; done' & done; disown -a" ; echo "Last auf 3 Kernen für ${2:-180} s" ;;
  *) sed -n '2,32p' "$0"; exit 2 ;;
esac
