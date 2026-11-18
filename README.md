# HostDeck

HostDeck ist ein Leitstand für deinen eigenen Linux-Server. Es läuft als Dienst auf dem Server und
wird im Browser bedient: Projekte und Container überwachen und steuern, Domains und Zertifikate im
Blick behalten, Backups von Hand erstellen und herunterladen und den ganzen Server Schritt für
Schritt auf einen neuen umziehen, auch zu einem anderen Hoster oder Betriebssystem.

HostDeck ändert nichts, ohne es vorher zu zeigen: Jeder Eingriff hat eine Vorschau, und du
bestätigst ihn mit deinem Passwort. Es braucht keine fremden Dienste und kein Konto irgendwo.

*English summary below.*

## Was HostDeck kann

- **Übersicht und Überwachung:** Prozessor, Arbeitsspeicher, Platte und Netz live und im Verlauf,
  Warnungen bei Problemen, auf Wunsch als Mitteilung aufs Handy (Web Push, ohne fremden Dienst).
- **Projekte:** Docker-Compose-Projekte und Container mit Zustand, Last, Logs und Verlauf; starten,
  stoppen, neu starten und aktualisieren mit Rückweg.
- **Domains:** alle Seiten aus Caddy mit Erreichbarkeit und Zertifikat.
- **Backup:** von Hand erstellen, verschlüsselt mit restic, als eine Datei herunterladen.
- **Umzug:** ein Lotse führt durch Auswahl, Backup, Probe-Umzug auf den neuen Server,
  Generalprobe und Umzugstag.
- **Pflege:** Speicherplatz aufräumen (mit Papierkorb), Leistung, Leistungstest, Server-Tuning,
  offene Ports schließen, Protokoll aller Anmeldungen und Änderungen.

## Voraussetzungen

- Linux-Server mit **Debian 12 oder 13** oder **Ubuntu 22.04 oder 24.04**, Prozessor amd64 (x86_64)
  oder arm64, mit systemd. Andere Systeme gehen oft auch, sind aber nicht geprüft.
- Zugang als root (oder mit `sudo`).
- **Docker** ist für Projekte und Container nötig. Ohne Docker zeigt HostDeck alles andere
  (Überwachung, Speicherplatz, Sicherheit, Domains).
- **Caddy** als systemd-Dienst, wenn der Leitstand unter einer eigenen Adresse erreichbar sein soll.
  Ohne Caddy öffnest du ihn über einen SSH-Tunnel (Notausgang).

## Installieren

Auf dem Server:

```bash
curl -fsSL https://github.com/officialtaxz2/HostDeck/releases/latest/download/install.sh | sudo bash
```

Das Skript prüft zuerst den Server, zeigt dann, was es tun wird, und fragt „Weiter?“. Erst danach
lädt es HostDeck, prüft die Signatur und die Prüfsumme und richtet den Dienst ein. Die Einrichtung
fragt nach einem Passwort für den Leitstand, legt das Backup-Passwort an, fragt, ob HostDeck einmal
am Tag nach neuen Versionen sehen soll, und bietet an, den Leitstand über Caddy erreichbar zu machen.

Wer das Skript erst lesen möchte:

```bash
curl -fsSLO https://github.com/officialtaxz2/HostDeck/releases/latest/download/install.sh
less install.sh
sudo bash install.sh
```

| Angabe | Bedeutung |
|---|---|
| `--ja` | keine Rückfragen (Hinweis auf neue Versionen an; Passwort danach mit `sudo hostdeck passwort`) |
| `--version 1.0.0` | eine bestimmte Version statt der neuesten (auch `HOSTDECK_VERSION=1.0.0`) |
| `--trotzdem` | auch auf einem System, das nicht offiziell unterstützt ist |

Mit Angaben: `curl -fsSL …/install.sh | sudo bash -s -- --ja`

<details>
<summary>Ohne Skript, von Hand</summary>

Lade aus dem [neuesten Release](https://github.com/officialtaxz2/HostDeck/releases/latest) die Datei
`hostdeck-VERSION-linux-amd64` (oder `-arm64`), `SHA256SUMS` und `SHA256SUMS.sig`. Prüfen und
einrichten:

```bash
echo 'hostdeck namespaces="hostdeck-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKWjAPn14E+qo8aDeTH0gzaR/xekPR2T6/z+72464ZaP' > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I hostdeck -n hostdeck-release -s SHA256SUMS.sig < SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
chmod +x hostdeck-*-linux-amd64
sudo ./hostdeck-*-linux-amd64 einrichten
```

</details>

## Den Leitstand öffnen

**Über eine eigene Adresse** (mit Caddy): Lege beim DNS-Anbieter einen A-Eintrag an, etwa
`hostdeck.example.de` auf die IP des Servers. Dann bei der Einrichtung die Adresse eingeben oder
später:

```bash
sudo hostdeck freischalten hostdeck.example.de
```

HostDeck legt dafür eine eigene Datei neben das Caddyfile, prüft alles mit `caddy validate`, lädt
Caddy neu und vergleicht deine anderen Seiten mit vorher. Geht etwas schief, legt es alles
byte-gleich zurück.

**Über den Notausgang** (immer möglich, auch ohne Caddy):

```bash
sudo hostdeck notausgang
```

Das zeigt einen `ssh`-Befehl für deinen PC und einen Link, der zwei Minuten und nur einmal gilt.

## Aktualisieren

- **Im Leitstand** unter Einstellungen > Updates die Datei `hostdeck-VERSION-linux-ARCH.hdu` aus
  dem Release hochladen. HostDeck prüft die Signatur, spielt die neue Version ein und holt die alte
  zurück, wenn die neue nicht antwortet.
- **Am Server** das Installationsskript noch einmal aufrufen. Es bringt die neueste Version auf
  demselben Weg wie das Update im Leitstand, also mit Rückweg. Auf eine ältere Version geht es nur
  nach Rückfrage zurück (oder mit `--ja --version …`).

Auf Wunsch sieht HostDeck einmal am Tag bei GitHub nach, ob es eine neue Version gibt, und zeigt das
im Leitstand. Eingespielt wird nie von selbst.

## Entfernen

```bash
sudo hostdeck deinstallieren
```

Das zeigt vorher, was passiert und was bleibt: Es schließt den Zugang von außen, öffnet Ports, die
HostDeck geschlossen hat, nimmt auf Wunsch das Server-Tuning zurück und entfernt Dienste, Programm
und Benutzer. Deine Projekte, Container und Caddy bleiben, wie sie sind. Die Daten von HostDeck
(`/var/lib/hostdeck`, darin Backups und Protokoll) löscht es nur, wenn du „löschen“ eingibst.

## Was HostDeck auf dem Server anlegt

| Was | Wo |
|---|---|
| Programm und Befehl | `/usr/local/lib/hostdeck/hostdeck`, `/usr/local/bin/hostdeck` |
| Daten (Einstellungen, Protokoll, Verlauf, Backups) | `/var/lib/hostdeck` |
| Backup-Passwort | `/root/.hostdeck/passwort` (nur root) |
| Dienste | `hostdeck-dienst` (root, ohne Netzwerk-Port), `hostdeck-web` (eigener Benutzer `hostdeck-web`, abgeschottet) |
| nach dem Freischalten | `hostdeck.caddy` neben dem Caddyfile, Socket `hostdeck-web.socket` |
| nach dem Schließen von Ports | nftables-Tabelle `hostdeck_schutz`, Dienst `hostdeck-schutz` |

Wie HostDeck aufgebaut ist und was es schützt, steht in [SECURITY.md](SECURITY.md).

## Mehr

- [Handbuch](docs/handbuch.md): alle Seiten und Befehle im Einzelnen
- [Änderungen](CHANGELOG.md)
- [Format der Auswahl (`manifest.yaml`)](docs/manifest-v1.md)

## Selbst bauen

Voraussetzung ist Go 1.27.4 oder neuer.

```bash
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o hostdeck-linux-amd64 ./cmd/hostdeck
```

Die Tests laufen gegen einen nachgebauten Server (`internal/fixture`). Das Prüflabor in `labor/`
baut mit Docker ganze Server nach, auch für die Prüfung des Installationsskripts auf allen
unterstützten Systemen (`labor/matrix/`). Releases baut `release/bauen.sh`.

Jedes Release nach 1.0.0 lässt sich nachbauen: den Tag auschecken und mit der Go-Version aus dem
Release-Text bauen. Die Prüfsumme muss mit der aus `SHA256SUMS` übereinstimmen.

```bash
v=1.2.3   # Version des Releases
git checkout "v$v"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=$v" -o "hostdeck-$v-linux-amd64" ./cmd/hostdeck
sha256sum "hostdeck-$v-linux-amd64"
```

## Lizenz

MIT, siehe [LICENSE](LICENSE). Die Lizenzen der eingebauten Bibliotheken und Schriften stehen in
[THIRD_PARTY_LICENSES.txt](THIRD_PARTY_LICENSES.txt).

---

## In English

HostDeck is a self-hosted control panel for a single Linux server. It runs as a service on the
server and is used in the browser: monitor and manage Docker Compose projects and containers, keep
an eye on Caddy sites and certificates, create and download encrypted backups (restic) by hand,
and move the whole server to a new one step by step. Every change is previewed first and confirmed
with your password; HostDeck needs no third-party services or accounts.

The interface and documentation are currently in **German only**; English is planned.

Install on Debian 12/13 or Ubuntu 22.04/24.04 (amd64 or arm64, systemd):

```bash
curl -fsSL https://github.com/officialtaxz2/HostDeck/releases/latest/download/install.sh | sudo bash
```

The script checks the system, shows what it will do and asks before changing anything. Releases are
signed; the script verifies `SHA256SUMS` with `ssh-keygen -Y verify` before installing. Remove with
`sudo hostdeck deinstallieren`. Licensed under MIT.
