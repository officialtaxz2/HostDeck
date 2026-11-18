package caddy

import (
	"bytes"
	"path/filepath"
	"strings"
)

type line struct {
	n    int    // Zeilennummer ab 0
	code string // ohne Kommentar und Leerraum am Rand
}

// topLevel liefert die Zeilen des Caddyfiles, die außerhalb jedes Blocks
// stehen.
func topLevel(b []byte) []line {
	var out []line
	depth := 0
	for i, l := range strings.Split(string(b), "\n") {
		code := stripComment(l)
		if depth == 0 {
			out = append(out, line{i, strings.TrimSpace(code)})
		}
		depth += strings.Count(code, "{") - strings.Count(code, "}")
		if depth < 0 {
			depth = 0
		}
	}
	return out
}

// stripComment entfernt einen Kommentar: # am Zeilenanfang oder nach einem
// Leerzeichen, nicht in Anführungszeichen.
func stripComment(s string) string {
	quoted := false
	for i, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '#' && !quoted && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return s[:i]
		}
	}
	return s
}

// Covered sagt, ob eine import-Zeile des Caddyfiles (außerhalb aller
// Blöcke) die Datei target schon einbindet, etwa „import *.caddy“. dir ist
// der Ordner des Caddyfiles; relative Pfade gelten von dort.
func Covered(caddyfile []byte, dir, target string) bool {
	for _, l := range topLevel(caddyfile) {
		f := strings.Fields(l.code)
		if len(f) < 2 || f[0] != "import" {
			continue
		}
		pat := strings.Trim(f[1], `"`)
		if !strings.ContainsAny(pat, "/.*") {
			continue // Snippet wie „import logging“
		}
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(dir, pat)
		}
		if ok, _ := filepath.Match(pat, target); ok {
			return true
		}
	}
	return false
}

// ImportComment steht über der Zeile, die HostDeck ins Caddyfile schreibt.
const ImportComment = "# Leitstand von HostDeck; zurücknehmen mit: sudo hostdeck freischalten --zuruecknehmen"

// AddImport hängt eine import-Zeile für target ans Ende des Caddyfiles.
func AddImport(caddyfile []byte, target string) []byte {
	var b bytes.Buffer
	b.Write(caddyfile)
	if len(caddyfile) > 0 && !bytes.HasSuffix(caddyfile, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.WriteString("\n" + ImportComment + "\nimport " + target + "\n")
	return b.Bytes()
}

// RemoveImport nimmt die import-Zeile für target wieder heraus, samt dem
// Kommentar von HostDeck und der Leerzeile davor. ok ist false, wenn es keine
// solche Zeile gibt.
func RemoveImport(caddyfile []byte, target string) ([]byte, bool) {
	lines := strings.Split(string(caddyfile), "\n")
	top := map[int]bool{}
	for _, l := range topLevel(caddyfile) {
		f := strings.Fields(l.code)
		if len(f) == 2 && f[0] == "import" && strings.Trim(f[1], `"`) == target {
			top[l.n] = true
		}
	}
	if len(top) == 0 {
		return caddyfile, false
	}
	var out []string
	for i := 0; i < len(lines); i++ {
		if top[i] {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "# Leitstand von HostDeck") && i+1 < len(lines) && top[i+1] {
			// Kommentar von HostDeck; die Leerzeile davor hat HostDeck auch angelegt.
			if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) == "" {
				out = out[:n-1]
			}
			continue
		}
		out = append(out, lines[i])
	}
	return []byte(strings.Join(out, "\n")), true
}
