# Manifest-Schema v1

`hostdeck discover` erzeugt zwei Dateien im Ergebnisordner:

| Datei | Inhalt | Wer schreibt |
|---|---|---|
| `inventory.json` | alles, was auf dem Server gefunden wurde | `hostdeck discover`, bei jedem Lauf neu |
| `manifest.yaml` | deine Entscheidungen und Strategien | `hostdeck discover` nur beim ersten Lauf als Vorschlag, danach du mit `hostdeck serve` oder von Hand |

Ist `manifest.yaml` schon da, lässt `hostdeck discover` sie unverändert und meldet im Bericht nur,
welche Schlüssel neu dazugekommen oder weggefallen sind.

## Schlüssel

Jeder Eintrag hat einen Schlüssel, der sich zwischen zwei Läufen nicht ändert, solange auf dem
Server nichts umgebaut wird. Container-IDs kommen darin nie vor, weil sie sich bei jedem Neustart
eines verwalteten Containers ändern.

| Schlüssel | Bedeutung | Beispiel |
|---|---|---|
| `project:<name>` | Compose-Projekt | `project:pensum` |
| `compose:<projekt>/<dienst>` | Dienst eines Compose-Projekts; ab der zweiten Instanz `#<nummer>` | `compose:pensum/db` |
| `managed:<name>` | Container, den ein anderer Container anlegt (z. B. Spielserver von Calagopus/Wings) | `managed:c324292c-dd72-4c73-b255-1302b7d71909` |
| `container:<name>` | Container ohne Compose-Projekt | `container:mein-test` |
| `volume:<name>` | benanntes Docker-Volume | `volume:pensum_pensum_db` |
| `anon:<container-schlüssel>:<ziel>` | anonymes Volume, benannt nach Container und Einhängepunkt | `anon:compose:urls/redis:/data` |
| `path:<absoluter pfad>` | Ordner oder Datei auf dem Host | `path:/opt/calagopus/wings` |
| `host:caddy` | Caddy als Systemdienst | `host:caddy` |

## Regeln für Pfade

1. **Ein Eigentümer pro Pfad, der genaueste gewinnt.** Liegt ein Datenordner mit eigener Strategie
   in einem Projektordner (z. B. `/opt/docker/arbeitszeit/postgres_data`), bekommt er einen eigenen
   Eintrag und wird aus dem Projektordner herausgeschnitten.
2. **Konfiguration bleibt beim Projekt.** Einzelne Dateien und nur lesend eingebundene Ordner im
   Projektordner haben keinen eigenen Eintrag; sie werden mit dem Projektordner gesichert.
3. **Elternordner sind keine Daten.** Bindet ein Container einen Ordner ein, der andere Projekte
   enthält (Arcane bindet `/opt/docker`), zählt das nie als Daten dieses Containers.
4. **Verwaltete Container gehören zum Verwalter.** Ein Container, dessen Ordner unter einem Pfad
   liegen, den ein anderer Container mit Docker-Socket am selben Pfad einbindet, gilt als von diesem
   verwaltet. Seine Daten werden mit dem Ordner des Verwalters gesichert, und er muss über den
   Verwalter gestoppt werden. Wings löscht den Container eines Servers, der aus ist; solche Server
   erkennt discover an ihrem Ordner `<Verwalter-Ordner>/volumes/<UUID>` und führt sie unter
   demselben `managed:`-Schlüssel, damit sie nicht als weggefallen gelten. Ein Server, der nicht
   mehr gebraucht wird, lässt sich mit einem eigenen `path:`-Eintrag für seinen Ordner und
   `strategy: skip` weglassen.
5. **Sperrliste für Systempfade.** Diese Quellen werden nie gesichert und erscheinen nur unter
   `system`: `/proc`, `/sys`, `/dev`, `/run`, `/var/run`, `/var/lib/docker`,
   `/var/lib/containerd`, `/lib/modules`, `/boot`, `/etc/localtime`, `/etc/timezone`,
   `/etc/machine-id`, `/etc/hostname`, `/etc/hosts`, `/etc/resolv.conf` und alle Pfade unterhalb,
   der Wurzelordner `/` selbst sowie jeder Pfad, der auf `docker.sock` oder `containerd.sock`
   endet.
6. **Projektordner** ist das Git-Wurzelverzeichnis des Compose-Ordners, sofern es keine anderen
   Projekte enthält, sonst der Compose-Ordner selbst.

## Aufbau

```yaml
version: 1                       # Pflicht, nur 1 ist gültig
created: "2026-10-30T22:00:00Z"  # wann der Vorschlag entstand (Info)
inventory: inventory.json        # Bezug (Info)
source:
  hostname: vps1
  os: Ubuntu 24.04.5 LTS

projects:
  pensum:                                  # Name des Compose-Projekts
    decision: take                         # take | archive | skip | open
    reason: ""                             # deine Begründung; Pflicht bei skip
    note: ""                               # Hinweis des Tools, wird beim Einlesen ignoriert
    path: /opt/docker/pensum               # Projektordner (Info)
    domains: [work.example.net]             # aus Caddy (Info, später für hostdeck verify)
    services:
      app: {image: image-save}             # image-save | rebuild | skip
      db: {image: rebuild}
    managed: {}                            # verwaltete Container, siehe mcserver
    data:
      volume:pensum_pensum_db: {strategy: dump, engine: postgres}

  mcserver:
    decision: take
    path: /opt/docker/mcserver
    managed:
      c324292c-dd72-4c73-b255-1302b7d71909:
        manager: web                        # Dienst, der ihn anlegt
        stop: manager                       # nur über den Verwalter stoppen (Panel)
    data:
      path:/opt/calagopus/wings: {strategy: stop-copy}
      path:/opt/calagopus/wings/backups: {strategy: skip, reason: nur aktueller Stand}
      # ein alter Server ohne Container, der nicht mehr gebraucht wird:
      path:/opt/calagopus/wings/volumes/<UUID>: {strategy: skip, reason: im Panel gelöscht}
      path:/opt/calagopus/postgres: {strategy: dump, engine: postgres}

unassigned:                                # Daten ohne Projekt
  volume:work_db_data: {strategy: archive, engine: postgres, note: "von keinem Container genutzt"}
  volume:work_go-mod-cache: {strategy: skip, reason: verwaister Cache}
  path:/opt/docker/neko: {strategy: archive}
  path:/root: {strategy: archive}           # am Server selbst: Home-Ordner, Programme, Webseiten
  path:/root/.cache: {strategy: skip, reason: "Cache, lässt sich neu laden"}
  path:/etc: {strategy: archive}            # Systemeinstellungen: nur archive oder skip
  path:/var/spool/cron/crontabs: {strategy: archive}
  path:/minecraft-alt: {strategy: open}     # Fund außerhalb der üblichen Orte

containers: {}                             # Container ohne Compose-Projekt

host:
  caddy: {decision: take}

system:                                    # Sperrliste: sichtbar, nie gesichert
  - {container: compose:kula/kula-monitor, source: /proc}
```

### Werte

| Feld | Werte | Bedeutung |
|---|---|---|
| `decision` (Projekt, Container, Host) | `take` | sichern und auf dem neuen Server starten |
| | `archive` | sichern, aber nicht starten |
| | `skip` | nicht sichern; `reason` ist Pflicht |
| | `open` | noch nicht entschieden; ab M2 verweigert `hostdeck backup`, solange etwas offen ist |
| `strategy` (Daten) | `dump` | Datenbank-Dump über `restic --stdin-from-command`; im finalen Lauf zusätzlich Rohkopie |
| | `stop-copy` | Container stoppen, kopieren, wieder starten |
| | `live-copy` | im laufenden Betrieb kopieren |
| | `rebuild` | nichts sichern, wird neu erzeugt (z. B. Cache) |
| | `archive` | nur unter `unassigned`: sichern, aber beim Restore nicht automatisch zurückspielen (verwaiste Volumes, alte Ordner) |
| | `skip` | nicht sichern; `reason` ist Pflicht |
| | `open` | noch nicht entschieden |
| `image` (Dienst) | `image-save` | Image per `docker save` sichern (lokal gebaut, ohne Namen oder Herkunft unklar) |
| | `rebuild` | Image per Digest neu aus der Registry holen |
| | `skip` | Dienst nicht übernehmen |
| `engine` | `postgres`, `mysql`, `mariadb`, `mongo`, `redis`, `valkey`, `sqlite` | erkannte Datenbank (Info) |
| `stop` (verwaltet) | `manager` | nur über den Verwalter stoppen |

### Strenges Einlesen

- Unbekannte Felder, unbekannte Werte und doppelte Schlüssel sind Fehler.
- `version` muss 1 sein.
- Schlüssel unter `data` und `unassigned` müssen mit `volume:`, `anon:` oder `path:` beginnen;
  Pfade müssen absolut und ohne `..` sein.
- `skip` braucht eine Begründung in `reason`.
- `archive` als `strategy` gibt es nur unter `unassigned`; Projektdaten folgen der `decision` ihres
  Projekts.
- `path:/etc` und die Crontab-Ordner sind Systemeinstellungen. Sie werden archiviert oder
  weggelassen, nie 1:1 zurückgespielt, weil das ein neues System beschädigen kann. Die Seite
  bietet für sie kein „Übernehmen“ an und lehnt es beim Speichern ab.
- Vorgeschlagene Caches in Home-Ordnern (`path:/root/.cache` mit `skip`) kommen nur zusammen mit
  ihrem Home-Ordner ins Manifest. Entfernst du einen, schlägt ein neuer Lauf ihn nicht wieder vor.
- Unter `data` darfst du eigene Unterpfade eines erkannten Ordners oder Projektordners eintragen,
  etwa `path:/opt/calagopus/wings/backups` mit `skip`. Sie gelten nicht als „weggefallen“, solange
  der übergeordnete Ordner erkannt wird.

### Was nie im Manifest steht

- Werte von Umgebungsvariablen, Passwörter, Tokens. Das Inventar enthält nur die Namen der
  Variablen.
- Container-IDs.

Weil das Manifest Domains und Pfade enthält, gehört es in ein privates Repo oder nur ins Backup,
nicht in dieses Repo.
