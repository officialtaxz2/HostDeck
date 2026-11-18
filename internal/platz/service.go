package platz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/aufraeumen"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// Service ist „Ordner und Dateien“ unter Speicherplatz: messen, anzeigen,
// Vorschau und Löschen. Laufsperre, Anmeldung, Bestätigung und Protokoll
// macht der Dienst.
type Service struct {
	// Root ist der echte Ordner für / (Standard /; in Tests ein Testordner).
	Root string
	// File ist der gemerkte Baum (gepackt, nur root lesbar).
	File string
	// Facts liefert, was der Schutz braucht; nil: nur die festen Regeln.
	Facts func(ctx context.Context) Facts
	// Mounts liefert die Einhängepunkte; nil: MountPoints.
	Mounts func() []string
	// Open liefert offene Dateien; nil: OpenFiles.
	Open func() []OpenFile
	// Disk misst Größe und freien Platz des Dateisystems von path.
	Disk func(path string) (total, free int64, err error)
	// Trash ist der Papierkorb von Aufräumen; nil: nur endgültig löschen.
	Trash *aufraeumen.Service
	// Unselect trägt gelöschte Pfade samt allem darin aus der Auswahl aus.
	Unselect func(paths []string) (int, error)
	Now      func() time.Time

	mu      sync.Mutex
	tree    *Tree
	loaded  bool
	meas    *MeasureInfo
	count   atomic.Int64
	plans   map[string]*plan
	deleted []string // während einer Messung gelöscht
	guard   *Guard
	guardAt time.Time
	// refreshed: wann ein Ordner zuletzt mit der Platte abgeglichen wurde.
	refreshed map[string]time.Time
}

// Fehler.
var (
	ErrBusy     = errors.New("es läuft schon eine Messung")
	ErrNothing  = errors.New("nichts ausgewählt")
	ErrNotDir   = errors.New("ist kein Ordner")
	ErrExpired  = errors.New("die Vorschau ist abgelaufen oder schon benutzt; bitte neu öffnen")
	ErrTooMany  = errors.New("zu viele auf einmal (höchstens 500)")
	ErrNoDelete = errors.New("nichts davon lässt sich löschen")
	// ErrIncomplete: Der Schutz kennt gerade nicht alles (siehe
	// Guard.Incomplete); HostDeck löscht und holt nichts zurück.
	ErrIncomplete = errors.New("bitte später noch einmal versuchen")
	// ErrViaLink: Der Pfad führt durch eine Verknüpfung.
	ErrViaLink = errors.New("liegt hinter einer Verknüpfung; HostDeck folgt ihr nicht")
)

// Schritte beim Löschen (dazu die des Papierkorbs).
const (
	StepCheck  = "Stand prüfen"
	StepDelete = "Löschen"
)

// Toleranz zwischen Vorschau und Löschen.
const (
	growAbs    = 1 << 30
	growMin    = 50 << 20
	planTTL    = 15 * time.Minute
	guardTTL   = time.Minute
	liveLimit  = 300_000
	liveTime   = 20 * time.Second
	liveKeep   = 500
	maxPick    = 500
	maxWarn    = 3
	planRemain = 20
)

// MeasureInfo ist der Stand einer Messung.
type MeasureInfo struct {
	Running bool       `json:"laeuft"`
	Why     string     `json:"anlass,omitempty"`
	Since   *time.Time `json:"seit,omitempty"`
	Seen    int64      `json:"eintraege,omitempty"`
	Error   string     `json:"fehler,omitempty"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) root() string {
	if s.Root == "" {
		return "/"
	}
	return s.Root
}

func (s *Service) real(host string) string {
	if s.root() == "/" {
		return host
	}
	return strings.TrimSuffix(s.root(), "/") + host
}

func (s *Service) mounts() []string {
	if s.Mounts != nil {
		return s.Mounts()
	}
	return MountPoints()
}

func (s *Service) openFiles() []OpenFile {
	if s.Open != nil {
		return s.Open()
	}
	return OpenFiles()
}

// Tree liefert den gemerkten Baum; nil: noch nie gemessen.
func (s *Service) Tree() *Tree {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.treeLocked()
}

func (s *Service) treeLocked() *Tree {
	if !s.loaded {
		s.loaded = true
		if s.File != "" {
			s.tree = Load(s.File)
		}
	}
	return s.tree
}

// Measuring sagt, ob gerade gemessen wird.
func (s *Service) Measuring() MeasureInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meas == nil {
		return MeasureInfo{}
	}
	m := *s.meas
	if m.Running {
		m.Seen = s.count.Load()
	}
	return m
}

// Measure misst die ganze Platte (schonend) und merkt den Baum. Es
// wartet, bis die Messung fertig ist; läuft schon eine: ErrBusy.
func (s *Service) Measure(ctx context.Context, why string) error {
	s.mu.Lock()
	if s.meas != nil && s.meas.Running {
		s.mu.Unlock()
		return ErrBusy
	}
	now := s.now()
	s.meas = &MeasureInfo{Running: true, Why: why, Since: &now}
	s.deleted = nil
	s.count.Store(0)
	s.mu.Unlock()

	t := Measure(ctx, "/", Options{Root: s.root(), Mounts: s.mounts(), Gentle: true, Count: &s.count})
	var err error
	if ctx.Err() != nil && t.Seen == 0 {
		err = ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		// Was während der Messung gelöscht wurde, nimmt der neue Baum
		// auch heraus.
		for _, p := range s.deleted {
			if e := t.Find(p); e != nil {
				t.Subtract(p, e.Size, max(e.Files, boolInt(!e.Dir && e.Link == "")))
			}
		}
		s.tree, s.loaded = t, true
		if s.File != "" {
			if serr := t.Save(s.File); serr != nil {
				err = fmt.Errorf("Messung nicht gespeichert: %w", serr)
			}
		}
	}
	s.deleted = nil
	s.meas.Running = false
	s.meas.Seen = t.Seen
	if err != nil {
		s.meas.Error = err.Error()
	}
	return err
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// guardFor liefert den Schutz; fresh: Fakten neu holen.
func (s *Service) guardFor(ctx context.Context, fresh bool) *Guard {
	s.mu.Lock()
	g, at := s.guard, s.guardAt
	s.mu.Unlock()
	if g != nil && !fresh && s.now().Sub(at) < guardTTL {
		return g
	}
	var f Facts
	if s.Facts != nil {
		f = s.Facts(ctx)
	}
	if f.Mounts == nil {
		f.Mounts = s.mounts()
	}
	if f.Swaps == nil {
		f.Swaps = SwapFiles()
	}
	f = s.expandFacts(f)
	g = NewGuard(f, func(p string) bool {
		fi, err := os.Lstat(s.real(p))
		return err != nil || fi.IsDir()
	})
	s.mu.Lock()
	s.guard, s.guardAt = g, s.now()
	s.mu.Unlock()
	return g
}

// expandFacts nimmt zu jedem Pfad der Fakten seinen echten Ort dazu, wenn
// darin eine Verknüpfung steckt: Liegt ein Projekt unter /opt/app und
// /opt/app zeigt auf /data/app, ist auch /data/app geschützt. Die Fakten
// selbst bleiben unverändert.
func (s *Service) expandFacts(f Facts) Facts {
	alt := s.realHost
	own := make([]Own, 0, len(f.Own))
	for _, o := range f.Own {
		own = append(own, o)
		if r := alt(o.Path); r != "" {
			own = append(own, Own{Path: r, What: o.What, Page: o.Page})
		}
	}
	f.Own = own
	projects := make([]Project, 0, len(f.Projects))
	for _, p := range f.Projects {
		files := append([]string(nil), p.Files...)
		for _, x := range p.Files {
			if r := alt(x); r != "" {
				files = append(files, r)
			}
		}
		p.Files = files
		projects = append(projects, p)
		if r := alt(p.Dir); r != "" {
			projects = append(projects, Project{Name: p.Name, Dir: r})
		}
	}
	f.Projects = projects
	conts := make([]Container, 0, len(f.Containers))
	for _, c := range f.Containers {
		binds := append([]Bind(nil), c.Binds...)
		for _, b := range c.Binds {
			if r := alt(b.Source); r != "" {
				binds = append(binds, Bind{Source: r, RW: b.RW})
			}
		}
		c.Binds = binds
		conts = append(conts, c)
	}
	f.Containers = conts
	vols := make([]Volume, 0, len(f.Volumes))
	for _, v := range f.Volumes {
		vols = append(vols, v)
		r, d := alt(v.Path), alt(v.Device)
		if r != "" || d != "" {
			w := v
			if r != "" {
				w.Path = r
			}
			if d != "" {
				w.Device = d
			}
			vols = append(vols, w)
		}
	}
	f.Volumes = vols
	swaps := append([]string(nil), f.Swaps...)
	for _, x := range f.Swaps {
		if r := alt(x); r != "" {
			swaps = append(swaps, r)
		}
	}
	f.Swaps = swaps
	roots := append([]string(nil), f.DockerRoots...)
	for _, x := range append([]string{f.DockerRoot}, lockedDocker...) {
		if r := alt(x); r != "" {
			roots = append(roots, r)
		}
	}
	f.DockerRoots = roots
	homes := map[string]string{}
	for user, h := range f.Homes {
		homes[user] = h
		if r := alt(h); r != "" {
			homes[user+" "] = r // zweiter Schlüssel: derselbe Benutzer am echten Ort
		}
	}
	f.Homes = homes
	return f
}

// realHost ist der echte Ort von host, wenn darin eine Verknüpfung
// steckt; leer: keine oder nicht auflösbar. Fehlt das Ende von host,
// zählt der Ordner darüber.
func (s *Service) realHost(host string) string {
	host = cleanAbs(host)
	if host == "" || host == "/" {
		return ""
	}
	root := filepath.Clean(s.root())
	rest := ""
	for p := host; p != "/"; p = path.Dir(p) {
		r, err := filepath.EvalSymlinks(s.real(p))
		if err != nil {
			rest = path.Join(path.Base(p), rest)
			continue
		}
		out := r
		if root != "/" {
			rel, err := filepath.Rel(root, r)
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				return ""
			}
			out = "/" + rel
		}
		out = path.Join(out, rest)
		if out == host {
			return ""
		}
		return out
	}
	return ""
}

// viaLink nennt den ersten Ordner über host, der eine Verknüpfung (oder
// keine Ordner) ist; leer: keiner.
func (s *Service) viaLink(host string) string {
	host = path.Clean("/" + host)
	parts := strings.Split(strings.TrimPrefix(path.Dir(host), "/"), "/")
	cur := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur += "/" + part
		fi, err := os.Lstat(s.real(cur))
		if err != nil {
			return ""
		}
		if !fi.IsDir() {
			return cur
		}
	}
	return ""
}

// CheckRestore prüft vor dem Zurückholen nach host (Art pfad), ob der Ort
// frei ist: Der Schutz wird frisch gebaut, und host muss sich löschen
// lassen dürfen; sonst legt HostDeck dort nichts hin.
func (s *Service) CheckRestore(ctx context.Context, host string) error {
	host = path.Clean("/" + host)
	g := s.guardFor(ctx, true)
	if why := g.Incomplete(); why != "" {
		return fmt.Errorf("%s; %w", why, ErrIncomplete)
	}
	if l := s.viaLink(host); l != "" {
		return &PathError{l, ErrViaLink}
	}
	if v := g.Check(host); v.Level != Free {
		return fmt.Errorf("%s %s", host, v.Reason)
	}
	return nil
}

// Row ist eine Zeile der Liste.
type Row struct {
	Name   string  `json:"name"`
	Path   string  `json:"pfad"`
	Size   int64   `json:"groesse"`
	Dir    bool    `json:"ordner,omitempty"`
	Link   string  `json:"ziel,omitempty"`
	Files  int64   `json:"dateien,omitempty"`
	Newest int64   `json:"neueste,omitempty"`
	Mount  bool    `json:"einhaengepunkt,omitempty"`
	Denied bool    `json:"nicht_lesbar,omitempty"`
	Guard  Verdict `json:"schutz"`
}

// Hidden ist Platz, den gelöschte, aber noch offene Dateien belegen.
type Hidden struct {
	Proc  string `json:"prozess"`
	PID   int    `json:"pid"`
	Size  int64  `json:"groesse"`
	Files int    `json:"dateien"`
	Path  string `json:"beispiel"`
}

// DiskInfo ist die Platte von /.
type DiskInfo struct {
	Total int64 `json:"gesamt"`
	Free  int64 `json:"frei"`
}

// Listing ist der Inhalt eines Ordners.
type Listing struct {
	Path    string      `json:"pfad"`
	Self    Row         `json:"ordner"`
	Rows    []Row       `json:"eintraege"`
	RestN   int64       `json:"weitere,omitempty"`
	RestS   int64       `json:"weitere_groesse,omitempty"`
	Live    bool        `json:"frisch,omitempty"`
	Partial bool        `json:"teilweise,omitempty"`
	NoTree  bool        `json:"nie_gemessen,omitempty"`
	At      *time.Time  `json:"stand,omitempty"`
	Took    int64       `json:"dauer_s,omitempty"`
	Measure MeasureInfo `json:"messung"`
	Disk    *DiskInfo   `json:"platte,omitempty"`
	Hidden  []Hidden    `json:"nicht_zuzuordnen,omitempty"`
	Trash   bool        `json:"papierkorb"`
	// Problem: Der Schutz kennt gerade nicht alles; löschen geht dann nicht.
	Problem string `json:"hinweis,omitempty"`
}

// Listing liefert den Inhalt von host, größte zuerst. Steht der Ordner im
// gemerkten Baum, kommt er von dort; kleine Ordner misst es frisch.
func (s *Service) Listing(ctx context.Context, host string) (Listing, error) {
	host = path.Clean("/" + host)
	if l := s.viaLink(host); l != "" {
		return Listing{}, &PathError{l, ErrViaLink}
	}
	fi, err := os.Lstat(s.real(host))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Listing{}, &PathError{host, ErrGone}
		}
		return Listing{}, &PathError{host, err}
	}
	if !fi.IsDir() {
		return Listing{}, &PathError{host, ErrNotDir}
	}
	l := Listing{Path: host, Rows: []Row{}, Measure: s.Measuring(), Trash: s.Trash != nil}
	s.refreshDir(ctx, host, fi)
	// Der Baum ändert sich unter s.mu (Abziehen, Abgleich); gelesen wird
	// eine Kopie.
	s.mu.Lock()
	t := s.treeLocked()
	var e *Entry
	if t != nil {
		e = t.Find(host)
	}
	fromTree := e != nil && e.Dir && !e.Flat && e.Size >= 1<<20
	if fromTree {
		e = copyEntry(e)
	}
	s.mu.Unlock()
	switch {
	// Kleine Ordner (auch solche, die nach dem Löschen klein sind) misst
	// die Liste frisch; das geht schnell und zeigt Neues.
	case fromTree:
		at := t.At
		l.At, l.Took, l.Partial = &at, t.Took, t.Partial
		sortEntries(e.Kids) // nach dem Abziehen kann die Reihenfolge verrutscht sein
	case t == nil && host == "/":
		l.NoTree = true
		e = &Entry{Name: "/", Dir: true}
	default:
		lt := Measure(ctx, host, Options{Root: s.root(), Mounts: s.mounts(), Full: true, Keep: liveKeep, Limit: liveLimit, Deadline: s.now().Add(liveTime)})
		e = lt.Root
		l.Live, l.Partial = true, lt.Partial
		if t == nil {
			l.NoTree = true
		}
		if mounted(host, s.mounts()) {
			e.Mount, e.Kids, e.RestN, e.RestS = true, nil, 0, 0
		}
	}
	g := s.guardFor(ctx, false)
	l.Problem = g.Incomplete()
	l.Self = s.row(g, host, e)
	l.Self.Name = path.Base(host)
	for _, k := range e.Kids {
		l.Rows = append(l.Rows, s.row(g, path.Join(host, k.Name), k))
	}
	l.RestN, l.RestS = e.RestN, e.RestS
	if host == "/" {
		if s.Disk != nil {
			if total, free, err := s.Disk(s.root()); err == nil {
				l.Disk = &DiskInfo{Total: total, Free: free}
			}
		}
		l.Hidden = hidden(s.openFiles())
	}
	return l, nil
}

// refreshMax: Ordner mit mehr Einträgen gleicht HostDeck nicht ab.
const refreshMax = 20_000

// refreshLimit: höchstens so viele Einträge misst der Abgleich je
// geändertem Eintrag nach (eine Variable für Tests).
var refreshLimit int64 = liveLimit

// refreshSlack: so viel gröber als die Uhr dürfen Zeitstempel sein.
const refreshSlack = 2 * time.Second

// refreshDir gleicht den gemerkten Ordner host mit der Platte ab, wenn sich
// darin seit der Messung (oder dem letzten Abgleich) etwas geändert hat:
// Neue und geänderte Einträge (mtime oder ctime neuer) misst es frisch und
// begrenzt, verschwundene nimmt es heraus, und die Ordner darüber führt es
// nach. Änderungen tiefer unten kommen dazu, sobald man den Ordner dort
// öffnet, oder mit der nächsten Messung. fi ist host auf der Platte.
func (s *Service) refreshDir(ctx context.Context, host string, fi fs.FileInfo) {
	s.mu.Lock()
	t := s.treeLocked()
	var e *Entry
	if t != nil {
		e = t.Find(host)
	}
	if e == nil || !e.Dir || e.Flat {
		s.mu.Unlock()
		return
	}
	since := t.At
	if r, ok := s.refreshed[host]; ok && r.After(since) {
		since = r
	}
	// Zeitstempel im Dateisystem sind gröber als die Uhr (ein Tick des
	// Kernels); was kurz vor since geändert wurde, zählt mit.
	since = since.Add(-refreshSlack)
	if !changedAt(fi).After(since) {
		s.mu.Unlock()
		return
	}
	known := map[string]bool{}
	for _, k := range e.Kids {
		known[k.Name] = true
	}
	s.mu.Unlock()

	start := s.now()
	ents, err := os.ReadDir(s.real(host))
	if err != nil || len(ents) > refreshMax {
		return
	}
	mounts := s.mounts()
	type change struct {
		name string
		e    *Entry // nil: verschwunden
	}
	var changes []change
	present := map[string]bool{}
	deadline := s.now().Add(liveTime)
	for _, de := range ents {
		name := de.Name()
		present[name] = true
		info, err := de.Info()
		if err != nil || !changedAt(info).After(since) {
			continue
		}
		p := path.Join(host, name)
		if mounted(p, mounts) {
			continue
		}
		if ctx.Err() != nil || s.now().After(deadline) {
			return // halb abgeglichen hilft nicht; beim nächsten Öffnen wieder
		}
		mt := Measure(ctx, p, Options{Root: s.root(), Mounts: mounts, Limit: refreshLimit, Deadline: deadline})
		if ctx.Err() != nil || s.now().After(deadline) {
			return
		}
		if mt.Root == nil || mt.Partial {
			// Zu viele Einträge, um sie hier nachzumessen: Der Eintrag
			// behält seinen alten Wert, die übrigen gleicht HostDeck trotzdem ab.
			continue
		}
		mt.Root.Name = name
		changes = append(changes, change{name, mt.Root})
	}
	for name := range known {
		if !present[name] {
			changes = append(changes, change{name: name})
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tree != t {
		return // inzwischen neu gemessen
	}
	for _, c := range changes {
		t.Replace(host, c.name, c.e)
	}
	if s.refreshed == nil {
		s.refreshed = map[string]time.Time{}
	}
	s.refreshed[host] = start
	if len(changes) > 0 && s.File != "" {
		_ = t.Save(s.File)
	}
}

// Changed gleicht nach dem Zurückholen von host den Ordner darüber mit der
// Platte ab, damit die Liste es gleich zeigt.
func (s *Service) Changed(ctx context.Context, host string) {
	if root := strings.TrimSuffix(s.root(), "/"); root != "" {
		if !strings.HasPrefix(host, root+"/") {
			return
		}
		host = strings.TrimPrefix(host, root)
	}
	dir := path.Dir(path.Clean("/" + host))
	fi, err := os.Lstat(s.real(dir))
	if err != nil || !fi.IsDir() {
		return
	}
	s.refreshDir(ctx, dir, fi)
}

// mountWhy sagt, warum HostDeck sel wegen eines Einhängepunkts nicht löscht.
func mountWhy(sel Sel) string {
	m := sel.Mounts[0]
	if under(m, sel.Path) && m != sel.Path {
		return "darin hängt " + m + " (eine andere Platte oder ein eingebundener Ordner); HostDeck löscht nichts, worin etwas eingehängt ist"
	}
	return "liegt auf einer anderen Platte oder ist selbst eingehängt; HostDeck löscht dort nichts"
}

func mounted(host string, mounts []string) bool {
	for _, m := range mounts {
		if host != "/" && cleanAbs(m) == host {
			return true
		}
	}
	return false
}

// copyEntry kopiert e und seine Einträge (eine Ebene tief).
func copyEntry(e *Entry) *Entry {
	c := *e
	c.Kids = make([]*Entry, len(e.Kids))
	for i, k := range e.Kids {
		kc := *k
		kc.Kids = nil
		c.Kids[i] = &kc
	}
	return &c
}

func (s *Service) row(g *Guard, host string, e *Entry) Row {
	return Row{Name: e.Name, Path: host, Size: e.Size, Dir: e.Dir, Link: e.Link, Files: e.Files, Newest: e.Newest, Mount: e.Mount, Denied: e.Denied, Guard: g.Check(host)}
}

// hidden fasst gelöschte, aber noch offene Dateien je Prozess zusammen.
func hidden(of []OpenFile) []Hidden {
	type key struct {
		dev  uint64
		path string
	}
	seen := map[key]bool{}
	by := map[int]*Hidden{}
	for _, f := range of {
		if !f.Deleted || f.Size <= 0 {
			continue
		}
		k := key{f.Dev, f.Path}
		if seen[k] {
			continue
		}
		seen[k] = true
		h := by[f.PID]
		if h == nil {
			h = &Hidden{Proc: f.Proc, PID: f.PID, Path: f.Path}
			by[f.PID] = h
		}
		h.Size += f.Size
		h.Files++
	}
	var out []Hidden
	for _, h := range by {
		if h.Size >= 1<<20 {
			out = append(out, *h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

// PlanItem ist ein Eintrag der Vorschau.
type PlanItem struct {
	Path  string   `json:"pfad"`
	Dir   bool     `json:"ordner,omitempty"`
	Link  bool     `json:"verknuepfung,omitempty"`
	Size  int64    `json:"groesse"`
	Frees int64    `json:"frei"`
	Files int64    `json:"dateien"`
	Kind  string   `json:"art,omitempty"`
	Warn  []string `json:"warnungen,omitempty"`
}

// Blocked ist etwas Ausgewähltes, das sich nicht löschen lässt.
type Blocked struct {
	Path string `json:"pfad"`
	Why  string `json:"grund"`
	Page string `json:"seite,omitempty"`
}

// Plan ist die Vorschau eines Löschens.
type Plan struct {
	Items      []PlanItem `json:"eintraege"`
	Blocked    []Blocked  `json:"geht_nicht,omitempty"`
	Size       int64      `json:"groesse"`
	Frees      int64      `json:"frei_wird"`
	Files      int64      `json:"dateien"`
	Free       int64      `json:"frei_jetzt"`
	TrashOK    bool       `json:"papierkorb_moeglich"`
	TrashWhy   string     `json:"papierkorb_grund,omitempty"`
	TrashNeed  int64      `json:"papierkorb_noetig,omitempty"`
	TrashUntil string     `json:"papierkorb_bis,omitempty"`
	Notes      []string   `json:"hinweise,omitempty"`
	Problem    string     `json:"problem,omitempty"`
	Stand      string     `json:"stand,omitempty"`
}

type plan struct {
	sels []Sel
	at   time.Time
}

// Plan misst die Auswahl paths frisch und zeigt, was ein Löschen täte.
// Löschen geht danach nur mit dem Stand dieser Vorschau.
func (s *Service) Plan(ctx context.Context, paths []string) (Plan, error) {
	if len(paths) == 0 {
		return Plan{}, ErrNothing
	}
	if len(paths) > maxPick {
		return Plan{}, ErrTooMany
	}
	var p Plan
	g := s.guardFor(ctx, true)
	if why := g.Incomplete(); why != "" {
		return Plan{}, fmt.Errorf("%s; %w", why, ErrIncomplete)
	}
	var free []string
	seen := map[string]bool{}
	for _, h := range paths {
		h = path.Clean("/" + h)
		if seen[h] {
			continue
		}
		seen[h] = true
		v := g.Check(h)
		if v.Level != Free {
			p.Blocked = append(p.Blocked, Blocked{Path: h, Why: v.Reason, Page: v.Page})
			continue
		}
		if l := s.viaLink(h); l != "" {
			p.Blocked = append(p.Blocked, Blocked{Path: h, Why: "liegt hinter der Verknüpfung " + l + "; HostDeck folgt ihr nicht"})
			continue
		}
		if _, err := os.Lstat(s.real(h)); err != nil {
			why := err.Error()
			if errors.Is(err, fs.ErrNotExist) {
				why = "gibt es nicht mehr"
			}
			p.Blocked = append(p.Blocked, Blocked{Path: h, Why: why})
			continue
		}
		free = append(free, h)
	}
	ok := pickPaths(free, &p)
	mounts := s.mounts()
	sels, err := Inspect(ctx, s.root(), ok, mounts, 0)
	if err != nil {
		return Plan{}, err
	}
	open := s.openFiles()
	var keep []Sel
	for _, sel := range sels {
		if len(sel.Mounts) > 0 {
			p.Blocked = append(p.Blocked, Blocked{Path: sel.Path, Why: mountWhy(sel)})
			continue
		}
		v := g.Check(sel.Path)
		it := PlanItem{Path: sel.Path, Dir: sel.Dir, Link: sel.Link, Size: sel.Size, Frees: sel.Frees, Files: sel.Files, Kind: v.Kind, Warn: v.Warn}
		it.Warn = append(it.Warn, openWarnings(sel.Path, open)...)
		if sel.Denied > 0 {
			it.Warn = append(it.Warn, fmt.Sprintf("%d %s darin nicht lesbar; was HostDeck nicht löschen kann, bleibt.", sel.Denied, plural(int(sel.Denied), "Ordner ist", "Ordner sind")))
		}
		if sel.Frees < sel.Size {
			it.Warn = append(it.Warn, units.HumanSize(sel.Size-sel.Frees)+" bleiben belegt: Diese Dateien haben weitere Namen (Hardlinks) außerhalb der Auswahl.")
		}
		if sel.Partial {
			p.Problem = "Zu viele Einträge, um sie vorher vollständig zu messen. Wähle kleinere Ordner."
		}
		p.Items = append(p.Items, it)
		p.Size += sel.Size
		p.Frees += sel.Frees
		p.Files += sel.Files
		keep = append(keep, sel)
	}
	if s.Disk != nil {
		if _, free, err := s.Disk(s.root()); err == nil {
			p.Free = free
		}
	}
	s.trashCheck(&p, keep)
	if len(p.Items) == 0 {
		if p.Problem == "" {
			p.Problem = "Nichts davon lässt sich löschen."
		}
		return p, nil
	}
	if p.Problem != "" {
		return p, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return Plan{}, err
	}
	p.Stand = hex.EncodeToString(b)
	s.mu.Lock()
	if s.plans == nil {
		s.plans = map[string]*plan{}
	}
	now := s.now()
	for k, v := range s.plans {
		if now.Sub(v.at) > planTTL {
			delete(s.plans, k)
		}
	}
	for len(s.plans) >= planRemain {
		var oldest string
		for k, v := range s.plans {
			if oldest == "" || v.at.Before(s.plans[oldest].at) {
				oldest = k
			}
		}
		delete(s.plans, oldest)
	}
	s.plans[p.Stand] = &plan{sels: keep, at: now}
	s.mu.Unlock()
	return p, nil
}

// pickPaths lässt weg, was schon in einem anderen ausgewählten Ordner
// liegt.
func pickPaths(paths []string, p *Plan) []string {
	clean := append([]string(nil), paths...)
	sort.SliceStable(clean, func(i, j int) bool { return len(clean[i]) < len(clean[j]) })
	var out []string
	for _, h := range clean {
		inside := false
		for _, o := range out {
			if under(h, o) {
				inside = true
				p.Notes = append(p.Notes, h+" liegt in "+o+" und kommt damit mit.")
				break
			}
		}
		if !inside {
			out = append(out, h)
		}
	}
	return out
}

func openWarnings(host string, of []OpenFile) []string {
	var hits []string
	seen := map[string]bool{}
	for _, f := range of {
		if f.Deleted || seen[f.Path] || !under(f.Path, host) {
			continue
		}
		seen[f.Path] = true
		hits = append(hits, f.Path+" (Prozess "+f.Proc+")")
	}
	if len(hits) == 0 {
		return nil
	}
	sort.Strings(hits)
	more := ""
	if len(hits) > maxWarn {
		more = fmt.Sprintf(" und %d weitere", len(hits)-maxWarn)
		hits = hits[:maxWarn]
	}
	return []string{"Gerade offen: " + strings.Join(hits, ", ") + more + ". Der Platz wird erst frei, wenn das Programm die Datei schließt oder neu startet."}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// trashCheck sagt, ob der Papierkorb für die Vorschau in Frage kommt: Er
// liegt auf derselben Platte und braucht Platz plus Reserve.
func (s *Service) trashCheck(p *Plan, sels []Sel) {
	if s.Trash == nil {
		p.TrashWhy = "Einen Papierkorb gibt es hier nicht."
		return
	}
	p.TrashNeed = p.Size + s.Trash.ReserveBytes()
	p.TrashUntil = s.Trash.KeepUntil().Format("02.01.2006")
	dir := s.Trash.Dir
	if dir == "" {
		dir = s.root()
	}
	if why := s.otherDisk(dir, sels); why != "" {
		p.TrashWhy = why
		return
	}
	if s.Disk == nil {
		p.TrashOK = true
		return
	}
	_, free, err := s.Disk(nearestDir(dir))
	if err != nil {
		p.TrashWhy = "Freier Platz für den Papierkorb nicht messbar: " + err.Error()
		return
	}
	if free < p.TrashNeed {
		p.TrashWhy = fmt.Sprintf("Für den Papierkorb reicht der Platz nicht: frei %s, nötig %s (die Auswahl plus %s Reserve).", units.HumanSize(free), units.HumanSize(p.TrashNeed), units.HumanSize(s.Trash.ReserveBytes()))
		return
	}
	p.TrashOK = true
}

// otherDisk sagt, warum der Papierkorb in dir nicht geht, wenn er auf
// einer anderen Platte liegt als eine Auswahl; leer: dieselbe. Zurückholen
// legt die Einträge mit einem Umbenennen zurück, und das geht nur auf
// derselben Platte.
func (s *Service) otherDisk(dir string, sels []Sel) string {
	fi, err := os.Stat(nearestDir(dir))
	if err != nil {
		return "Der Ordner des Papierkorbs ist nicht lesbar: " + err.Error()
	}
	_, dev, _, _ := statOf(fi)
	for _, sel := range sels {
		if dev != sel.Ident.Dev {
			return "Der Papierkorb liegt auf einer anderen Platte als " + sel.Path + "; von dort ginge Zurückholen nicht."
		}
	}
	return ""
}

// nearestDir ist dir oder der nächste Ordner darüber, den es gibt.
func nearestDir(dir string) string {
	for dir != "/" && dir != "." {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
		dir = path.Dir(dir)
	}
	return dir
}

// Pending nennt die Pfade der Vorschau stand; nil: gibt es nicht.
func (s *Service) Pending(stand string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	pl := s.plans[stand]
	if pl == nil {
		return nil
	}
	out := make([]string, len(pl.sels))
	for i, sel := range pl.sels {
		out[i] = sel.Path
	}
	return out
}

// Run löscht, was die Vorschau stand gezeigt hat: mit trash erst in den
// Papierkorb, sonst endgültig. Es meldet das Ergebnis in Sätzen.
func (s *Service) Run(ctx context.Context, stand string, trash bool, ev func(aufraeumen.Event)) (string, error) {
	if ev == nil {
		ev = func(aufraeumen.Event) {}
	}
	s.mu.Lock()
	pl := s.plans[stand]
	delete(s.plans, stand)
	s.mu.Unlock()
	ev(aufraeumen.Event{Step: StepCheck, State: aufraeumen.StateRunning})
	fail := func(step string, err error) (string, error) {
		ev(aufraeumen.Event{Step: step, State: aufraeumen.StateFailed, Detail: err.Error()})
		return "", err
	}
	if pl == nil || s.now().Sub(pl.at) > planTTL {
		return fail(StepCheck, ErrExpired)
	}
	if trash && s.Trash == nil {
		return fail(StepCheck, errors.New("einen Papierkorb gibt es hier nicht"))
	}
	g := s.guardFor(ctx, true)
	if why := g.Incomplete(); why != "" {
		return fail(StepCheck, fmt.Errorf("%s; nichts ist gelöscht; %w", why, ErrIncomplete))
	}
	hosts := make([]string, len(pl.sels))
	for i, sel := range pl.sels {
		if v := g.Check(sel.Path); v.Level != Free {
			return fail(StepCheck, fmt.Errorf("%s %s; nichts ist gelöscht", sel.Path, v.Reason))
		}
		hosts[i] = sel.Path
	}
	now, err := Inspect(ctx, s.root(), hosts, s.mounts(), 0)
	if err != nil {
		return fail(StepCheck, fmt.Errorf("%w; nichts ist gelöscht", err))
	}
	for i, n := range now {
		old := pl.sels[i]
		switch {
		case n.Ident != old.Ident:
			return fail(StepCheck, fmt.Errorf("%s %w; nichts ist gelöscht", n.Path, ErrChanged))
		case len(n.Mounts) > 0:
			return fail(StepCheck, fmt.Errorf("%s: %s; nichts ist gelöscht", n.Path, mountWhy(n)))
		case n.Partial:
			return fail(StepCheck, errors.New("zu viele Einträge zum Nachmessen; nichts ist gelöscht"))
		}
		if grown := n.Size - old.Size; grown > growAbs || (grown > growMin && grown > old.Size/10) {
			return fail(StepCheck, fmt.Errorf("%s ist seit der Vorschau um %s gewachsen; bitte die Vorschau neu öffnen. Nichts ist gelöscht", n.Path, units.HumanSize(grown)))
		}
	}
	if trash {
		dir := s.Trash.Dir
		if dir == "" {
			dir = s.root()
		}
		if why := s.otherDisk(dir, now); why != "" {
			return fail(StepCheck, errors.New(why+" Nichts ist gelöscht."))
		}
	}
	ev(aufraeumen.Event{Step: StepCheck, State: aufraeumen.StateOK})
	before := s.free()

	var gone []Sel
	var notes []string
	partial := 0
	// removeOne löscht sel; partial: ein Teil blieb stehen (steht in notes).
	removeOne := func(ctx context.Context, sel Sel) (bool, error) {
		res, err := Remove(ctx, s.root(), sel)
		if err != nil && res.Files == 0 {
			return false, err
		}
		if res.Left > 0 || err != nil {
			partial++
			msg := fmt.Sprintf("%s ist nur teilweise gelöscht: %d %s bleiben", sel.Path, res.Left, plural(res.Left, "Eintrag", "Einträge"))
			if len(res.Errors) > 0 {
				msg += " (" + res.Errors[0] + ")"
			}
			notes = append(notes, msg+". Neu messen zeigt, was übrig ist.")
			return true, nil
		}
		gone = append(gone, sel)
		return false, nil
	}
	var saved int
	if trash {
		items := make([]aufraeumen.PathItem, len(now))
		for i := range now {
			sel := now[i]
			items[i] = aufraeumen.PathItem{Path: s.real(sel.Path), Size: sel.Size, Remove: func(ctx context.Context) (bool, error) { return removeOne(ctx, sel) }}
		}
		n, more, err := s.Trash.TrashPaths(ctx, items, ev)
		if err != nil {
			return "", err
		}
		saved = n
		notes = append(notes, more...)
	} else {
		ev(aufraeumen.Event{Step: StepDelete, State: aufraeumen.StateRunning, Detail: fmt.Sprintf("%d %s", len(now), plural(len(now), "Eintrag", "Einträge"))})
		for _, sel := range now {
			if ctx.Err() != nil {
				break
			}
			if _, err := removeOne(ctx, sel); err != nil {
				notes = append(notes, sel.Path+" bleibt: "+err.Error()+".")
			}
		}
		ev(aufraeumen.Event{Step: StepDelete, State: aufraeumen.StateOK, Detail: fmt.Sprintf("%d gelöscht", len(gone))})
	}

	// Den gemerkten Baum nachführen und die Auswahl austragen.
	var gonePaths []string
	s.mu.Lock()
	t := s.treeLocked()
	for _, sel := range gone {
		gonePaths = append(gonePaths, sel.Path)
		if t != nil {
			if e := t.Find(sel.Path); e != nil {
				t.Subtract(sel.Path, e.Size, max(e.Files, boolInt(!e.Dir && e.Link == "")))
			}
		}
		if s.meas != nil && s.meas.Running {
			s.deleted = append(s.deleted, sel.Path)
		}
	}
	if t != nil && len(gone) > 0 && s.File != "" {
		_ = t.Save(s.File)
	}
	s.mu.Unlock()
	if s.Unselect != nil && len(gonePaths) > 0 {
		n, err := s.Unselect(gonePaths)
		switch {
		case err != nil:
			notes = append(notes, "Deine Auswahl ist nicht angepasst ("+err.Error()+"); vor dem nächsten Backup unter Auswahl die nicht mehr gefundenen Einträge entfernen.")
		case n > 0:
			notes = append(notes, fmt.Sprintf("%d %s aus deiner Auswahl ausgetragen.", n, plural(n, "Eintrag", "Einträge")))
		}
	}
	if len(gone) == 0 && partial == 0 {
		return "", fmt.Errorf("nichts gelöscht: %s", strings.Join(notes, " "))
	}
	after := s.free()
	head := fmt.Sprintf("Gelöscht: %d %s", len(gone), plural(len(gone), "Eintrag", "Einträge"))
	if partial > 0 {
		head = fmt.Sprintf("Ganz gelöscht: %d, teilweise gelöscht: %d", len(gone), partial)
	}
	msg := head + "."
	if before >= 0 && after >= 0 {
		msg = fmt.Sprintf("%s, %s frei geworden (vorher %s frei, jetzt %s).", head, units.HumanSize(max(after-before, 0)), units.HumanSize(before), units.HumanSize(after))
	}
	if trash && saved > 0 {
		msg += fmt.Sprintf(" Im Papierkorb bis %s; zurückholen geht unter Speicherplatz, Papierkorb.", s.Trash.KeepUntil().Format("02.01.2006"))
	}
	if len(notes) > 0 {
		msg += " " + strings.Join(notes, " ")
	}
	if ctx.Err() != nil {
		return msg, ctx.Err()
	}
	return msg, nil
}

func (s *Service) free() int64 {
	if s.Disk == nil {
		return -1
	}
	_, f, err := s.Disk(s.root())
	if err != nil {
		return -1
	}
	return f
}
