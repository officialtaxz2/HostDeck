package web

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/manifest"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
)

// Unselect trägt die Einträge keys aus der Auswahl in dir aus, etwa
// Volumes, die das Aufräumen gerade in den Papierkorb gelegt hat. Es
// zählt, wie viele davon in der Auswahl standen. Fehlt die Auswahl oder
// ist sie ungültig, ändert es nichts: Das klärt die Seite Auswahl.
func Unselect(dir string, keys []string) (int, error) {
	return unselect(dir, func(*manifest.Manifest) []string { return keys })
}

// UnselectUnder trägt alle Pfade aus der Auswahl in dir aus, die gleich
// einem der Pfade in paths sind oder darin liegen (Schlüssel path:<pfad>).
// Das braucht das Löschen unter Speicherplatz: Ein gelöschter Ordner nimmt
// alles darin mit.
func UnselectUnder(dir string, paths []string) (int, error) {
	return unselect(dir, func(m *manifest.Manifest) []string {
		var keys []string
		for _, k := range m.Keys() {
			p, ok := strings.CutPrefix(k, "path:")
			if !ok {
				continue
			}
			for _, base := range paths {
				base = strings.TrimSuffix(base, "/")
				if p == base || strings.HasPrefix(p, base+"/") {
					keys = append(keys, k)
					break
				}
			}
		}
		return keys
	})
}

func unselect(dir string, pick func(*manifest.Manifest) []string) (int, error) {
	mPath := filepath.Join(dir, ManifestFile)
	b, err := os.ReadFile(mPath)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	m, err := manifest.Parse(bytes.NewReader(b))
	if err != nil {
		return 0, nil
	}
	n := 0
	for _, k := range pick(m) {
		if m.Remove(k) {
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	out, err := m.Marshal()
	if err != nil {
		return 0, err
	}
	if err := outdir.WritePrivate(mPath, out); err != nil {
		return 0, err
	}
	outdir.GiveToSudoUser(dir, ManifestFile)
	return n, nil
}
