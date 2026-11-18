package dockerapi

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// markers deuten auf ein installiertes Docker hin, auch wenn es gerade
// nicht läuft. Für Tests umlenkbar.
var markers = []string{"/var/lib/docker", "/etc/docker", "/usr/bin/dockerd", "/usr/sbin/dockerd", "/usr/local/bin/dockerd"}

// markerRoot ist das Wurzelverzeichnis für markers (Tests).
var markerRoot = "/"

// Missing sagt, ob err daher kommt, dass es auf diesem Server kein Docker
// gibt: Der Socket fehlt, und nichts sonst deutet auf Docker hin. Ein
// installiertes Docker, das gerade nicht läuft, ist kein Fehlen.
func Missing(err error) bool {
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	for _, m := range markers {
		if _, err := os.Lstat(filepath.Join(markerRoot, m)); err == nil {
			return false
		}
	}
	return true
}
