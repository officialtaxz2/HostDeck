package platz

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Ident ist Gerät und Inode eines Eintrags. Löschen geht nur, wenn sie
// noch zur Vorschau passen.
type Ident struct {
	Dev uint64 `json:"geraet"`
	Ino uint64 `json:"inode"`
}

// Sel ist ein frisch gemessener Eintrag einer Auswahl.
type Sel struct {
	Path  string `json:"pfad"`
	Ident Ident  `json:"-"`
	Dir   bool   `json:"ordner,omitempty"`
	Link  bool   `json:"verknuepfung,omitempty"`
	// Size ist belegt wie du, Hardlinks einmal.
	Size int64 `json:"groesse"`
	// Frees ist, was beim Löschen frei wird: Dateien mit weiteren Links
	// außerhalb der Auswahl zählen nicht.
	Frees int64 `json:"frei"`
	Files int64 `json:"dateien"`
	// Mounts sind Einhängepunkte darin; dann löscht HostDeck nichts davon.
	Mounts []string `json:"einhaengepunkte,omitempty"`
	// Denied zählt Ordner darin, die nicht lesbar waren.
	Denied int64 `json:"nicht_lesbar,omitempty"`
	// Partial: abgebrochen (zu viele Einträge oder zu lange).
	Partial bool `json:"teilweise,omitempty"`
}

// ErrGone: den Eintrag gibt es nicht (mehr).
var ErrGone = errors.New("gibt es nicht mehr")

type inode struct {
	links uint64
	seen  uint64
	size  int64
	sel   int
}

// Inspect misst die Auswahl hosts frisch, ohne Verknüpfungen zu folgen und
// ohne in andere Platten zu steigen. root ist der echte Ordner für /,
// mounts die Einhängepunkte des Servers.
func Inspect(ctx context.Context, root string, hosts []string, mounts []string, limit int64) ([]Sel, error) {
	if root == "" {
		root = "/"
	}
	if limit <= 0 {
		limit = 5_000_000
	}
	w := &inspector{ctx: ctx, root: root, mounts: map[string]bool{}, inodes: map[[2]uint64]*inode{}, limit: limit}
	for _, m := range mounts {
		if m = cleanAbs(m); m != "" && m != "/" {
			w.mounts[m] = true
		}
	}
	if fi, err := os.Lstat(root); err == nil {
		_, w.rootDev, _, _ = statOf(fi)
	}
	out := make([]Sel, 0, len(hosts))
	for i, h := range hosts {
		h = path.Clean("/" + h)
		fi, err := os.Lstat(w.real(h))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, &PathError{h, ErrGone}
			}
			return nil, &PathError{h, err}
		}
		size, dev, ino, _ := statOf(fi)
		s := Sel{Path: h, Ident: Ident{dev, ino}, Dir: fi.IsDir(), Link: fi.Mode()&fs.ModeSymlink != 0}
		if w.mounts[h] || (w.rootDev != 0 && dev != 0 && dev != w.rootDev) {
			s.Mounts = append(s.Mounts, h)
			out = append(out, s)
			continue
		}
		w.sel = i
		w.cur = &s
		if s.Dir {
			s.Size = size
			s.Frees = size
			w.dir(h)
		} else {
			w.file(fi)
		}
		out = append(out, s)
	}
	// Hardlinks: frei wird eine Datei erst, wenn alle ihre Links in der
	// Auswahl liegen.
	for _, in := range w.inodes {
		out[in.sel].Size += in.size
		if in.seen >= in.links {
			out[in.sel].Frees += in.size
		}
	}
	for i := range out {
		out[i].Partial = w.partial
	}
	return out, nil
}

// PathError nennt den Pfad zu einem Fehler.
type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *PathError) Unwrap() error { return e.Err }

type inspector struct {
	ctx     context.Context
	root    string
	mounts  map[string]bool
	rootDev uint64
	inodes  map[[2]uint64]*inode
	sel     int
	cur     *Sel
	n       int64
	limit   int64
	partial bool
}

func (w *inspector) real(host string) string {
	if w.root == "/" {
		return host
	}
	return strings.TrimSuffix(w.root, "/") + host
}

func (w *inspector) file(fi fs.FileInfo) {
	size, dev, ino, links := statOf(fi)
	if fi.Mode()&fs.ModeSymlink == 0 {
		w.cur.Files++
	}
	if links > 1 && ino != 0 && fi.Mode().IsRegular() {
		k := [2]uint64{dev, ino}
		in := w.inodes[k]
		if in == nil {
			in = &inode{links: links, size: size, sel: w.sel}
			w.inodes[k] = in
		}
		in.seen++
		return
	}
	w.cur.Size += size
	w.cur.Frees += size
}

func (w *inspector) dir(host string) {
	ents, err := os.ReadDir(w.real(host))
	if err != nil {
		w.cur.Denied++
		return
	}
	for _, d := range ents {
		if w.partial {
			return
		}
		w.n++
		if w.n >= w.limit || (w.n%1024 == 0 && w.ctx.Err() != nil) {
			w.partial = true
			return
		}
		ch := path.Join(host, d.Name())
		info, err := d.Info()
		if err != nil {
			continue
		}
		if !info.IsDir() {
			w.file(info)
			continue
		}
		size, dev, _, _ := statOf(info)
		if w.mounts[ch] || (w.rootDev != 0 && dev != 0 && dev != w.rootDev) {
			w.cur.Mounts = append(w.cur.Mounts, ch)
			continue
		}
		w.cur.Size += size
		w.cur.Frees += size
		w.dir(ch)
	}
}

// MountsUnder nennt die Einhängepunkte unter host (ohne host selbst).
func MountsUnder(host string, mounts []string) []string {
	host = path.Clean("/" + host)
	var out []string
	for _, m := range mounts {
		if m = cleanAbs(m); m != "" && m != host && under(m, host) {
			out = append(out, m)
		}
	}
	return out
}
