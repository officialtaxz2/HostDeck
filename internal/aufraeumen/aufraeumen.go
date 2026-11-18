// Package aufraeumen schlägt vor, was auf dem Server ohne Datenverlust
// weg kann, und räumt nach deiner Auswahl auf.
//
// Was neu entsteht, löscht es direkt: den Build-Cache und Images, die kein
// Container braucht. Was Daten enthält (Volumes ohne Container, alte
// Kopien des Caddyfiles), sichert es vorher in den Papierkorb, ein eigenes
// restic-Repository auf dem Server, und löscht erst, wenn die Sicherung
// geprüft ist. Aus dem Papierkorb lässt sich alles zurückholen, bis er es
// nach Keep (30 Tage) vergisst.
//
// Nie angefasst werden: Images und Volumes, die ein Container braucht, auch
// ein gestoppter; das Rückfall-Image beim Aktualisieren; Volumes mit
// besonderen Einstellungen (etwa ein Netzlaufwerk).
package aufraeumen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

// Arten von Vorschlägen.
const (
	KindCache  = "cache"
	KindImage  = "image"
	KindVolume = "volume"
	KindFile   = "datei"
	// KindPath ist ein Ordner oder eine Datei, die unter Speicherplatz
	// gelöscht wurde.
	KindPath = "pfad"
)

// Fehler.
var (
	ErrChanged  = errors.New("seit der Vorschau hat sich etwas geändert; bitte die Vorschau neu öffnen")
	ErrNothing  = errors.New("nichts ausgewählt")
	ErrNotFound = errors.New("diesen Eintrag gibt es nicht (mehr)")
	ErrNoSpace  = errors.New("zu wenig freier Platz für den Papierkorb")
	ErrBlocked  = errors.New("das geht gerade nicht")
)

// Docker ist, was das Aufräumen von Docker braucht (dockerapi.Cleaner).
type Docker interface {
	Containers(ctx context.Context) ([]dockerapi.OpsContainer, error)
	Images(ctx context.Context) ([]dockerapi.Image, error)
	Volumes(ctx context.Context) ([]dockerapi.CleanVolume, error)
	DiskUsage(ctx context.Context) (dockerapi.DiskUse, error)
	RemoveImage(ctx context.Context, ref string) error
	RemoveVolume(ctx context.Context, name string) error
	PruneBuildCache(ctx context.Context) (int64, error)
	CreateVolume(ctx context.Context, name string, labels map[string]string) (string, error)
}

// Trash ist der Papierkorb: ein restic-Repository auf dem Server.
type Trash interface {
	// Save sichert paths und liefert die Snapshot-ID und Dateien, die
	// nicht lesbar waren.
	Save(ctx context.Context, paths, tags []string) (id string, unreadable []string, err error)
	// Check prüft das Repository samt aller Daten.
	Check(ctx context.Context) error
	// Restore spielt sub aus dem Snapshot nach target zurück.
	Restore(ctx context.Context, id, sub, target string) error
	Forget(ctx context.Context, ids []string) error
	Prune(ctx context.Context) error
	// List liefert alle Snapshots im Papierkorb.
	List(ctx context.Context) ([]Snap, error)
}

// Snap ist ein Snapshot im Papierkorb.
type Snap struct {
	ID   string
	Time time.Time
}

// Service räumt auf.
type Service struct {
	Docker Docker
	Trash  Trash
	// Caddyfile liefert das Caddyfile; nil oder Fehler: keine Kopien.
	Caddyfile func(ctx context.Context) (string, error)
	// Disk misst Größe und freien Platz des Dateisystems von path.
	Disk func(path string) (total, free int64, err error)
	// RootReserve misst, was das Dateisystem von path für root
	// zurückhält (nicht in Total); nil: unbekannt.
	RootReserve func(path string) int64
	// DiskPath ist der Ort, dessen Platte die Seite zeigt (Standard /).
	DiskPath string
	// Dir ist der Ordner des Papierkorbs; dort liegt auch seine Liste.
	Dir string
	// ProjectDir liefert den Ordner eines Compose-Projekts, wenn es ihn
	// noch gibt; nil: unbekannt.
	ProjectDir func(name string) string
	Keep       time.Duration // so lange bleibt etwas im Papierkorb (Standard 30 Tage)
	Reserve    int64         // so viel bleibt neben dem Papierkorb frei (Standard 2 GB)
	MaxAge     time.Duration // so lange gilt eine Übersicht (Standard 1 Minute)
	Now        func() time.Time
	// Pullable sagt, ob eine Registry das Image ref herausgibt: nil heißt
	// ja, ErrNotPullable heißt nein (unbekannt oder nur mit Anmeldung),
	// andere Fehler heißen „gerade nicht zu prüfen“. Ist Pullable nil, gilt
	// ein Image mit RepoDigests als neu ladbar; das ist seit dem
	// containerd-Speicher von Docker unzuverlässig (dort tragen auch hier
	// gebaute Images RepoDigests), darum setzt der Dienst Pullable immer.
	Pullable func(ctx context.Context, ref string) error
	// Unselect trägt gelöschte Volumes und Dateien aus deiner Auswahl aus
	// (Schlüssel wie volume:<name> oder path:<pfad>) und gibt zurück, wie
	// viele dort standen. Sonst scheitert die Vorprüfung des nächsten
	// Backups an ihnen. Holst du etwas zurück, fragt die Auswahl wieder.
	Unselect func(keys []string) (int, error)
	// RestoreCheck prüft vor dem Zurückholen eines Ordners oder einer
	// Datei von Speicherplatz (Art pfad), ob der alte Ort noch frei ist;
	// nil: keine Prüfung.
	RestoreCheck func(ctx context.Context, path string) error
	// Restored meldet einen Ordner oder eine Datei (Art pfad), die wieder
	// an ihrem alten Ort liegt; nil: niemand will es wissen.
	Restored func(ctx context.Context, path string)

	mu    sync.Mutex // schützt cache, pulled und die Liste des Papierkorbs
	calc  sync.Mutex // eine Rechnung zur Zeit
	cache *state
	// pulled merkt Antworten der Registry je Referenz (eine Stunde).
	pulled map[string]pullAnswer
}

// ErrNotPullable: Keine Registry gibt das Image heraus (unbekannt oder nur
// mit Anmeldung).
var ErrNotPullable = errors.New("keine Registry gibt es heraus")

type pullAnswer struct {
	at  time.Time
	err error
}

const pullKeep = time.Hour

// Standardwerte.
const (
	DefaultKeep    = 30 * 24 * time.Hour
	DefaultReserve = 2 << 30
	entriesFile    = "inhalt.json"
	restoredFile   = "zurueckgeholt.json"
	trashTag       = "sbm-papierkorb"
	minCache       = 1 << 20 // kleiner lohnt kein Vorschlag
)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) keep() time.Duration {
	if s.Keep > 0 {
		return s.Keep
	}
	return DefaultKeep
}

func (s *Service) reserve() int64 {
	if s.Reserve > 0 {
		return s.Reserve
	}
	return DefaultReserve
}

func (s *Service) diskPath() string {
	if s.DiskPath != "" {
		return s.DiskPath
	}
	return "/"
}

// Disk ist der Platz auf dem Server.
type Disk struct {
	Total      int64 `json:"gesamt"`
	Free       int64 `json:"frei"`
	Reserve    int64 `json:"reserve,omitempty"` // für root zurückgehalten, nicht in Total
	Images     int64 `json:"images"`
	BuildCache int64 `json:"build_cache"`
	Volumes    int64 `json:"volumes"`
	Containers int64 `json:"container"`
	Trash      int64 `json:"papierkorb"`
}

// Item ist ein Vorschlag.
type Item struct {
	Key    string `json:"schluessel"`
	Kind   string `json:"art"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Size   int64  `json:"groesse"`    // Byte; -1 unbekannt
	Trash  bool   `json:"papierkorb"` // kommt vorher in den Papierkorb
	Pick   bool   `json:"vorgeschlagen"`
	Warn   string `json:"hinweis,omitempty"`

	id    string // was sich nicht ändern darf, ohne dass die Vorschau neu muss
	pull  bool   // Image mit Namen: bei der Registry nachfragen
	refs  []string
	path  string
	label map[string]string
	// remove löscht einen Eintrag der Art KindPath; partial: ein Teil blieb.
	remove func(ctx context.Context) (partial bool, err error)
}

// Kept ist etwas, das aufzuräumen wäre, das HostDeck aber nicht anfasst.
type Kept struct {
	Name string `json:"name"`
	Why  string `json:"warum"`
}

// Entry ist ein Eintrag im Papierkorb.
type Entry struct {
	ID      string            `json:"id"` // Snapshot
	Kind    string            `json:"art"`
	Name    string            `json:"name"`
	Path    string            `json:"pfad"`
	Size    int64             `json:"groesse"`
	Deleted time.Time         `json:"geloescht"`
	Until   time.Time         `json:"bis"`
	Labels  map[string]string `json:"labels,omitempty"`
	Mode    uint32            `json:"modus,omitempty"`
	UID     int               `json:"uid,omitempty"`
	GID     int               `json:"gid,omitempty"`
	// Partial: Beim Löschen blieb ein Teil stehen (nur Art pfad);
	// Zurückholen legt dann nur das Fehlende hin.
	Partial bool `json:"teilweise,omitempty"`
}

// Overview ist der Stand für die Seite Aufräumen.
type Overview struct {
	Disk    Disk    `json:"platte"`
	Items   []Item  `json:"vorschlaege"`
	Kept    []Kept  `json:"bleibt"`
	Trash   []Entry `json:"papierkorb"`
	Days    int     `json:"tage"`
	Problem string  `json:"problem,omitempty"`
	// NoDocker: Auf dem Server gibt es kein Docker, also nichts aufzuräumen.
	NoDocker bool      `json:"ohne_docker,omitempty"`
	At       time.Time `json:"stand_von"`
}

type state struct {
	ov    Overview
	items map[string]Item
	at    time.Time
}

// Overview liefert den Stand; ist der letzte jünger als MaxAge, diesen.
func (s *Service) Overview(ctx context.Context) Overview {
	st := s.state(ctx, false)
	ov := st.ov
	ov.Trash = s.entries()
	ov.Disk.Trash = 0
	for _, e := range ov.Trash {
		ov.Disk.Trash += e.Size
	}
	return ov
}

// Refresh vergisst den letzten Stand.
func (s *Service) Refresh() {
	s.mu.Lock()
	s.cache = nil
	s.mu.Unlock()
}

func (s *Service) state(ctx context.Context, fresh bool) *state {
	maxAge := s.MaxAge
	if maxAge <= 0 {
		maxAge = time.Minute
	}
	s.calc.Lock()
	defer s.calc.Unlock()
	s.mu.Lock()
	c := s.cache
	s.mu.Unlock()
	if c != nil && !fresh && s.now().Sub(c.at) < maxAge {
		return c
	}
	c = s.compute(ctx)
	if c.ov.Problem == "" {
		s.mu.Lock()
		s.cache = c
		s.mu.Unlock()
	}
	return c
}

func (s *Service) compute(ctx context.Context) *state {
	now := s.now()
	st := &state{at: now, items: map[string]Item{}}
	ov := &st.ov
	ov.At = now
	ov.Days = int(s.keep() / (24 * time.Hour))
	ov.Items, ov.Kept = []Item{}, []Kept{}
	if s.Disk != nil {
		ov.Disk.Total, ov.Disk.Free, _ = s.Disk(s.diskPath())
	}
	if s.RootReserve != nil {
		ov.Disk.Reserve = s.RootReserve(s.diskPath())
	}
	conts, err := s.Docker.Containers(ctx)
	if dockerapi.Missing(err) {
		ov.NoDocker = true
		return st
	}
	if err != nil {
		ov.Problem = "Docker antwortet nicht: " + err.Error()
		return st
	}
	du, err := s.Docker.DiskUsage(ctx)
	if err != nil {
		ov.Problem = "Docker kann den belegten Platz gerade nicht messen: " + err.Error()
		return st
	}
	ov.Disk.Images, ov.Disk.BuildCache, ov.Disk.Volumes, ov.Disk.Containers = du.Images, du.BuildCache, du.Volumes, du.Containers

	if du.CacheFree >= minCache {
		ov.Items = append(ov.Items, Item{
			Key: KindCache, Kind: KindCache, Name: "Build-Cache leeren", Size: du.CacheFree, Pick: true, id: KindCache,
			Detail: "Zwischenstände vom Bauen deiner Images. Docker legt sie beim nächsten Bauen neu an; das dauert dann etwas länger.",
		})
	}
	imgs, err := s.Docker.Images(ctx)
	if err != nil {
		ov.Problem = "Docker liefert die Images nicht: " + err.Error()
		return st
	}
	it, kept := unusedImages(imgs, conts, du.ImageSizes)
	s.checkPull(ctx, it)
	ov.Items = append(ov.Items, it...)
	ov.Kept = append(ov.Kept, kept...)

	vols, err := s.Docker.Volumes(ctx)
	if err != nil {
		ov.Problem = "Docker liefert die Volumes nicht: " + err.Error()
		return st
	}
	it, kept = s.orphanVolumes(vols, conts, du.VolumeSizes)
	ov.Items = append(ov.Items, it...)
	ov.Kept = append(ov.Kept, kept...)

	if s.Caddyfile != nil {
		if cf, err := s.Caddyfile(ctx); err == nil && cf != "" {
			ov.Items = append(ov.Items, caddyCopies(cf)...)
		}
	}
	back := s.restored()
	for k, i := range ov.Items {
		if back[i.Key] {
			ov.Items[k].Pick = false
			ov.Items[k].Warn = "Du hast es aus dem Papierkorb zurückgeholt; HostDeck wählt es nicht mehr von selbst."
		}
		st.items[i.Key] = ov.Items[k]
	}
	return st
}

// restored sind die Schlüssel von Einträgen, die du aus dem Papierkorb
// zurückgeholt hast.
func (s *Service) restored() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(s.Dir, restoredFile))
	if err != nil {
		return out
	}
	var keys []string
	_ = json.Unmarshal(b, &keys)
	for _, k := range keys {
		out[k] = true
	}
	return out
}

func (s *Service) markRestored(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.restored()
	if m[key] {
		return nil
	}
	keys := []string{key}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b, _ := json.Marshal(keys)
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, restoredFile), b, 0o600)
}

// ---------- Images ----------

func noTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if t != "" && t != "<none>:<none>" {
			out = append(out, t)
		}
	}
	return out
}

// withTag ergänzt :latest, wie Docker es bei einem Namen ohne Tag tut.
func withTag(ref string) string {
	if strings.Contains(ref, "@") {
		return ref
	}
	slash := strings.LastIndex(ref, "/")
	if !strings.Contains(ref[slash+1:], ":") {
		return ref + ":latest"
	}
	return ref
}

const (
	registryDetail = "Kein Container braucht es. Es kommt aus einer Registry und lässt sich jederzeit neu laden."
	localWarn      = "Keine Registry gibt es heraus, vermutlich hier gebaut. Nur löschen, wenn du es neu bauen kannst."
)

// checkPull fragt für Images mit Namen bei der Registry nach, ob sie sich
// neu laden lassen. Nur dann bleiben sie vorgewählt.
func (s *Service) checkPull(ctx context.Context, items []Item) {
	if s.Pullable == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := range items {
		if !items[i].pull {
			continue
		}
		wg.Add(1)
		go func(it *Item) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var bad error
			for _, ref := range it.refs {
				if err := s.pullable(ctx, ref); err != nil {
					bad = err
					break
				}
			}
			switch {
			case bad == nil:
			case errors.Is(bad, ErrNotPullable):
				it.Pick, it.Detail, it.Warn = false, "Kein Container braucht es.", localWarn
			default:
				it.Pick, it.Detail = false, "Kein Container braucht es."
				it.Warn = "Ob es sich neu laden lässt, war gerade nicht zu prüfen (" + bad.Error() + "). Darum nicht vorgewählt."
			}
		}(&items[i])
	}
	wg.Wait()
}

func (s *Service) pullable(ctx context.Context, ref string) error {
	s.mu.Lock()
	a, ok := s.pulled[ref]
	s.mu.Unlock()
	if ok && s.now().Sub(a.at) < pullKeep {
		return a.err
	}
	err := s.Pullable(ctx, ref)
	if err == nil || errors.Is(err, ErrNotPullable) {
		s.mu.Lock()
		if s.pulled == nil {
			s.pulled = map[string]pullAnswer{}
		}
		s.pulled[ref] = pullAnswer{s.now(), err}
		s.mu.Unlock()
	}
	return err
}

func unusedImages(imgs []dockerapi.Image, conts []dockerapi.OpsContainer, sizes map[string]int64) ([]Item, []Kept) {
	usedID := map[string]bool{}
	usedRef := map[string]bool{}
	for _, c := range conts {
		usedID[c.ImageID] = true
		usedID[c.Image] = true
		usedRef[withTag(c.Image)] = true
	}
	var items []Item
	var kept []Kept
	for _, im := range imgs {
		tags := noTags(im.RepoTags)
		used := usedID[im.ID]
		for _, t := range tags {
			if usedRef[withTag(t)] {
				used = true
			}
		}
		if used {
			continue
		}
		size := im.Size
		if v, ok := sizes[im.ID]; ok && v > 0 {
			size = v
		}
		short := strings.TrimPrefix(im.ID, "sha256:")
		if len(short) > 12 {
			short = short[:12]
		}
		rueck := false
		for _, t := range tags {
			if dockerapi.IsFallback(t) {
				rueck = true
			}
		}
		if rueck {
			kept = append(kept, Kept{strings.Join(tags, ", "), "Alter Stand eines Projekts von „Aktualisieren“; bleibt, damit „Zurück auf vorher“ geht."})
			continue
		}
		it := Item{Key: KindImage + ":" + im.ID, Kind: KindImage, Size: size, id: im.ID + "|" + strings.Join(tags, ","), refs: tags}
		switch {
		case len(tags) == 0:
			it.Name = "Image ohne Namen (" + short + ")"
			it.Detail = "Übrig geblieben, als ein neuerer Stand den Namen übernommen hat. Kein Container braucht es."
			it.Pick = true
			it.refs = []string{im.ID}
		case panelImage(tags):
			// Spielserver-Panels (Wings, Calagopus) löschen die Container
			// gestoppter Server; das Image braucht der Server beim nächsten
			// Start trotzdem.
			it.Name = strings.Join(tags, ", ")
			it.Detail = "Image für Spielserver im Panel. Gerade nutzt es kein Container; startet ein Spielserver, lädt das Panel es neu, das dauert dann länger."
		case len(im.RepoDigests) > 0:
			it.Name = strings.Join(tags, ", ")
			it.Detail = registryDetail
			it.Pick = true
			it.pull = true
		default:
			it.Name = strings.Join(tags, ", ")
			it.Detail = "Kein Container braucht es."
			it.Warn = "Hier gebaut oder eingespielt: Nur löschen, wenn du es neu bauen kannst."
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].Size > items[b].Size })
	return items, kept
}

// ---------- Volumes ----------

const (
	labelProject = "com.docker.compose.project"
	labelVolume  = "com.docker.compose.volume"
)

var anonymous = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Service) orphanVolumes(vols []dockerapi.CleanVolume, conts []dockerapi.OpsContainer, sizes map[string]int64) ([]Item, []Kept) {
	used := map[string]bool{}
	running := map[string]bool{}
	for _, c := range conts {
		for _, m := range c.Mounts {
			if m.Type == "volume" {
				used[m.Name] = true
			}
		}
		if p := c.Labels[labelProject]; p != "" {
			running[p] = true
		}
	}
	var items []Item
	var kept []Kept
	for _, v := range vols {
		if used[v.Name] {
			continue
		}
		if v.Driver != "local" || len(v.Options) > 0 {
			kept = append(kept, Kept{v.Name, "Volume mit besonderen Einstellungen (etwa ein Netzlaufwerk); HostDeck fasst es nicht an."})
			continue
		}
		if !cleanVolumePath(v.Mountpoint) {
			kept = append(kept, Kept{v.Name, "liegt an einem ungewöhnlichen Ort; HostDeck fasst es nicht an."})
			continue
		}
		size, ok := sizes[v.Name]
		if !ok || size < 0 {
			size = dirSize(v.Mountpoint)
		}
		it := Item{Key: KindVolume + ":" + v.Name, Kind: KindVolume, Name: v.Name, Size: size, Trash: true,
			id: v.Name + "|" + v.Mountpoint + "|" + v.CreatedAt, path: v.Mountpoint, label: v.Labels}
		p := v.Labels[labelProject]
		switch {
		case anonymous.MatchString(v.Name):
			it.Name = "Volume ohne Namen (" + v.Name[:12] + ")"
			it.Detail = "Ein Container hat es nebenbei angelegt; kein Container nutzt es noch."
			it.Pick = true
		case p != "" && running[p]:
			it.Detail = "Aus dem Projekt " + p + ", das läuft, dieses Volume aber nicht nutzt."
			it.Warn = "Vielleicht ein alter Name. Erst prüfen, ob die Daten im laufenden Projekt angekommen sind."
		case p != "" && s.ProjectDir != nil && s.ProjectDir(p) != "":
			it.Detail = "Aus dem Projekt " + p + ", das gerade keine Container hat."
			it.Warn = "Der Ordner des Projekts gibt es noch. Nur wegräumen, wenn du es nicht mehr startest."
		case p != "":
			it.Detail = "Aus dem Projekt " + p + ", das es auf dem Server nicht mehr gibt."
			it.Pick = true
		default:
			it.Detail = "Kein Container nutzt es."
			it.Warn = "Unklar, wozu es gehört. Erst ansehen, dann entscheiden."
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].Size > items[b].Size })
	return items, kept
}

// cleanVolumePath: ein Volume des Treibers local an seinem üblichen Ort.
func cleanVolumePath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && filepath.Base(p) == "_data" && strings.Contains(p, "/volumes/")
}

// dirSize zählt die Größe aller Dateien unter dir, ohne Links zu folgen.
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// ---------- alte Kopien des Caddyfiles ----------

var copySuffix = []string{".bak", ".old", ".orig", ".backup", ".save", ".alt", ".prev", ".dpkg-old", ".dpkg-dist", ".dpkg-bak", ".ucf-old", ".ucf-dist", ".rpmsave", ".rpmnew", "~"}

func isCopyName(name string) bool {
	if name == "Caddyfile" || strings.HasPrefix(name, ".hostdeck-") || strings.HasSuffix(name, ".caddy") {
		return false
	}
	if strings.HasPrefix(name, "Caddyfile") {
		return true
	}
	low := strings.ToLower(name)
	if !strings.Contains(low, "caddy") {
		return false
	}
	for _, s := range copySuffix {
		if strings.HasSuffix(low, s) {
			return true
		}
	}
	return false
}

// imports sammelt die Muster aller import-Zeilen im Caddyfile und in den
// .caddy-Dateien daneben und in sites/, als absolute Pfade.
func imports(cf string) []string {
	dir := filepath.Dir(cf)
	files := []string{cf}
	for _, g := range []string{"*.caddy", "*/*.caddy"} {
		m, _ := filepath.Glob(filepath.Join(dir, g))
		files = append(files, m...)
	}
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, d := range caddy.Directives(string(b)) {
			if len(d.Words) >= 2 && d.Words[0] == "import" {
				p := d.Words[1]
				if !filepath.IsAbs(p) {
					p = filepath.Join(filepath.Dir(f), p)
				}
				out = append(out, p)
			}
		}
	}
	return out
}

func caddyCopies(cf string) []Item {
	dir := filepath.Dir(cf)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	pats := imports(cf)
	var items []Item
	for _, e := range ents {
		if !e.Type().IsRegular() || !isCopyName(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		loaded := false
		for _, pat := range pats {
			if ok, _ := filepath.Match(pat, p); ok || pat == p {
				loaded = true
			}
		}
		if loaded {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, Item{
			Key: KindFile + ":" + p, Kind: KindFile, Name: e.Name(), Size: fi.Size(), Trash: true, Pick: true,
			Detail: "Alte Kopie neben dem Caddyfile vom " + fi.ModTime().Format("02.01.2006") + ". Caddy lädt sie nicht.",
			id:     fmt.Sprintf("%s|%d|%d", p, fi.Size(), fi.ModTime().UnixNano()), path: p,
		})
	}
	return items
}

// ---------- Papierkorb-Liste ----------

func (s *Service) entriesPath() string { return filepath.Join(s.Dir, entriesFile) }

func (s *Service) entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadEntries()
}

func (s *Service) loadEntries() []Entry {
	out := []Entry{}
	b, err := os.ReadFile(s.entriesPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = []Entry{}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Deleted.After(out[b].Deleted) })
	return out
}

func (s *Service) saveEntries(es []Entry) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(es, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.entriesPath() + ".neu"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.entriesPath())
}

func (s *Service) addEntry(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveEntries(append(s.loadEntries(), e))
}

func (s *Service) dropEntries(ids map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keep []Entry
	for _, e := range s.loadEntries() {
		if !ids[e.ID] {
			keep = append(keep, e)
		}
	}
	if keep == nil {
		keep = []Entry{}
	}
	return s.saveEntries(keep)
}

// fingerprint bindet einen Lauf an die Vorschau.
func fingerprint(items []Item) string {
	h := sha256.New()
	for _, i := range items {
		fmt.Fprintf(h, "%s\x00%s\x00", i.Key, i.id)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// PanelImages sind Anfänge von Image-Namen, die Spielserver-Panels nutzen
// (Pterodactyl-Yolks und Eggs). Solche Images wählt HostDeck nie vor.
var PanelImages = []string{"ghcr.io/pterodactyl/", "quay.io/pterodactyl/", "ghcr.io/parkervcp/", "ghcr.io/ptero-eggs/", "ghcr.io/pelican-eggs/", "ghcr.io/pelican-dev/", "ghcr.io/calagopus/"}

func panelImage(tags []string) bool {
	for _, t := range tags {
		for _, p := range PanelImages {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
	}
	return false
}
