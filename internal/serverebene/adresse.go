package serverebene

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Hit ist eine Fundstelle der alten Adresse: Datei und Zeile, nie Inhalte.
type Hit struct {
	Path string `json:"pfad"`
	Line int    `json:"zeile"`
}

// Limits begrenzen die Suche.
type Limits struct {
	Files int   // so viele Dateien höchstens
	Bytes int64 // so viel lesen höchstens
	Hits  int   // so viele Fundstellen höchstens
}

// DefaultLimits reichen für einen Server mit vielen Projekten und halten
// die Suche bei Spielständen und Uploads kurz.
var DefaultLimits = Limits{Files: 200000, Bytes: 2 << 30, Hits: 200}

// skipDirs sind Ordner, in denen nur Werkzeuge und Zwischenspeicher liegen.
// Logs schreibt ein Programm selbst; eine Adresse darin ist Verlauf, keine
// Einstellung.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".cache": true, "__pycache__": true, ".venv": true, "vendor": true, "logs": true, "log": true}

// logFile: app.log, app.log.1, latest.log
func logFile(name string) bool {
	if strings.HasSuffix(name, ".log") {
		return true
	}
	if i := strings.Index(name, ".log."); i > 0 {
		return strings.Trim(name[i+5:], "0123456789.gz") == ""
	}
	return false
}

// FindAddress sucht in Textdateien unter 1 MB nach den Adressen. skip
// sind Ordner, die ausgelassen werden (Datenbanken, Volumes). Das Ergebnis
// ist nach Pfad sortiert; cut sagt, ob eine Grenze die Suche beendet hat.
func FindAddress(ctx context.Context, root string, dirs, skip, addrs []string, lim Limits) (hits []Hit, cut bool) {
	if len(addrs) == 0 {
		return nil, false
	}
	full := func(p string) string {
		if root == "" {
			return p
		}
		return filepath.Join(root, p)
	}
	skipSet := map[string]bool{}
	for _, d := range skip {
		skipSet[filepath.Clean(d)] = true
	}
	dirs = topDirs(dirs)
	files := 0
	var read int64
	needles := make([][]byte, len(addrs))
	for i, a := range addrs {
		needles[i] = []byte(a)
	}
	buf := make([]byte, maxFile)
	for _, d := range dirs {
		base := full(d)
		err := filepath.WalkDir(base, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rel := d
			if r, err := filepath.Rel(base, p); err == nil && r != "." {
				rel = filepath.Join(d, r)
			}
			if e.IsDir() {
				if p != base && (skipDirs[e.Name()] || skipSet[rel]) {
					return filepath.SkipDir
				}
				return nil
			}
			if !e.Type().IsRegular() || logFile(e.Name()) {
				return nil
			}
			fi, err := e.Info()
			if err != nil || fi.Size() == 0 || fi.Size() >= maxFile {
				return nil
			}
			if files++; files > lim.Files || read+fi.Size() > lim.Bytes {
				cut = true
				return fs.SkipAll
			}
			f, err := os.Open(p)
			if err != nil {
				return nil
			}
			n, _ := f.Read(buf[:fi.Size()])
			f.Close()
			read += int64(n)
			b := buf[:n]
			if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
				return nil // keine Textdatei
			}
			for _, line := range matchLines(b, needles) {
				hits = append(hits, Hit{Path: rel, Line: line})
				if len(hits) >= lim.Hits {
					cut = true
					return fs.SkipAll
				}
			}
			return nil
		})
		if err != nil || cut {
			break
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Line < hits[j].Line
	})
	return hits, cut
}

// topDirs nimmt nur Ordner, die nicht in einem anderen der Liste liegen.
func topDirs(dirs []string) []string {
	clean := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d != "" && filepath.IsAbs(d) {
			clean = append(clean, filepath.Clean(d))
		}
	}
	sort.Strings(clean)
	var out []string
	for _, d := range clean {
		if len(out) > 0 {
			last := out[len(out)-1]
			if d == last || strings.HasPrefix(d, strings.TrimSuffix(last, "/")+"/") {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

// matchLines nennt die Zeilen, in denen eine Adresse für sich steht: Bei
// 1.2.3.4 zählt weder 11.2.3.4 noch 1.2.3.45.
func matchLines(b []byte, needles [][]byte) []int {
	var out []int
	line := 1
	start := 0
	for i := 0; i <= len(b); i++ {
		if i < len(b) && b[i] != '\n' {
			continue
		}
		l := b[start:i]
		for _, n := range needles {
			if containsAddr(l, n) {
				out = append(out, line)
				break
			}
		}
		line++
		start = i + 1
	}
	return out
}

func containsAddr(l, n []byte) bool {
	off := 0
	for {
		i := bytes.Index(l[off:], n)
		if i < 0 {
			return false
		}
		i += off
		end := i + len(n)
		var before, after bool
		if bytes.IndexByte(n, ':') >= 0 { // IPv6
			before = i == 0 || !(isHex(l[i-1]) || l[i-1] == ':')
			after = end >= len(l) || !(isHex(l[end]) || l[end] == ':')
		} else {
			before = i == 0 || !addrChar(l[i-1])
			after = end >= len(l) || !(isDigit(l[end]) || (l[end] == '.' && end+1 < len(l) && isDigit(l[end+1])))
		}
		if before && after {
			return true
		}
		off = i + 1
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool { return isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'f') }

func addrChar(c byte) bool { return isDigit(c) || c == '.' }
