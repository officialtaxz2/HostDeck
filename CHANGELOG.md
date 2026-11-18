# Änderungen

Versionen nach [SemVer](https://semver.org/lang/de/). Neueste oben.

## 1.0.0

Erste öffentliche Fassung.

- **Installieren mit einem Befehl:** `install.sh` prüft den Server, zeigt den Plan, fragt nach,
  prüft Signatur und Prüfsumme und richtet HostDeck ein. Über eine laufende Installation spielt es
  die neue Version wie ein Update ein, mit Rückweg; eine ältere nur nach Rückfrage. Geprüft auf
  Debian 12 und 13 sowie Ubuntu 22.04 und 24.04, mit curl oder wget, mit Docker und Caddy, ohne
  Docker, neben nginx und mit `/tmp` ohne Ausführrecht.
- **Entfernen:** `sudo hostdeck deinstallieren` zeigt vorher, was passiert und was bleibt, nimmt
  Freischaltung, Port-Schutz und auf Wunsch das Tuning zurück und löscht Daten nur nach
  ausdrücklichem „löschen“. Ersetzt `einrichten --entfernen`.
- **Signierte Releases:** `SHA256SUMS` mit Signatur, prüfbar mit `ssh-keygen -Y verify`; dieselbe
  Signatur wie für die Update-Dateien (`.hdu`).
- **Hinweis auf neue Versionen:** auf Wunsch einmal am Tag bei GitHub nachsehen; beim Einrichten
  gefragt, im Leitstand unter Updates änderbar.
- **Server ohne Docker:** Bestandsaufnahme und Leitstand funktionieren, ohne Container; keine
  falschen Warnungen.
- **Geekbench** läuft im Leistungstest nur noch auf ausdrücklichen Wunsch, weil es Ergebnisse
  öffentlich hochlädt.
- **Neuer Name:** Die frühere interne Fassung hieß `sbm`. Ein Update im Leitstand stellt eine
  laufende Installation samt Daten, Anmeldung, Freischaltung und Port-Schutz um.
- MIT-Lizenz.
