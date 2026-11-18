# Sicherheit

## Eine Lücke melden

Bitte melde Sicherheitslücken **nicht** als öffentliches Issue, sondern vertraulich über GitHub:
im Repository unter **Security > Report a vulnerability**. Beschreibe, was du gefunden hast, wie
es sich auslösen lässt und welche Version betroffen ist. Du bekommst eine Antwort, sobald die Meldung
gelesen ist; ein Fix kommt als neues Release mit einem Hinweis im [CHANGELOG](CHANGELOG.md).

Unterstützt wird die jeweils neueste Version. Ältere Versionen bekommen keine Fixes; aktualisiere
im Leitstand oder mit dem Installationsskript.

## Wie HostDeck aufgebaut ist

HostDeck verwaltet einen Server als root. Damit das nicht von außen angreifbar wird, ist es in zwei
Teile getrennt:

- **`hostdeck-dienst`** läuft als root und tut die eigentliche Arbeit. Er hat **keinen
  Netzwerk-Port**, nur einen Unix-Socket. Bei jeder Verbindung prüft er über den Kernel
  (`SO_PEERCRED`), wer anruft: die Website oder root. Er nimmt nur eine feste Liste von Aufträgen an.
- **`hostdeck-web`** liefert die Seite aus. Es läuft als eigener Benutzer ohne Rechte in einer
  systemd-Sandbox: kein Netzwerk außer Unix-Sockets, kein Schreiben außer seinem Socket, kein Blick
  in Docker, Caddy, `/root`, `/home` oder die Daten von HostDeck. Jede Anfrage reicht es an den Dienst
  weiter, der Sitzung und Rechte selbst prüft.

**Anmeldung.** Von außen gilt nur das Leitstand-Passwort (mindestens 12 Zeichen). Fehlversuche
bremsen je Adresse (bei IPv6 je /64-Netz) und insgesamt. Alles, was etwas ändert, zeigt vorher eine
Vorschau und verlangt das Passwort noch einmal. Jede Anmeldung, jeder Fehlversuch und jede Änderung
steht mit Zeit und Adresse im Protokoll; Passwörter stehen nie darin.

**Notausgang.** `sudo hostdeck notausgang` öffnet den Leitstand über einen SSH-Tunnel mit einem
Einmal-Link. Wer ihn nutzt, hat root auf dem Server schon bewiesen. Der Link gilt nie von außen.

**Zugang von außen.** Nur über Caddy und nur, wenn du ihn freischaltest. Caddy spricht mit
`hostdeck-web` über einen Unix-Socket, der nur der Gruppe von Caddy offen steht.

**Backups.** restic verschlüsselt sie mit dem Backup-Passwort. Der Leitstand zeigt dieses Passwort nie
an; es liegt nur für root lesbar in `/root/.hostdeck/passwort`.

**Releases und Updates.** Jede Update-Datei (`.hdu`) ist mit Ed25519 signiert; HostDeck spielt nur
Dateien ein, die zum eingebauten Schlüssel passen. Für das Installationsskript ist die Liste der
Prüfsummen (`SHA256SUMS`) mit demselben Schlüssel signiert und lässt sich mit `ssh-keygen` prüfen:

```
hostdeck namespaces="hostdeck-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKWjAPn14E+qo8aDeTH0gzaR/xekPR2T6/z+72464ZaP
```

```bash
ssh-keygen -Y verify -f allowed_signers -I hostdeck -n hostdeck-release -s SHA256SUMS.sig < SHA256SUMS
```

**Keine fremden Dienste.** HostDeck braucht kein Konto und schickt keine Daten an Dritte. Nach
außen spricht es nur, wenn du es auslöst: GitHub für den Hinweis auf neue Versionen (wenn
eingeschaltet) und für restic (mit geprüfter Prüfsumme), die Paketquellen deines Systems für
Leistungstest und Umzug, und für Mitteilungen aufs Handy den Push-Dienst deines Browsers (Web Push,
Inhalt verschlüsselt). Geekbench lädt Ergebnisse nur hoch, wenn du es ausdrücklich dazunimmst.
