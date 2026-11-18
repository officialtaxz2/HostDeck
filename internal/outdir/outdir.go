// Package outdir schreibt Dateien in den Ergebnisordner von HostDeck.
package outdir

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ServiceDataDir ist der Datenordner von HostDeck als Dienst (ort.DataDir).
const ServiceDataDir = "/var/lib/hostdeck"

// WritePrivate schreibt eine Datei, die nur der Besitzer lesen darf, und
// ersetzt eine vorhandene erst, wenn die neue vollständig geschrieben ist.
func WritePrivate(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// GiveToSudoUser übergibt Ordner und Dateien dem Benutzer, der sudo
// aufgerufen hat. So kann er sie ohne root lesen und etwa per WinSCP
// herunterladen; für andere Benutzer bleiben sie unlesbar (0600). Ohne sudo
// passiert nichts, ebenso im Datenordner des Dienstes (/var/lib/hostdeck): Der
// gehört root.
func GiveToSudoUser(dir string, files ...string) {
	if abs, err := filepath.Abs(dir); err == nil && (abs == ServiceDataDir || strings.HasPrefix(abs, ServiceDataDir+"/")) {
		return
	}
	uid, err1 := strconv.Atoi(os.Getenv("SUDO_UID"))
	gid, err2 := strconv.Atoi(os.Getenv("SUDO_GID"))
	if err1 != nil || err2 != nil || uid == 0 || os.Geteuid() != 0 {
		return
	}
	_ = os.Chown(dir, uid, gid)
	for _, f := range files {
		_ = os.Chown(filepath.Join(dir, f), uid, gid)
	}
}
