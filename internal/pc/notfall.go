package pc

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// notfallText liegt als LIESMICH.txt im Notfall-Ordner.
const notfallText = `HostDeck – Notfall-Ordner
====================

In diesem Ordner liegt alles, was du brauchst, um das Backup wieder zu
öffnen, auch wenn der alte Server nicht mehr da ist:

  hostdeck.exe            HostDeck für Windows (dieser PC)
  hostdeck-linux-amd64    HostDeck für Linux-Server mit Intel/AMD-Prozessor
  hostdeck-linux-arm64    HostDeck für Linux-Server mit ARM-Prozessor
  SHA256SUMS         Prüfsummen dieser Dateien

Das Backup selbst liegt im Ordner daneben, benannt nach dem Server
(zum Beispiel vps1). Es ist mit deinem Backup-Passwort verschlüsselt.
Ohne dieses Passwort lässt es sich nicht öffnen.

Wiederherstellen auf einem neuen Server:
  hostdeck.exe restore --server root@<neuer-server>
  (kommt mit Meilenstein M2b; bis dahin bitte nichts von Hand löschen)

Notfalls geht es auch ohne HostDeck mit restic %s
(https://github.com/restic/restic/releases):
  restic -r <Backup-Ordner>\<Server> snapshots
  restic -r <Backup-Ordner>\<Server> restore <Snapshot-ID> --target <Ziel>
Jeder Lauf hat das Tag sbm-run=<Zeitpunkt>; der Snapshot mit dem Tag
sbm-lauf enthält lauf.json mit der Zuordnung aller Einträge.
`

// WriteNotfall legt die Programme und eine Anleitung in <folder>/notfall
// ab, damit nach einem Reset nicht genau das Werkzeug fehlt. Vorhandene
// gleiche Dateien bleiben unverändert.
func WriteNotfall(folder, binDir, self, resticVersion string) error {
	dir := filepath.Join(folder, "notfall")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	files := map[string]string{"hostdeck-linux-amd64": "", "hostdeck-linux-arm64": "", "SHA256SUMS": ""}
	for name := range files {
		files[name] = filepath.Join(binDir, name)
	}
	files[filepath.Base(self)] = self
	var missing []string
	for name, src := range files {
		b, err := os.ReadFile(src)
		if err != nil {
			missing = append(missing, name)
			continue
		}
		dst := filepath.Join(dir, name)
		if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, b) {
			continue
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return err
		}
	}
	text := fmt.Sprintf(notfallText, resticVersion)
	text = strings.ReplaceAll(text, "\n", "\r\n") // für den Windows-Editor
	if err := os.WriteFile(filepath.Join(dir, "LIESMICH.txt"), []byte(text), 0o600); err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("nicht im Notfall-Ordner, weil sie neben HostDeck fehlen: %s", strings.Join(missing, ", "))
	}
	return nil
}
