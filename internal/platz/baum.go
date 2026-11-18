// Package platz misst, was auf der Systemplatte Platz belegt, ordnet es
// ein und löscht auf Wunsch einzelne Ordner und Dateien. Gemessen wird wie
// du: belegter Platz, nur dieses Dateisystem, Hardlinks einmal. Gelöscht
// wird nur, was die Schutzstufen erlauben, ohne Verknüpfungen zu folgen und
// ohne in eingehängte Platten zu steigen.
package platz

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Entry ist ein Ordner, eine Datei oder eine Verknüpfung im Baum. Die
// kurzen Namen halten die gespeicherte Datei klein.
type Entry struct {
	Name   string   `json:"n"`
	Size   int64    `json:"s,omitempty"` // belegt, bei Ordnern mit allem darin
	Dir    bool     `json:"d,omitempty"`
	Link   string   `json:"l,omitempty"` // Ziel einer Verknüpfung
	Files  int64    `json:"f,omitempty"` // Dateien darin (Ordner)
	Newest int64    `json:"t,omitempty"` // neueste Änderung darin, Unix-Sekunden
	Mount  bool     `json:"m,omitempty"` // Einhängepunkt: nicht gemessen
	Flat   bool     `json:"x,omitempty"` // Inhalt nicht gespeichert (klein)
	Denied bool     `json:"u,omitempty"` // nicht lesbar
	Kids   []*Entry `json:"k,omitempty"`
	RestN  int64    `json:"rn,omitempty"` // weitere Einträge, zusammengefasst
	RestS  int64    `json:"rs,omitempty"`
}

// Tree ist eine Messung der ganzen Platte.
type Tree struct {
	Root    *Entry    `json:"wurzel"`
	At      time.Time `json:"stand"`
	Took    int64     `json:"dauer_s"`
	Partial bool      `json:"teilweise,omitempty"`
	Seen    int64     `json:"eintraege"`
}

// Options steuern eine Messung.
type Options struct {
	// Root ist der echte Ordner, der für / steht (in Tests ein Testordner).
	Root string
	// Mounts sind Einhängepunkte als Pfade auf dem Server; dort steigt die
	// Messung nicht hinein. / selbst zählt nicht.
	Mounts []string
	// MinDir: Ordner darunter behalten ihren Inhalt nicht (Standard 1 MB);
	// die Liste misst ihn bei Bedarf frisch.
	MinDir int64
	// Keep: so viele Einträge behält ein Ordner (Standard 50).
	Keep int
	// Limit bricht nach so vielen Einträgen ab (Standard 5 Millionen).
	Limit int64
	// Deadline bricht nach dieser Zeit ab; leer: 30 Minuten.
	Deadline time.Time
	// Gentle: niedrige Priorität und kleine Pausen.
	Gentle bool
	// Full: jeder Ordner behält bis zu Keep Einträge, auch kleine (für die
	// frische Liste eines einzelnen Ordners).
	Full bool
	// Count bekommt regelmäßig die Zahl der bisher gesehenen Einträge.
	Count *atomic.Int64
}

func (o *Options) defaults() {
	if o.MinDir <= 0 {
		o.MinDir = 1 << 20
	}
	if o.Keep <= 0 {
		o.Keep = 50
	}
	if o.Limit <= 0 {
		o.Limit = 5_000_000
	}
	if o.Deadline.IsZero() {
		o.Deadline = time.Now().Add(30 * time.Minute)
	}
	if o.Root == "" {
		o.Root = "/"
	}
}

type walker struct {
	ctx     context.Context
	o       Options
	mounts  map[string]bool
	rootDev uint64
	seen    map[[2]uint64]bool
	n       int64
	partial bool
	full    bool // Inhalt aller Ordner behalten (Messung eines Teilbaums)
}

// Measure misst ab host (ein Pfad auf dem Server, meist /).
func Measure(ctx context.Context, host string, o Options) *Tree {
	o.defaults()
	start := time.Now()
	w := newWalker(ctx, o)
	var root *Entry
	if o.Gentle {
		// Schonend in einem eigenen Thread: nice 19 und Platte nur, wenn
		// sonst niemand will. Der Thread wird nicht zurückgegeben; Go beendet
		// ihn mit der Goroutine, so erbt kein anderer Teil des Dienstes die
		// niedrige Priorität.
		done := make(chan *Entry, 1)
		go func() {
			runtime.LockOSThread()
			lowerPriority()
			done <- w.measure(host)
		}()
		root = <-done
	} else {
		root = w.measure(host)
	}
	return &Tree{Root: root, At: start, Took: int64(time.Since(start).Seconds()), Partial: w.partial, Seen: w.n}
}

func newWalker(ctx context.Context, o Options) *walker {
	w := &walker{ctx: ctx, o: o, mounts: map[string]bool{}, seen: map[[2]uint64]bool{}, full: o.Full}
	for _, m := range o.Mounts {
		if m = path.Clean(m); m != "/" {
			w.mounts[m] = true
		}
	}
	if fi, err := os.Lstat(o.Root); err == nil {
		_, w.rootDev, _, _ = statOf(fi)
	}
	return w
}

func (w *walker) real(host string) string {
	return filepath.Join(w.o.Root, filepath.FromSlash(strings.TrimPrefix(host, "/")))
}

func (w *walker) measure(host string) *Entry {
	host = path.Clean("/" + host)
	fi, err := os.Lstat(w.real(host))
	if err != nil {
		return &Entry{Name: path.Base(host), Denied: true}
	}
	if !fi.IsDir() {
		e, _ := w.leaf(host, fi)
		return e
	}
	return w.dir(host, fi)
}

func (w *walker) stop() bool {
	if w.partial {
		return true
	}
	if w.ctx.Err() != nil || w.n >= w.o.Limit || (w.n%1024 == 0 && time.Now().After(w.o.Deadline)) {
		w.partial = true
	}
	return w.partial
}

// leaf misst eine Datei oder Verknüpfung; dup: ein weiterer Link einer
// schon gezählten Datei.
func (w *walker) leaf(host string, fi fs.FileInfo) (e *Entry, dup bool) {
	size, _, ino, links := statOf(fi)
	e = &Entry{Name: path.Base(host), Newest: fi.ModTime().Unix()}
	if fi.Mode()&fs.ModeSymlink != 0 {
		e.Link, _ = os.Readlink(w.real(host))
		e.Size = size
		return e, false
	}
	if links > 1 && ino != 0 {
		_, dev, _, _ := statOf(fi)
		k := [2]uint64{dev, ino}
		if w.seen[k] {
			return e, true // schon gezählt
		}
		w.seen[k] = true
	}
	e.Size = size
	return e, false
}

func (w *walker) dir(host string, fi fs.FileInfo) *Entry {
	e := &Entry{Name: path.Base(host), Dir: true, Newest: fi.ModTime().Unix()}
	if host == "/" {
		e.Name = "/"
	}
	e.Size, _, _, _ = statOf(fi)
	ents, err := os.ReadDir(w.real(host))
	if err != nil {
		e.Denied = true
		return e
	}
	newest := int64(0)
	kids := make([]*Entry, 0, len(ents))
	for _, d := range ents {
		if w.stop() {
			break
		}
		w.n++
		if w.o.Count != nil && w.n%256 == 0 {
			w.o.Count.Store(w.n)
		}
		if w.o.Gentle && w.n%4096 == 0 {
			time.Sleep(2 * time.Millisecond)
		}
		ch := path.Join(host, d.Name())
		info, err := d.Info()
		if err != nil {
			continue
		}
		var k *Entry
		switch {
		case info.IsDir():
			_, dev, _, _ := statOf(info)
			if w.mounts[ch] || (w.rootDev != 0 && dev != 0 && dev != w.rootDev) {
				k = &Entry{Name: d.Name(), Dir: true, Mount: true}
			} else {
				k = w.dir(ch, info)
			}
			e.Files += k.Files
		default:
			var dup bool
			k, dup = w.leaf(ch, info)
			if k.Link == "" && !dup {
				e.Files++
			}
		}
		e.Size += k.Size
		if k.Newest > newest {
			newest = k.Newest
		}
		kids = append(kids, k)
	}
	if newest > 0 {
		e.Newest = newest
	}
	w.prune(e, kids)
	return e
}

// prune behält die größten Einträge, den Rest fasst es zusammen. Kleine
// Ordner behalten ihren Inhalt nicht.
func (w *walker) prune(e *Entry, kids []*Entry) {
	sortEntries(kids)
	for i, k := range kids {
		if i < w.o.Keep && (w.full || k.Size >= w.o.MinDir || i < 10) {
			if k.Dir && !w.full && k.Size < w.o.MinDir && len(k.Kids)+int(k.RestN) > 0 {
				k.Kids, k.RestN, k.RestS, k.Flat = nil, 0, 0, true
			}
			e.Kids = append(e.Kids, k)
			continue
		}
		e.RestN++
		e.RestS += k.Size
	}
}

func sortEntries(es []*Entry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].Size != es[j].Size {
			return es[i].Size > es[j].Size
		}
		return es[i].Name < es[j].Name
	})
}

// Find sucht host im Baum; nil: nicht darin (zu klein oder nicht gemessen).
func (t *Tree) Find(host string) *Entry {
	if t == nil || t.Root == nil {
		return nil
	}
	e := t.Root
	host = path.Clean("/" + host)
	if host == "/" {
		return e
	}
	for _, name := range strings.Split(strings.TrimPrefix(host, "/"), "/") {
		var next *Entry
		for _, k := range e.Kids {
			if k.Name == name {
				next = k
				break
			}
		}
		if next == nil {
			return nil
		}
		e = next
	}
	return e
}

// Subtract zieht nach dem Löschen von host freed von allen Ordnern darüber
// ab und nimmt den Eintrag heraus.
func (t *Tree) Subtract(host string, freed int64, files int64) {
	if t == nil || t.Root == nil {
		return
	}
	host = path.Clean("/" + host)
	parts := strings.Split(strings.TrimPrefix(host, "/"), "/")
	e := t.Root
	for i, name := range parts {
		e.Size = max(0, e.Size-freed)
		e.Files = max(0, e.Files-files)
		idx := -1
		for j, k := range e.Kids {
			if k.Name == name {
				idx = j
				break
			}
		}
		if idx < 0 {
			if e.RestN > 0 {
				e.RestN--
				e.RestS = max(0, e.RestS-freed)
			}
			return
		}
		if i == len(parts)-1 {
			e.Kids = append(e.Kids[:idx:idx], e.Kids[idx+1:]...)
			return
		}
		e = e.Kids[idx]
	}
}

// Replace setzt im Ordner dir den Eintrag name auf e (nil: heraus) und
// führt Größe, Dateien und neueste Änderung aller Ordner darüber nach.
func (t *Tree) Replace(dir, name string, e *Entry) {
	if t == nil || t.Root == nil {
		return
	}
	chain := []*Entry{t.Root}
	if d := path.Clean("/" + dir); d != "/" {
		cur := t.Root
		for _, part := range strings.Split(strings.TrimPrefix(d, "/"), "/") {
			var next *Entry
			for _, k := range cur.Kids {
				if k.Name == part {
					next = k
					break
				}
			}
			if next == nil {
				return
			}
			cur = next
			chain = append(chain, cur)
		}
	}
	parent := chain[len(chain)-1]
	var dSize, dFiles int64
	idx := -1
	for i, k := range parent.Kids {
		if k.Name == name {
			idx = i
			dSize, dFiles = -k.Size, -fileCount(k)
			break
		}
	}
	switch {
	case e == nil && idx >= 0:
		parent.Kids = append(parent.Kids[:idx:idx], parent.Kids[idx+1:]...)
	case e == nil:
		return
	case idx >= 0:
		parent.Kids[idx] = e
	default:
		parent.Kids = append(parent.Kids, e)
	}
	if e != nil {
		dSize += e.Size
		dFiles += fileCount(e)
	}
	for _, c := range chain {
		c.Size = max(0, c.Size+dSize)
		c.Files = max(0, c.Files+dFiles)
		if e != nil && e.Newest > c.Newest {
			c.Newest = e.Newest
		}
	}
}

// fileCount: Dateien in e (eine, wenn e selbst eine Datei ist).
func fileCount(e *Entry) int64 {
	if e.Dir {
		return e.Files
	}
	if e.Link == "" {
		return 1
	}
	return 0
}

// Save schreibt den Baum gepackt nach file (nur root lesbar).
func (t *Tree) Save(file string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".neu"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	err = json.NewEncoder(zw).Encode(t)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, file)
}

// Load liest einen gespeicherten Baum; nil: keiner da.
func Load(file string) *Tree {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil
	}
	var t Tree
	if json.NewDecoder(zr).Decode(&t) != nil || t.Root == nil {
		return nil
	}
	return &t
}
