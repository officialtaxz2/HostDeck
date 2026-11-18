# Prüflabor

Zwei nachgebaute Server, ein eigener DNS und eine eigene Zertifikatsstelle, alles in Docker auf dem
Entwicklungsrechner. Damit lassen sich Umzug, Überwachung und Oberfläche von HostDeck durchspielen, ohne einen echten
Server oder echtes DNS anzufassen.

| Teil | Adresse | Was |
|---|---|---|
| `alt` | 10.77.0.10 | Ubuntu 24.04, systemd, sshd, Docker, Caddy; Beispielprojekte; HostDeck eingerichtet und freigeschaltet unter `hostdeck.beispiel.test` |
| `neu` | 10.77.0.20 | Debian 13, systemd, sshd, **ohne** Docker und Caddy (HostDeck installiert beides beim Umzug) |
| DNS | 10.77.0.53 | CoreDNS, zuständig für `beispiel.test` (Zonendatei `lauf/zone.txt`, lädt alle 2 s neu) |
| Zertifikate | 10.77.0.60 | Pebble (ACME), Caddy holt dort echte Zertifikate; die Wurzel liegt auf beiden Servern |

Nichts davon spricht mit echten Servern oder echtem DNS. Die Server erreichen das Internet nur für Pakete und
Images (über den Proxy der Bauumgebung, falls es einen gibt).

## Starten und stoppen

```bash
labor/start.sh                 # alles starten; Images nur bauen, wenn sie fehlen
LAB_REBUILD=1 labor/start.sh   # Images neu bauen
LAB_HOSTDECK=/pfad/hostdeck labor/start.sh   # diese HostDeck-Datei einspielen (sonst go build aus dem Repo)
labor/hostdeck-einspielen.sh alt labor/lauf/hostdeck   # neue HostDeck-Datei auf alt (Update wie beim Nutzer)
labor/stop.sh                  # Container, Netz und Volumes weg; labor/lauf bleibt
labor/stop.sh --alles          # auch labor/lauf
```

Wegwerf-Passwörter erzeugt `start.sh` in `labor/lauf/` (nicht im Repo): `root-alt`, `root-neu`,
`leitstand-alt`, `leitstand-neu`.

Docker Hub antwortet aus Cloud-Umgebungen oft mit 429. Die Beispielprojekte laden deshalb von
`public.ecr.aws/docker/library`, CoreDNS und Pebble von `mirror.gcr.io` und `ghcr.io`.

## Beispielprojekte auf alt

`labor/alt/einrichten.sh` legt sie an (mehrfach aufrufbar):

| Projekt | Muster |
|---|---|
| `app` | Web (selbst gebautes Image) + PostgreSQL, Daten als Bind-Ordner, `app.beispiel.test` |
| `shop` | Web + MariaDB, Daten im Volume, `shop.beispiel.test` |
| `blog` | statische Seite, `blog.beispiel.test` (im Drehbuch „von dir gestoppt“) |
| `aufgabe` | Einmal-Job (Code 0, Neustart „no“) |
| `panel` | Verwalter mit Docker-Socket und zwei verwalteten Containern (Muster eines Spiele-Panels) |
| `spiel` | TCP-Dienst ohne Caddy; `spiel.beispiel.test` nur im DNS, dazu ein SRV-Eintrag `_minecraft._tcp` |
| `start` | Variable nur aus einem Start-Skript |
| `wackel` | Healthcheck und Neustart-Schleife per Schalterdatei |
| Caddy | dazu `leer.beispiel.test`, eine Route ins Leere |
| Spielserver | `labor/spiele.sh` (nicht von `start.sh`): drei nachgebaute Spielserver im Panel-Muster (2 × Minecraft Java, 1 × Left 4 Dead 2), ein gestoppter ohne Container, ein Compose-Minecraft; beantworten nur die Abfrage der Spiele (`labor/spielserver/main.go`); `labor/spiele.sh weg` räumt ab |
| Server-Ebene | Cron sichert `app`, Timer für `shop`, Cron für ein archiviertes Projekt, SSH-Schlüssel, Anmeldung an einer erfundenen Registry |

## DNS umstellen

```bash
labor/dns.sh zeigen
labor/dns.sh setzen app 10.77.0.20          # A-Eintrag auf den neuen Server
labor/dns.sh setzen hostdeck-neu 10.77.0.20      # Testadresse für die Probe-Kopie
labor/dns.sh setzen _minecraft._tcp '0|5|25565|spiel.beispiel.test.' SRV
labor/dns.sh loeschen hostdeck-neu
```

## Zustände einspielen

`labor/zustand.sh` stellt her, was sonst Tage dauert: Projekt über den Leitstand stoppen und starten,
„unhealthy“, Neustart-Schleife, Platte voll (eine kleine ext4-Platte über ein Loop-Gerät unter `/srv/platte`;
die Überwachung misst kein tmpfs), Generalprobe vordatieren, Dienst hart beenden. Standard ist der alte
Server, `SERVER=neu labor/zustand.sh …` nimmt den neuen. `platte-weg` hängt die Testplatte wieder aus. Zeitabhängiges entsteht durch Vordatieren der Dateien in `/var/lib/hostdeck`, nie durch
Verstellen der Uhr (die Container teilen die Uhr des Hosts).

## Browser

Playwright-Chromium auf dem Host. `browser/lib.js` leitet jede Domain aus `beispiel.test` an die IP, die beim
Öffnen laut Zone gilt; ein offener Kontext behält sie wie ein Browser mit zwischengespeichertem DNS.

```bash
node labor/browser/aktion.js hostdeck.beispiel.test GET /api/lauf
node labor/browser/aktion.js hostdeck.beispiel.test STOP projekt:blog
node labor/browser/bild.js hostdeck.beispiel.test '#/umzug' labor/lauf/bilder/umzug.png 390 hell
node labor/browser/klick.js hostdeck.beispiel.test '[{"geh":"#/umzug"},{"text":"#lt-now-h"}]'
node labor/browser/live.js                      # Live und Spielserver (braucht labor/spiele.sh)
node labor/browser/bilder-live.js               # Bilder von Live und 6 Std in fünf Breiten
node labor/browser/widerspruch.js --zustand blog-gestoppt --ruhig 'blog.*(antwortet|erreichbar|Leere)'
node labor/browser/werte.js                     # Werte je Projekt nachbilden, alle Seiten nach „[object“, NaN, JS-Fehlern absuchen
node labor/browser/seitenfenster.js              # Seitenfenster der Übersicht mit echten Mausklicks (1440 und 390)
node labor/browser/sogelassen.js                 # „Bewusst so lassen“ und „Bewusst offen lassen“ mit echten Mausklicks
```

`LAB_MAP=hostdeck=10.77.0.10` hält einen Namen auf einer IP fest, etwa um nach der DNS-Umstellung den alten Leitstand
zu öffnen (wie ein alter Tab). `zaehlen.js <host> '#/umzug' [--ui <git-stand>]` zählt Hauptknöpfe und sichtbare
Abschnitte; mit `--ui` liefert der Browser die Oberfläche aus einem älteren Stand, im selben
Labor-Zustand. Mehrere `dns.sh setzen` in derselben Sekunde sind unbedenklich: Die Seriennummer der Zone steigt
immer.

`widerspruch.js` geht alle Seiten in 1440 dunkel und 390 hell durch, sammelt jede Statusmarke und prüft die
Regeln I1, I2, I3, I6, I7 (ruhende Container je Grund gleich gezählt in Übersicht, Projekte und API), I8
(„Alle N Projekte laufen“ nur, wenn es alle sind) und waagerechtes Rollen (Ausgabe in `labor/lauf/pruefer/`).

## Übersicht und Überwachung

- **Zustände:** `zustand.sh ue-einrichten` legt die Beispiele Z1 bis Z6 an (Kopf von `zustand.sh`), danach
  stellen `altbestand`, `warnung-blog`, `backup-alt`, `grenzen-niedrig`, `last`, `dienst-neu`, `leer-an` und
  `verlauf-weg` die Bildzustände her (a alles läuft, b Projekt gestoppt mit Altbestand, c Warnung und Last,
  d frisch, e Neustart mit Spitze, f Umzug geplant).
- **Verlauf:** Der Labor-Host hat cgroup v1, also keine Werte je Container. `zustand.sh verlauf` baut
  `labor/verlauf` und schreibt damit über den echten Speicher-Code (`ueberwachung.Store`) 30 Tage Verlauf für
  Server, Platte, Container und Projekte: mit Spitzen, einer Lücke, einem Neustart und einem Ausfall von `shop`.
  Kerne, Arbeitsspeicher und Platte übernimmt es vom laufenden Dienst, damit die Teile nie größer als das Ganze
  sind.
- **Zeitzone:** Die Labor-Server laufen auf UTC. `LAB_TZ=Europe/Berlin` (oder `tz` in `lib.context`) stellt
  den Browser auf eine andere Zeitzone.
- **Bildmatrix:** `node labor/browser/matrix.js <zustand> <ordner> [voll]` nimmt Übersicht und Überwachung
  (1 Std, 24 Std, 30 Tage) in 1440 und 390 dunkel auf, mit `voll` dazu 1920, 1440 hell, 1024, 390 hell und
  320. Daneben legt es die Antworten der API und die Statusmarken als JSON ab. Dateinamen:
  `<zustand>-<seite>-<breite>-<dunkel|hell>.jpg`, vorher und nachher gleich.
- **Prüfer:** `widerspruch.js` prüft zusätzlich I10 bis I19 (Projektwort, Containerzahlen, Domainmenge und
  -zustand, Kennzahlen über alle sechs Zeiträume, Farben nach Grenzen, Absichtliches nie rot, Gesamtstatus,
  Uhrzeiten mit Datum, Teile ≤ Ganzes). `--grenzen` setzt für I15 die Prozessor-Grenze kurz um und danach
  zurück.
- **Bedienung (Gate 3):** `node labor/browser/bedienung.js` klickt Zeitraum, Live, Zoom (Ziehen und
  Tastatur), Sortieren, Auf- und Zuklappen, Grenzen ändern (ungültig, außerhalb, gültig, zurück) und
  Aktualisieren durch. Es misst Zielgrößen, Fokus und Kontraste der Tagesbalken in 1440 und 390 und sammelt
  Konsolenfehler.

Neu aufgesetzter Server (Drehbuch S11): einen dritten Server aus dem Image `hostdecklab-neu` starten (wie in
`start.sh`, etwa `--name hostdecklab-frisch --hostname neu --ip 10.77.0.30`), Labor-Wurzel und Proxy wie dort
eintragen, das im Leitstand heruntergeladene Backup hineinkopieren und dem Weg im README folgen
(`hostdeck restore --repo`). Den Leitstand dort erreicht der Browser mit `LAB_MAP=hostdeck=10.77.0.30`. `stop.sh`
räumt ihn mit weg.

## Sicherheitsregeln

- Nie eine Verbindung zu echten Servern des Nutzers, nie echte DNS-Änderungen.
- Geekbench nie starten (`GEEKBENCH=0`); den Leistungstest höchstens einmal kurz.
- Tuning nur Vorschau oder mit `VT_PREFIX` und `VT_BIN`, nie gegen den echten Kernel (die Container sind
  privilegiert und teilen den Kernel des Hosts).
- Auf `alt` keine globalen `vm.*`- oder `kernel.*`-Werte: Der Umzug wendet sie per `sysctl -p` auf `neu` an,
  also auf den Host.
- Am Ende `labor/stop.sh`.

## Bekannte Labor-Artefakte

- cgroup v1 auf dem Host: Messwerte je Container und CPU-Vorrang sind im Labor nicht aussagekräftig.
- Die Zertifikatsstelle stellt nur 90-Tage-Zertifikate aus (eigene Pebble-Einstellung); mit dem
  Standardprofil meldete jede Seite „Zertifikat läuft in 5 Tagen ab“.
