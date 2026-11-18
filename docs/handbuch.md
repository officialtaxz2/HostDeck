# HostDeck: Handbuch

Wie HostDeck auf dem Server arbeitet, Seite für Seite und Befehl für Befehl. Installieren,
Aktualisieren und Entfernen stehen im [README](../README.md).

## Die Dienste

`sudo hostdeck einrichten` (das Installationsskript ruft es auf) legt das Programm nach
`/usr/local/lib/hostdeck/hostdeck` (danach heißt der Befehl überall einfach `hostdeck`), die Daten
nach `/var/lib/hostdeck`, einen eigenen Benutzer `hostdeck-web` und zwei systemd-Dienste an:

- `hostdeck-dienst` läuft als root und macht die eigentliche Arbeit. Er hat keinen Netzwerk-Port, nur
  einen Unix-Socket. Er erfasst den Server jede Nacht um 03:30 neu und auf Knopfdruck im Leitstand.
- `hostdeck-web` läuft als `hostdeck-web` in einer systemd-Sandbox, liefert die Seite aus und reicht Anfragen
  an den Dienst weiter. Er darf keine Netzwerkverbindungen aufbauen, nicht in Docker, Caddy,
  `/root`, `/home` oder `/var/lib/hostdeck` schauen und nichts schreiben außer seinem Socket.

Erneut aufgerufen bringt `einrichten` eine neue Version an ihren Platz und lässt Daten, Passwörter und
Freischaltung, wie sie sind. Gibt es noch keine Bestandsaufnahme, macht `einrichten` die erste.
Entfernen geht mit `sudo hostdeck deinstallieren` (siehe README).

Von außen ist der Leitstand erst erreichbar, wenn du ihn über Caddy freischaltest (siehe unten).
Bis dahin, und später als Notfallzugang, öffnest du ihn über den **Notausgang**:

```bash
sudo hostdeck notausgang
```

Es erscheinen ein `ssh`-Befehl für den PC und eine Adresse für den Browser. Der
Link gilt zwei Minuten und nur einmal. Danach bleibt die Seite angemeldet, solange du sie nutzt:
nach 30 Minuten ohne Aktivität oder spätestens nach 12 Stunden ist Schluss. Beenden mit Strg+C.

## Leitstand-Passwort

`einrichten` fragt beim ersten Mal nach einem Passwort für den Leitstand: Es schlägt eines vor
(vier Gruppen aus je fünf Zeichen, ohne leicht verwechselbare wie l und 1), oder du gibst ein
eigenes ein (mindestens 12 Zeichen). Ändern oder neu festlegen, auch wenn du es vergessen hast:

```bash
sudo hostdeck passwort
```

Das meldet alle ab. Mit dem Passwort meldest du dich im Browser an, über den Notausgang ohne Link
und später auch von außen. Nach fünf falschen Passwörtern in 15 Minuten ist Anmelden von dieser
Adresse aus 15 Minuten gesperrt; bei sehr vielen Fehlversuchen insgesamt nimmt der Leitstand nur
noch alle zehn Sekunden einen Versuch an (der Notausgang ist davon ausgenommen). Bevor du die
Auswahl speicherst oder ein Update einspielst, fragt der Leitstand das Passwort noch einmal ab;
das gilt dann fünf Minuten. Wer mit dem Link vom Notausgang hereinkommt, hat root schon bewiesen
und wird nicht gefragt. Oben rechts siehst du, wie du angemeldet bist, und kannst dich abmelden,
auch überall auf einmal.

Das **Protokoll** im Leitstand zeigt jede Anmeldung, jeden Fehlversuch, jede Sperre
und jede Änderung mit Zeit und Adresse. Es liegt in `/var/lib/hostdeck/protokoll.jsonl` und hält etwa
die letzten 2 MB. Passwörter stehen nie darin, auch keine falschen.

## Leistungstest

Unter Überwachung > Leistungstest misst HostDeck, was der Prozessor des Servers leistet: Sysbench mit
einem und mit allen Threads, dazu Steal-Time mit mpstat und vmstat. Geekbench 6 läuft nur, wenn du es
ausdrücklich dazunimmst: Es lädt das Ergebnis öffentlich auf browser.geekbench.com hoch, geht nur auf
x86_64 und belegt heruntergeladen rund 480 MB, die du unter Leistungstest wieder entfernen kannst.
Dahinter steckt ein eingebettetes `benchmark.sh`; fehlende Pakete (sysbench, sysstat, …) installiert
es mit apt-get. Ergebnisse und ZIP mit allen Rohdaten liegen in `/var/lib/hostdeck/leistungstest/`, bis
du sie löschst. Im Terminal:

```bash
sudo hostdeck leistungstest [--mit-geekbench] [--sekunden 120]
```

## Tuning

Unter Überwachung > Tuning steuert HostDeck das eingebettete `tune.sh` (vps-tune 2.3.0-lean): den
Ist-Zustand lesen, ein Preset (mixed, throughput, safe) mit Schaltern in der Vorschau ansehen und
anwenden, den CPU-Vorrang einzelner Dienste setzen und alles zurücknehmen. Vor jeder Änderung
sichert das Skript den Originalwert. Bootparameter wie `mitigations=off` wirken erst nach einem
Neustart; dann bietet die Seite ihn an. Anwenden, Zurücknehmen, CPU-Vorrang und Neustart fragen
dein Passwort. Im Terminal:

```bash
sudo hostdeck tuning status
sudo hostdeck tuning vorschau mixed
sudo hostdeck tuning anwenden mixed
sudo hostdeck tuning zuruecknehmen
```

Beim Umzug ziehen die Dateien von vps-tune nicht einzeln mit. Der neue Leitstand kennt aber das
Tuning des alten Servers und bietet es unter Überwachung > Tuning mit Vorschau an, ebenso den
CPU-Vorrang je Dienst.

## Updates im Leitstand

Unter **Einstellungen > Updates** lädst du eine neue Version hoch: eine Datei, die auf `.hdu` endet, mit `amd64`
oder `arm64` im Namen, je nach Server. HostDeck prüft die Signatur (nur Dateien, die mit dem Schlüssel
der Releases unterschrieben sind), ob die Datei zum Server passt und dass es eine andere Version ist.
Dann legt es die laufende Version als `hostdeck.alt` beiseite, spielt die neue ein und startet beide
Dienste neu. Antwortet die neue Version nicht innerhalb einer Minute, kommt die alte zurück. Die
Seite zeigt den Fortschritt und meldet sich danach von selbst; angemeldet bleibst du dabei.
Während eines Backups oder einer Bestandsaufnahme geht kein Update.

Alles, was etwas tut (Backup, Probelauf, Zurückspielen, Dienste starten, Bestandsaufnahme,
Einrichten), nimmt dieselbe Sperre: Es läuft immer nur eins davon, und der Leitstand zeigt, was
gerade läuft. Bricht ein Backup durch einen Absturz oder Neustart ab, startet der Dienst die
gestoppten Container wieder (außer nach dem „letzten Lauf“ für den Umzug). `hostdeck serve` ist auf
einem eingerichteten Server nicht mehr nötig und verweist auf den Notausgang.

Auf derselben Seite schaltest du den Hinweis auf neue Versionen an oder aus. Ist er an, fragt
HostDeck einmal am Tag bei GitHub nach dem neuesten Release; GitHub sieht dabei nur die Adresse des
Servers und die Version. Eingespielt wird nie von selbst.

## Zugang von außen (Freischalten über Caddy)

Läuft auf dem Server Caddy als systemd-Dienst mit einem Caddyfile, kann `hostdeck` den Leitstand unter
einer eigenen Adresse freischalten, etwa `hostdeck.example.de`. Danach öffnest du ihn dort mit dem
Leitstand-Passwort, auch am Handy und ohne SSH. Vorher braucht die Adresse beim DNS-Anbieter einen
A-Eintrag auf den Server, wie deine anderen Subdomains; gibt es auch einen AAAA-Eintrag, muss er
ebenfalls auf den Server zeigen.

Im Leitstand unter **Einstellungen > Zugang von außen**: Adresse eingeben, „Prüfen“ zeigt, ob sie auf diesen
Server zeigt und was sich an Caddy ändert, „Freischalten“ macht es nach einer Bestätigung mit dem
Passwort. Oder am Server:

```bash
sudo hostdeck freischalten hostdeck.example.de      # zeigt den Plan und fragt nach
sudo hostdeck freischalten                     # zeigt den Stand
sudo hostdeck freischalten --zuruecknehmen     # wieder nur Notausgang
```

Was dabei geschieht:

- `hostdeck` legt neben dem Caddyfile eine Datei `hostdeck.caddy` an (nur der Eintrag für den Leitstand) und
  hängt ans Ende des Caddyfiles zwei Zeilen an: einen Kommentar und `import hostdeck.caddy`. Sonst ändert
  es am Caddyfile nichts. Bindet das Caddyfile die Datei schon ein (etwa mit `import *.caddy`),
  bleibt es ganz unverändert.
- Caddy reicht die Anfragen an einen Unix-Socket weiter (`/run/hostdeck-caddy/web.sock`), den systemd
  für `hostdeck-web` anlegt und nur der Gruppe von Caddy öffnet. `hostdeck-web` bekommt dafür weiterhin
  keinen Netzwerk-Port.
- Vorher **probt** `hostdeck`, ob die Prüfung taugt: Deine jetzige Konfiguration muss `caddy validate`
  bestehen, und eine Kopie des Caddyfiles mit einem absichtlich kaputten Eintrag muss abgelehnt
  werden. Die Kopie liegt kurz neben dem Caddyfile und wird gleich gelöscht; deine Dateien rührt
  die Probe nicht an. Geht das nicht, schaltet `hostdeck` nicht frei.
- Dann fragt es alle deine Seiten über Caddy ab, legt die alten Fassungen der Dateien in
  `/var/lib/hostdeck/caddy-rueckweg.json` (falls der Server mittendrin ausfällt: der nächste Start des
  Dienstes legt sie zurück), schreibt die Dateien, lässt `caddy validate`
  prüfen, lädt Caddy neu, wartet bis zu zwei Minuten auf das Zertifikat und vergleicht die Seiten
  mit vorher. Lehnt Caddy die Dateien ab, antwortet die neue Seite nicht oder antwortet eine
  andere Seite schlechter als vorher, legt `hostdeck` beide Dateien byte-gleich zurück und lädt Caddy
  wieder; deine Seiten laufen dann wie vorher.
- Wird der Vorgang abgebrochen (Strg+C, Dienst hält an), räumt `hostdeck` trotzdem vollständig auf.
  Läuft Caddy beim Zurücknehmen gerade nicht, nimmt `hostdeck` die Dateien trotzdem heraus.
- Von außen gilt nur das Passwort. Einmal-Links vom Notausgang gelten dort nie, und eine Anmeldung
  gilt nur an dem Eingang, an dem sie entstand. Die Bremsen zählen je Besucher (bei IPv6 je
  /64-Netz), damit Fremde den Besitzer nicht aussperren können; von einer Adresse, von der zuletzt
  das richtige Passwort kam, gilt die Bremse für alle nicht. Nach fünf falschen Bestätigungen ist
  die Anmeldung beendet. Der Notausgang bleibt immer, und durch ihn kommt nur root oder wer
  `sudo hostdeck notausgang` aufgerufen hat.

Zurücknehmen geht im Leitstand auf derselben Seite oder mit `--zuruecknehmen`, mit derselben
Prüfung. `einrichten` (auch bei einem Update) behält die Freischaltung; `deinstallieren`
nimmt sie vorher zurück. Alles steht im Protokoll.

`sudo hostdeck einrichten` fragt beim ersten Einrichten gleich nach der Adresse und schaltet frei; mit
`n` geht das auch später.

## Backup im Leitstand

Als Dienst sichert `hostdeck` auf dem Server selbst, in `/var/lib/hostdeck/backup` (ein restic-Repository,
verschlüsselt mit dem Backup-Passwort). Im Leitstand unter **Backup**:

- **Probelauf** zeigt, was ein Backup tun würde; er stoppt nichts und sichert nichts.
- **Backup erstellen** (mit dem Leitstand-Passwort bestätigen) prüft erst den Platz: Es muss
  reichen, und 2 GB bleiben frei. Dann stoppt `hostdeck` jedes Projekt kurz, sichert es und startet es
  wieder. Die Ausgabe erscheint live; Fragen des Laufs, etwa „verwaltete Spielserver im Panel
  stoppen“, kommen als Knöpfe. **Abbrechen** startet gestoppte Container gleich wieder. Schließt du
  den Browser, läuft der Lauf weiter.
- Auf dem Server bleiben die letzten **zwei gültigen** Backups; ältere gibt `hostdeck` nach jedem
  gültigen Lauf frei.
- **Herunterladen** (mit Bestätigung) liefert den ganzen Backup-Ordner als **eine Datei**
  (`hostdeck-backup-<server>-<datum>.tar`). Sie entsteht beim Lesen, braucht also keinen zusätzlichen
  Platz. Bricht der Download ab, setzt der Browser ihn fort, solange sich das Backup nicht geändert
  hat. Während des Downloads startet kein anderer Lauf. Jeder Download steht im Protokoll.

Das **Backup-Passwort** zeigt der Leitstand nie an. `sudo hostdeck einrichten` legt es beim ersten Mal an
und zeigt es einmal; du bestätigst mit „ja“, dass du es notiert hast. Vorher startet kein Backup.

```bash
sudo hostdeck backup-passwort             # anlegen oder bestätigen, falls noch offen
sudo hostdeck backup-passwort --zeigen    # noch einmal anzeigen
```

Zum Öffnen der heruntergeladenen Datei: entpacken, dann `restic -r hostdeck-backup snapshots` mit dem
Backup-Passwort. Die Ausgabe jedes Laufs liegt zusätzlich in `/var/lib/hostdeck/laeufe/` (die letzten 30).

**Server neu aufsetzen und das heruntergeladene Backup zurückspielen** (etwa nach einer
Neuinstallation desselben Servers; der Probe-Umzug geht dann nicht, weil der alte Server als Quelle
fehlt). Im Leitstand gibt es dafür noch keinen Knopf; auf der Kommandozeile geht es so:

```bash
sudo apt install curl                       # auf einem frischen Debian fehlt es oft
curl -fsSL https://get.docker.com | sudo sh # Docker, falls es fehlt
# Programm aus dem Release (hostdeck-VERSION-linux-ARCH, als ./hostdeck) und die
# heruntergeladene Backup-Datei auf den Server kopieren, dann:
chmod +x hostdeck
tar xf hostdeck-backup-<server>-<datum>.tar           # ergibt den Ordner hostdeck-backup
sudo ./hostdeck restore --repo "$PWD/hostdeck-backup" # fragt nach dem Backup-Passwort, zeigt den Plan, dann „ja“
sudo ./hostdeck einrichten                            # Leitstand als Dienst
sudo hostdeck passwort                                # Leitstand-Passwort
sudo hostdeck freischalten <leitstand-adresse>        # übernimmt die zurückgespielte Leitstand-Datei von Caddy
```

`hostdeck restore` legt die Projekte an, spielt Dateien und Datenbanken ein, installiert Caddy samt
Konfiguration und Zertifikaten und startet, was beim Backup lief. Bricht ein Schritt ab (etwa ein
Image ließ sich nicht laden), startest du `hostdeck restore` einfach noch einmal: Es frischt dann nur auf.
Was im Archiv liegt (`/etc`, `/root` und Ähnliches), kommt nicht von selbst zurück. Der Leitstand
steht danach wieder auf „Umzug geplant“; im Alltag schaltest du das unter Umzug mit „Umzug absagen“
um.

## Umzug mit dem Lotsen

Unter **Umzug** auf „Umzug planen“, dann führt der Leitstand Schritt für Schritt: Auswahl, Backup,
Probelauf, DNS vorbereiten (nur die Testadresse `hostdeck-neu.<deine Leitstand-Domain>` anlegen), Generalprobe,
Umzugstag vorbereiten, Umzugstag, Domains umstellen (erst alle anderen, die Leitstand-Adresse zum Schluss)
und der Abschluss auf dem neuen Server. Oben steht immer nur, was jetzt dran ist, mit einem Knopf. Die
Einzelheiten der Schritte stehen in den folgenden Abschnitten.

Bleibt der Umzugstag stehen (Fehler drüben, Dienst neu gestartet), steht das Formular für einen neuen
Start direkt im Kasten; der neue Start nimmt den letzten Lauf von vorhin, die Seiten bleiben so lange
aus. Nach einem **Rückweg** sagt der Lotse, was zurückgegangen ist und dass drüben noch Projekte laufen;
„Probe-Umzug wiederholen“ steht daneben. Hat der neue Server schon übernommen (eigener Leitstand dort),
hält ein neuer Umzugstag vor dem letzten Lauf an: Dann den neuen Server beim Anbieter neu aufsetzen und
wieder mit dem Probe-Umzug beginnen.

Nach der Ablösung bleiben die umgezogenen Projekte auf dem alten Server aus und warnen nicht; die
Domains prüft der neue Server. Was auf dem alten Server trotzdem läuft, neu startet oder volläuft,
meldet die Überwachung dort weiter.

## Probe-Umzug im Leitstand

Mit einem gültigen Backup auf dem Server spielt `hostdeck` es unter **Umzug** direkt auf einen neuen
Server, zur Probe. Der alte Server läuft dabei weiter; Caddy, Domains und DNS bleiben, wie sie sind.

1. Neuen Server bestellen (Ubuntu oder Debian, frisch installiert), Anmeldung als root mit Passwort.
2. Im Leitstand Adresse, Port (meist 22), Benutzer (meist root) und Passwort eintragen. Das Passwort
   geht nur für diesen Lauf an den neuen Server und wird nirgends gespeichert, auch nicht im
   Protokoll.
3. Dazu dein **Backup-Passwort** eintippen. Es geht an den neuen Server, damit er das Backup öffnen
   kann; der Leitstand vergleicht es nur und speichert es nicht. So reicht das Leitstand-Passwort
   allein nicht, um die Daten im Klartext auf einen fremden Server zu holen. Nach fünf falschen
   Versuchen ist der Probe-Umzug 15 Minuten gesperrt; jeder Fehlversuch steht im Protokoll.
4. **Probelauf** (mit dem Leitstand-Passwort bestätigen): `hostdeck` meldet sich an, zeigt den
   Fingerabdruck des neuen Servers zum Bestätigen, bringt sich selbst nach `~/.hostdeck/bin/hostdeck` und
   prüft dort, was ein Zurückspielen täte. Eingespielt wird nichts; fehlt Docker noch, sagt der
   Probelauf das nur und prüft den Rest trotzdem.
5. **Probe-Umzug starten**: Fehlt Docker, bietet `hostdeck` an, es mit dem Skript von get.docker.com zu
   installieren. Dann spielt es das letzte gültige Backup ein, startet die Projekte und prüft
   Dienste, Ports und Datenbank-Zeilen. Fragen kommen als Knöpfe, **Abbrechen** geht jederzeit
   (eine laufende Docker-Installation läuft noch zu Ende).

Eingespielt wird immer der neueste gültige Lauf, den die Seite Umzug nennt. Ein zweiter Lauf auf
denselben Server spielt Dateien neu ein; einen Datenbank-Dump spielt `hostdeck` aber nur in eine leere
Datenbank. Hat der letzte Lauf genau diesen Dump schon eingespielt und stimmen die Zeilen je
Tabelle, lässt `hostdeck` die Datenbank, wie sie ist (was die Projekte dort seitdem geändert haben,
bleibt). Liegt dort etwas anderes, meldet es einen Fehler: dann die Datenbank leeren oder den
Server neu aufsetzen. Ist das Ziel dieser Server selbst (gleiche `/etc/machine-id`), bricht `hostdeck`
ab.

Das Backup verlässt den alten Server nur durch die SSH-Verbindung: Der neue Server liest es über
einen Port, den nur er selbst unter 127.0.0.1 sieht, nur zum Lesen und mit einem Schlüssel nur für
diesen Lauf. Das Backup-Passwort reist ebenfalls über SSH und liegt danach in `~/.hostdeck/passwort` auf dem neuen
Server (nur für root lesbar), damit `hostdeck verify` dort später prüfen kann. Den Fingerabdruck merkt sich `hostdeck` in
`/var/lib/hostdeck/umzug/bekannte-server`; meldet sich der Server später mit einem anderen, fragt es
deutlich nach. Die Ausgabe jedes Laufs liegt in `/var/lib/hostdeck/umzug/laeufe/` (die letzten 30).

Danach laufen die Projekte auf beiden Servern. Was nur einmal laufen darf (etwa ein Bot mit
demselben Zugangsschlüssel), stoppst du auf dem neuen Server nach der Probe.

## Bestandsaufnahme starten

Als Dienst erfasst HostDeck den Server jede Nacht und auf Knopfdruck. Von Hand, ohne Dienst:

```bash
sudo hostdeck discover
```

`sudo` ist nötig, damit `hostdeck` Docker abfragen und in Volumes hineinschauen kann. Der Lauf dauert
meist ein bis wenige Minuten, weil Ordnergrößen mit niedriger Priorität berechnet werden. Wer es
eilig hat, nimmt `--no-sizes`.

`discover` schaut an diesen Orten:

- alles, was Docker nutzt: Compose-Projekte, Volumes, eingebundene Ordner, Images, auch
  Spielserver, die ein Panel wie Calagopus/Wings anlegt (auch wenn sie gerade aus sind; bei
  Minecraft nennt `hostdeck` MOTD und Welt aus der `server.properties`, damit man sie auseinanderhält)
- Caddy und die Ordner in `/opt` und `/srv`, die zu keinem Projekt gehören
- am Server selbst: Home-Ordner (`/root`, `/home/*`), Webseiten in `/var/www`, eigene Programme in
  `/usr/local/bin`, Cronjobs, selbst angelegte systemd-Dienste und -Timer, und in `/etc` die
  Dateien, die du geändert oder angelegt hast (erkannt über die Paketdatenbank; Dateien, die
  zur Maschine gehören wie `grub` oder `crypttab`, und solche, die Pakete selbst anlegen, zählen nicht)
- die ganze Platte, um zu zeigen, wo der Platz hingeht; größere Ordner an ungewöhnlichen Orten
  (ab 100 MB, direkt unter `/` jede Größe) landen als „offen“ in der Auswahl

Caches wie `.cache`, `.npm` oder `.vscode-server` in Home-Ordnern schlägt `hostdeck` ab 1 MB zum
Weglassen vor. Ein Timer und der Dienst, den er startet, erscheinen als eine Aufgabe; Zeitpläne
stehen lesbar da, etwa „täglich 03:30“.
Inhalte von Dateien landen nie im Ergebnis: Von SSH-Schlüsseln und `docker login` nennt der
Bericht nur Namen, in Befehlen von Cronjobs und Diensten blendet er Passwörter und Tokens aus.
Was nicht als Datei mitkommt (Benutzer, SSH-Zugang, Firewall, Swap, Kernel-Einstellungen aus
`sysctl`, Paketquellen und Pakete ohne Kernel und Bootloader, Zeitzone), steht im Bericht als
Checkliste für den neuen Server.

Danach liegen im Ordner `hostdeck-ergebnis/` drei Dateien. Sie gehören dem Benutzer, der `sudo`
aufgerufen hat, und sind nur für ihn lesbar:

| Datei | Was drinsteht |
|---|---|
| `bericht.txt` | lesbarer Bericht: Projekte, Datenbanken, Daten, Caddy-Routen, Warnungen |
| `manifest.yaml` | Vorschlag, was wie gesichert wird; hier triffst du deine Entscheidungen |
| `inventory.json` | alles Gefundene als Daten, bei jedem Lauf neu |

Das Terminal zeigt eine Kurzfassung mit allen Warnungen. Den ganzen Bericht liest du mit
`less hostdeck-ergebnis/bericht.txt` oder lädst ihn herunter.

`manifest.yaml` schreibt `hostdeck discover` nur beim ersten Lauf als Vorschlag. Deine Entscheidungen
triffst du am einfachsten im Leitstand unter Backup > Auswahl. Ein neuer `discover`-Lauf
überschreibt sie nie; er meldet nur, was auf dem Server neu dazugekommen oder weggefallen ist. Das
Format steht in [docs/manifest-v1.md](docs/manifest-v1.md), falls du die Datei lieber von Hand
bearbeitest.

**Wichtig:** `manifest.yaml` und `inventory.json` enthalten Domains und Pfade deines Servers. Lege
sie nicht in ein öffentliches Repo. Passwörter und andere Werte von Umgebungsvariablen speichert
`hostdeck` nie, nur deren Namen.

### Optionen

| Option | Bedeutung |
|---|---|
| `--out ORDNER` | Ergebnisordner (Standard `hostdeck-ergebnis`) |
| `--no-sizes` | keine Größen berechnen |
| `--scan-root ORDNER` | hier nach Ordnern ohne Projekt suchen, mehrfach angebbar (Standard `/opt` und `/srv`) |
| `--caddyfile DATEI` | Caddy-Konfiguration (Standard `/etc/caddy/Caddyfile`) |
| `--docker-socket DATEI` | Docker-Socket (Standard `/var/run/docker.sock`) |
| `--size-timeout DAUER` | Zeitlimit pro Ordner bei der Größenberechnung (Standard `3m`) |

## Leitstand ohne Dienst

Mit `sudo hostdeck einrichten` läuft der Leitstand als Dienst, und dieser Abschnitt ist nicht nötig. Ohne
Dienst geht es auch so:

```bash
./hostdeck serve
```

`hostdeck serve` braucht kein `sudo`. Es liest die Ergebnisse von `discover` und zeigt im Terminal zwei
Zeilen zum Kopieren:

1. Einen `ssh`-Befehl für deinen PC. Unter Windows öffnest du dafür eine PowerShell, fügst ihn ein
   und lässt das Fenster offen; es bleibt leer, das ist richtig. Der Befehl baut einen Tunnel vom
   PC zum Server.
2. Eine Adresse wie `http://127.0.0.1:7322/#…`, die du auf dem PC im Browser öffnest.

Die Seite ist ein Leitstand für deinen Server. Die **Übersicht** sagt in einem Satz, wie es steht,
zeigt Kennzahlen, was dich gerade braucht und was heute passiert ist. Die **Überwachung** zeigt
dazu eine Verbindungskarte: welche Domain zu welchem Projekt und welchen Daten führt. Dazu kommen **Projekte**, **Domains**,
**Backup** (letzter Lauf und Verlauf), **Umzug** (Ablauf und Checkliste für den neuen Server) und
**Speicherplatz**. Ohne Dienst stammen alle Zahlen aus der letzten Bestandsaufnahme und den
Backup-Läufen; die Seite zeigt, von wann sie sind. Als Dienst (`hostdeck einrichten`) ist vieles live:
Projekte mit Zustand und Last, eine eigene Seite je Projekt mit Logs, Verlauf, Prozessen und
Konfiguration, Überwachung mit Diagrammen, Starten und Stoppen mit Vorschau. Strg+K sucht nach
Projekten, Domains und Seiten und kennt Befehle wie „Logs von …“ oder „Backup starten“.

In der **Auswahl** (unter Backup) entscheidest du, was mitkommt. Beim ersten Öffnen stellt sie nur
die Fragen, bei denen es auf dich ankommt: was `hostdeck` nicht selbst einordnen konnte, ob die
vorgeschlagenen Projekte passen, die großen Brocken und Einträge mit Hinweisen; den Rest übernimmst
du auf einmal. Danach zeigt die Auswahl-Übersicht, was auf den neuen Server kommt, was ins Archiv
geht und was bewusst zurückbleibt.

In den Bereichen (Projekte, Daten ohne Projekt, Am Server selbst, …) liegt jeder Eintrag in einer
von drei Kisten: **Übernehmen** (sichern und auf dem neuen Server starten), **Archivieren**
(sichern, aber nicht starten) oder **Weglassen** (mit kurzem Grund). Ein Klick auf den Namen öffnet
die Details; dort stellst du einzelne Datenbanken, Ordner und Images ein und nimmst große
Unterordner aus, etwa die Spielserver-Backups in `/opt/calagopus/wings/backups`. **Speichern**
(oder Strg+S) schreibt `hostdeck-ergebnis/manifest.yaml`; sonst ändert die Seite nichts. Hell oder
dunkel folgt dem System und lässt sich oben umstellen.

Nach einem neuen `sudo hostdeck discover` genügt auf der Seite „Neu laden“. Neu gefundene Einträge sind
mit „neu“ markiert und kommen als Fragen; weggefallene fragt die Seite zum Schluss ab. Beenden mit
Strg+C im Terminal.

| Option | Bedeutung |
|---|---|
| `--out ORDNER` | Ergebnisordner von `discover` (Standard `hostdeck-ergebnis`) |
| `--port PORT` | Port auf 127.0.0.1 (Standard `7322`) |

**Sicherheit:** Die Seite lauscht nur auf `127.0.0.1` und ist aus dem Internet nicht erreichbar,
nur über deinen SSH-Tunnel. Jeder Start erzeugt einen neuen Schlüssel. Er steht im Link hinter dem
`#`, landet so in keinem Log, und die Seite tauscht ihn einmal gegen eine Anmeldung (ein Cookie,
das nur `hostdeck` lesen kann). Ohne Anmeldung liefert `hostdeck serve` keine Daten. Kommentare,
die du von Hand in `manifest.yaml` geschrieben hast, gehen beim Speichern über die Seite verloren.

## Warum `discover` nichts verändern kann

- Der Docker-Client lehnt alles außer lesenden Anfragen auf einer festen Liste von Endpunkten ab,
  bevor er Docker überhaupt anspricht.
- Dateien werden nur zum Lesen geöffnet; gelesen werden Ordnerinhalte, Größen und kleine
  Kennungen wie `PG_VERSION` oder der Kopf von SQLite-Dateien, keine Konfigurationsinhalte.
- `git` läuft mit `--no-optional-locks` und als Eigentümer des Repos statt als root; `caddy adapt`
  übersetzt nur die Konfiguration.
- Geschrieben wird nur in den Ergebnisordner.

## Aufbau

| Ordner | Inhalt |
|---|---|
| `cmd/hostdeck` | Kommandozeile |
| `internal/dockerapi` | kleiner Docker-Client: lesend, dazu ein zweiter mit wenigen erlaubten Aktionen fürs Backup |
| `internal/discover` | Bestandsaufnahme: Container, Pfade, Datenbanken, Git, Caddy, Ports, Größen |
| `internal/inventory` | Format von `inventory.json` |
| `internal/manifest` | Format, Prüfung und Vorschlag von `manifest.yaml` |
| `internal/report` | deutscher Textbericht |
| `internal/web` | Leitstand und Auswahl (HTML, CSS, JavaScript ohne Fremdbibliotheken) und ihre API |
| `internal/runlog` | Liste der Backup-Läufe (`backup-laeufe.json`) für den Leitstand |
| `internal/archiv` | Backup-Ordner als tar-Datei zum Herunterladen, ohne Kopie auf der Platte, fortsetzbar |
| `internal/backup` | Backup: Plan aus dem Manifest, Ablauf, Bericht |
| `internal/restore` | Zurückspielen und Prüfen auf dem neuen Server |
| `internal/restic` | restic holen, prüfen und aufrufen |
| `internal/pc` | früherer Weg am PC (Menü, ssh) und der Empfänger, den heute auch der Probe-Umzug nutzt |
| `internal/remote` | Absprache zwischen PC und Server über die ssh-Verbindung |
| `internal/outdir` | sicheres Schreiben in den Ergebnisordner |
| `internal/fixture` | nachgebauter Testserver |
| `labor` | Prüflabor mit nachgebauten Servern in Docker |
| `release` | Release bauen und signieren, Lizenzliste, Prüfung gegen eine Sperrliste vor dem Hochladen |
| `docs` | Handbuch und Manifest-Schema |
