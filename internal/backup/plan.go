// Package backup plant und führt hostdeck backup aus: Aus dem Manifest wird ein
// Plan mit genau einem Schritt pro Eintrag, der Lauf sichert ihn mit restic.
package backup

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
)

// Strategien eines Schritts, wie im Manifest; dazu die eigenen Arten für
// Projektordner, Images und die Rohkopie einer Datenbank im letzten Lauf.
const (
	StratDump    = "dump"
	StratStop    = "stop-copy"
	StratLive    = "live-copy"
	StratArchive = "archive"
	StratFolder  = "projektordner"
	StratImage   = "image-save"
	StratRaw     = "rohkopie" // Dateien einer Datenbank, im letzten Lauf zusätzlich zum Dump
	StratHost    = "host"     // Konfiguration eines Dienstes am Server selbst, etwa Caddy
)

// Item ist ein Schritt: genau ein Snapshot.
type Item struct {
	Key      string   `json:"key"`             // Schlüssel aus dem Manifest, bei Images image:<ID>
	Group    string   `json:"group,omitempty"` // Projekt oder Container; leer: Archiv, Server, Images
	Title    string   `json:"title"`           // lesbar, für Ausgabe und Bericht
	Strategy string   `json:"strategy"`        // siehe Strat*
	Paths    []string `json:"paths,omitempty"` // Ordner und Dateien auf dem Host
	Excludes []string `json:"excludes,omitempty"`
	Engine   string   `json:"engine,omitempty"`
	// Für Dumps: der Container, in dem der Dump läuft.
	Container     string `json:"container,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
	// Für Images: ID und Namen, unter denen es gespeichert wird.
	ImageID string   `json:"image_id,omitempty"`
	Refs    []string `json:"refs,omitempty"`
	// Filename ist der Dateiname im Snapshot für Dumps und Images.
	Filename string `json:"filename,omitempty"`
	Size     int64  `json:"size"` // geschätzt aus dem Inventar, -1 unbekannt
	// Archive heißt: wird gesichert, aber beim Restore nicht automatisch
	// zurückgespielt.
	Archive bool `json:"archive,omitempty"`
}

// Ctr ist ein Container, den der Lauf stoppen und wieder starten muss.
type Ctr struct {
	Key     string `json:"key"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Running bool   `json:"running"`
	// DB-Container werden zuletzt gestoppt und zuerst gestartet.
	DB bool `json:"db,omitempty"`
	// Always: „restart: always“; Docker startet ihn nach einem Neustart
	// des Servers wieder, auch wenn HostDeck ihn gestoppt hat.
	Always bool `json:"always,omitempty"`
}

// ManagedRef ist ein verwalteter Server, der vor dem Kopieren aus sein muss.
type ManagedRef struct {
	Key     string `json:"key"`
	Name    string `json:"name"` // Name des Containers, wenn er läuft
	Label   string `json:"label,omitempty"`
	Running bool   `json:"running"`
}

// Group ist ein Projekt (oder ein Container ohne Projekt) mit allem, was
// dazugehört.
type Group struct {
	Name       string       `json:"name"`
	Decision   string       `json:"decision"`
	Containers []Ctr        `json:"containers,omitempty"` // alle eigenen Container, nicht die verwalteten
	Live       []Item       `json:"live,omitempty"`       // im laufenden Betrieb
	Dumps      []Item       `json:"dumps,omitempty"`
	Stopped    []Item       `json:"stopped,omitempty"` // nur bei gestoppten Containern
	Managed    []ManagedRef `json:"managed,omitempty"`
}

// NeedsStop meldet, ob der Lauf die Container dieser Gruppe stoppen muss.
func (g *Group) NeedsStop(final bool) bool {
	if len(g.Stopped) == 0 && !final {
		return false
	}
	for _, c := range g.Containers {
		if c.Running {
			return true
		}
	}
	return false
}

// RebuildImage ist ein Image, das beim Restore neu aus der Registry kommt.
type RebuildImage struct {
	Service string   `json:"service"`
	Image   string   `json:"image"`
	ID      string   `json:"id"`
	Digests []string `json:"digests,omitempty"`
}

// Skip ist ein Eintrag, der bewusst nicht gesichert wird.
type Skip struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// Plan ist alles, was ein Lauf tut, in Reihenfolge.
type Plan struct {
	Created  time.Time      `json:"created"`
	Hostname string         `json:"hostname"`
	Final    bool           `json:"final"`
	Groups   []Group        `json:"groups"`  // Gruppen mit verwalteten Servern stehen am Ende
	Archive  []Item         `json:"archive"` // Daten ohne Projekt und Einstellungen am Server
	Images   []Item         `json:"images"`
	Rebuild  []RebuildImage `json:"rebuild,omitempty"`
	Skipped  []Skip         `json:"skipped,omitempty"`
	// Problems verhindern den Lauf.
	Problems []string `json:"problems,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// AlwaysRestart sind die übernommenen Container mit „restart: always“, die
// liefen. Nach dem letzten Lauf startet Docker sie bei einem Neustart des
// alten Servers wieder.
func (p *Plan) AlwaysRestart() []string {
	var out []string
	for _, g := range p.Groups {
		for _, c := range g.Containers {
			if c.Always && c.Running {
				out = append(out, c.Name)
			}
		}
	}
	return out
}

// Options steuern den Plan.
type Options struct {
	Final bool
	// Excludes kommen nie mit, etwa der Ordner von HostDeck selbst mit dem
	// Passwort und dem Cache von restic.
	Excludes []string
	Now      time.Time
}

// Items gibt alle Schritte in der Reihenfolge des Laufs zurück.
func (p *Plan) Items() []Item {
	var all []Item
	for _, g := range p.Groups {
		all = append(all, g.Live...)
		all = append(all, g.Dumps...)
		all = append(all, g.Stopped...)
	}
	all = append(all, p.Archive...)
	all = append(all, p.Images...)
	return all
}

// TotalSize summiert die geschätzten Größen; unknown zählt die Schritte
// ohne Größe. Konfiguration am Server selbst (Caddy) ist klein und zählt
// nicht als unbekannt.
func (p *Plan) TotalSize() (total int64, unknown int) {
	for _, it := range p.Items() {
		if it.Size < 0 && it.Strategy == StratHost {
			continue
		}
		if it.Size < 0 {
			unknown++
			continue
		}
		total += it.Size
	}
	return total, unknown
}

// Build erstellt den Plan aus Manifest und frischem Inventar. Er prüft nicht,
// ob noch etwas offen ist; das tut die Vorprüfung.
func Build(m *manifest.Manifest, inv *inventory.Inventory, opt Options) *Plan {
	b := &builder{m: m, inv: inv, opt: opt}
	return b.build()
}

type builder struct {
	m     *manifest.Manifest
	inv   *inventory.Inventory
	opt   Options
	plan  *Plan
	owned []string // Pfade mit eigenem Eintrag; andere Schritte lassen sie aus
}

func (b *builder) build() *Plan {
	now := b.opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	b.plan = &Plan{Created: now.UTC(), Hostname: b.inv.Host.Hostname, Final: b.opt.Final}
	b.collectOwned()

	var normal, managers []Group
	for _, name := range sortedKeys(b.m.Projects) {
		g, ok := b.project(name, b.m.Projects[name])
		if !ok {
			continue
		}
		if len(g.Managed) > 0 {
			managers = append(managers, g)
		} else {
			normal = append(normal, g)
		}
	}
	for _, key := range sortedKeys(b.m.Containers) {
		if g, ok := b.standalone(key, b.m.Containers[key]); ok {
			normal = append(normal, g)
		}
	}
	b.plan.Groups = append(normal, managers...)
	b.unassigned()
	b.host()
	b.images()
	return b.plan
}

// collectOwned sammelt alle Pfade, die ein eigener Eintrag sind: Projekt-
// ordner und path:-Einträge, egal mit welcher Strategie. Ein Schritt, der
// einen Elternordner sichert, lässt sie aus („der genaueste gewinnt“).
func (b *builder) collectOwned() {
	set := map[string]bool{}
	for _, p := range b.m.Projects {
		if p == nil {
			continue
		}
		if p.Path != "" {
			set[p.Path] = true
		}
		for k := range p.Data {
			if strings.HasPrefix(k, "path:") {
				set[strings.TrimPrefix(k, "path:")] = true
			}
		}
	}
	for k := range b.m.Unassigned {
		if strings.HasPrefix(k, "path:") {
			set[strings.TrimPrefix(k, "path:")] = true
		}
	}
	for _, e := range b.opt.Excludes {
		set[e] = true
	}
	for _, p := range b.caddyPaths() {
		set[p] = true
	}
	for p := range set {
		b.owned = append(b.owned, p)
	}
	sort.Strings(b.owned)
}

func under(p, dir string) bool {
	if dir == "/" {
		return p != "/"
	}
	return strings.HasPrefix(p, dir+"/")
}

// net zieht von der geschätzten Größe eines Schritts die ausgelassenen
// Unterordner ab, soweit ihre Größe bekannt ist.
func (b *builder) net(it *Item) {
	if it.Size < 0 {
		return
	}
	for _, e := range it.Excludes {
		n := b.pathSize("path:" + e)
		if n < 0 {
			n = b.largest(it.Paths, e)
		}
		if n > 0 && n <= it.Size {
			it.Size -= n
		}
	}
}

// largest sucht die Größe eines Unterordners in der Liste der größten
// Unterordner, die discover für einen Ordner notiert.
func (b *builder) largest(parents []string, sub string) int64 {
	for _, p := range parents {
		if e := b.inv.Path("path:" + p); e != nil {
			for _, l := range e.Largest {
				if l.Path == sub {
					return l.Size
				}
			}
		}
	}
	return inventory.Unknown
}

// excludes liefert alle eigenen Pfade unterhalb von paths.
func (b *builder) excludes(paths []string) []string {
	var out []string
	for _, o := range b.owned {
		for _, p := range paths {
			if under(o, p) {
				out = append(out, o)
				break
			}
		}
	}
	return out
}

func (b *builder) problem(format string, args ...any) {
	b.plan.Problems = append(b.plan.Problems, fmt.Sprintf(format, args...))
}

func (b *builder) warn(format string, args ...any) {
	b.plan.Warnings = append(b.plan.Warnings, fmt.Sprintf(format, args...))
}

func (b *builder) skip(key, reason string) {
	b.plan.Skipped = append(b.plan.Skipped, Skip{Key: key, Reason: reason})
}

func (b *builder) project(name string, pr *manifest.Project) (Group, bool) {
	key := "project:" + name
	if pr == nil {
		return Group{}, false
	}
	switch pr.Decision {
	case "take", "archive":
	case "skip":
		b.skip(key, pr.Reason)
		return Group{}, false
	default:
		return Group{}, false // open: die Vorprüfung meldet es
	}
	g := Group{Name: name, Decision: pr.Decision}
	archive := pr.Decision == "archive"
	for _, c := range b.inv.Containers {
		if c.Project != key || c.ManagedBy != "" {
			continue
		}
		g.Containers = append(g.Containers, Ctr{Key: c.Key, ID: c.ID, Name: c.Name, Running: c.Running, DB: isDB(c), Always: c.RestartPolicy == "always"})
	}
	for _, mn := range sortedKeys(pr.Managed) {
		ref := ManagedRef{Key: "managed:" + mn, Name: mn}
		if ms := b.inv.ManagedServer(ref.Key); ms != nil {
			ref.Running, ref.Label = ms.Running, ms.Label
		}
		g.Managed = append(g.Managed, ref)
	}

	// Projektordner: Code und Konfiguration. Er braucht keinen Stopp, kommt
	// aber in die Stopp-Phase, wenn das Projekt ohnehin gestoppt wird.
	var folder *Item
	if pr.Path != "" {
		if ip := b.inv.Project(key); ip != nil && !ip.DirExists {
			b.warn("%s: Projektordner %s fehlt auf dem Server und wird nicht gesichert", name, pr.Path)
		} else {
			it := Item{Key: key, Group: name, Title: "Projektordner " + pr.Path, Strategy: StratFolder,
				Paths: []string{pr.Path}, Size: b.pathSize("path:" + pr.Path), Archive: archive}
			it.Excludes = b.excludes(it.Paths)
			b.net(&it)
			folder = &it
		}
	}

	for _, dk := range sortedKeys(pr.Data) {
		d := pr.Data[dk]
		if d == nil {
			continue
		}
		switch d.Strategy {
		case "skip":
			b.skip(dk, d.Reason)
			continue
		case "rebuild":
			b.skip(dk, "wird neu erzeugt")
			continue
		case "open", "":
			continue
		}
		paths, size, ok := b.dataPaths(dk)
		if !ok {
			continue
		}
		it := Item{Key: dk, Group: name, Title: title(dk), Strategy: d.Strategy, Paths: paths,
			Engine: d.Engine, Size: size, Archive: archive}
		it.Excludes = b.excludes(paths)
		b.net(&it)
		switch d.Strategy {
		case "live-copy":
			g.Live = append(g.Live, it)
		case "stop-copy":
			g.Stopped = append(g.Stopped, it)
		case "dump":
			dump, raw := b.dump(name, it)
			if dump != nil {
				g.Dumps = append(g.Dumps, *dump)
			}
			if raw != nil {
				g.Stopped = append(g.Stopped, *raw)
			}
		}
	}
	if folder != nil {
		if g.NeedsStop(b.opt.Final) {
			g.Stopped = append([]Item{*folder}, g.Stopped...)
		} else {
			g.Live = append([]Item{*folder}, g.Live...)
		}
	}
	return g, true
}

// dump plant den Dump einer Datenbank. Läuft der Container nicht, sind die
// Dateien in Ruhe und werden stattdessen kopiert. Im letzten Lauf kommt die
// Rohkopie zusätzlich dazu.
func (b *builder) dump(group string, it Item) (dump, raw *Item) {
	if !dumpable(it.Engine) {
		b.problem("%s: Für %q kann HostDeck keinen Dump machen; im Manifest stop-copy wählen", it.Key, it.Engine)
		return nil, nil
	}
	c := b.dbContainer(it.Key)
	rawItem := it
	rawItem.Strategy, rawItem.Title = StratRaw, it.Title+" (Dateien)"
	if c == nil {
		b.warn("%s: kein Datenbank-Container gefunden; die Dateien werden bei gestopptem Projekt kopiert", it.Key)
		return nil, &rawItem
	}
	if !c.Running {
		b.warn("%s: Container %s läuft nicht; die Dateien werden statt eines Dumps kopiert", it.Key, c.Name)
		return nil, &rawItem
	}
	d := it
	d.Container, d.ContainerName = c.ID, c.Name
	d.Filename = path.Join("dumps", group, fileName(it.Key)+dumpExt(it.Engine))
	d.Paths, d.Excludes = nil, nil
	if b.opt.Final {
		return &d, &rawItem
	}
	return &d, nil
}

func (b *builder) dbContainer(key string) *inventory.Container {
	var found *inventory.Container
	for i := range b.inv.Containers {
		c := &b.inv.Containers[i]
		if c.DB == nil {
			continue
		}
		match := c.DB.DataKey == key
		for _, mt := range c.Mounts {
			if mt.DataKey == key {
				match = true
			}
		}
		if !match {
			continue
		}
		if found == nil || (c.Running && !found.Running) {
			found = c
		}
	}
	return found
}

func (b *builder) standalone(key string, s *manifest.Standalone) (Group, bool) {
	if s == nil {
		return Group{}, false
	}
	switch s.Decision {
	case "take", "archive":
	case "skip":
		b.skip(key, s.Reason)
		return Group{}, false
	default:
		return Group{}, false
	}
	c := b.inv.Container(key)
	if c == nil {
		return Group{}, false
	}
	g := Group{Name: c.Name, Decision: s.Decision,
		Containers: []Ctr{{Key: c.Key, ID: c.ID, Name: c.Name, Running: c.Running, DB: isDB(*c), Always: c.RestartPolicy == "always"}}}
	// Die Daten eines Containers ohne Projekt stehen unter unassigned; mit
	// stop-copy gehören sie in seine Stopp-Phase.
	for _, mt := range c.Mounts {
		if mt.DataKey == "" {
			continue
		}
		d := b.m.Unassigned[mt.DataKey]
		if d == nil || d.Strategy != "stop-copy" {
			continue
		}
		paths, size, ok := b.dataPaths(mt.DataKey)
		if !ok {
			continue
		}
		it := Item{Key: mt.DataKey, Group: g.Name, Title: title(mt.DataKey), Strategy: StratStop, Paths: paths,
			Engine: d.Engine, Size: size, Archive: s.Decision == "archive"}
		it.Excludes = b.excludes(paths)
		b.net(&it)
		g.Stopped = append(g.Stopped, it)
	}
	return g, true
}

// claimedByStandalone meldet, ob ein unassigned-Eintrag schon in der Gruppe
// eines Containers ohne Projekt gesichert wird.
func (b *builder) claimedByStandalone(key string) bool {
	for _, g := range b.plan.Groups {
		for _, it := range g.Stopped {
			if it.Key == key {
				return true
			}
		}
	}
	return false
}

func (b *builder) unassigned() {
	for _, dk := range sortedKeys(b.m.Unassigned) {
		d := b.m.Unassigned[dk]
		if d == nil {
			continue
		}
		switch d.Strategy {
		case "skip":
			b.skip(dk, d.Reason)
			continue
		case "rebuild":
			b.skip(dk, "wird neu erzeugt")
			continue
		case "open", "":
			continue
		}
		if b.claimedByStandalone(dk) {
			continue
		}
		paths, size, ok := b.dataPaths(dk)
		if !ok {
			continue
		}
		it := Item{Key: dk, Title: title(dk), Strategy: d.Strategy, Paths: paths, Engine: d.Engine, Size: size}
		it.Excludes = b.excludes(paths)
		b.net(&it)
		switch d.Strategy {
		case "archive":
			it.Archive = true
		case "stop-copy":
			// Ohne Projekt weiß HostDeck nicht, wen es stoppen müsste. Verwaiste
			// Daten ruhen ohnehin; sonst gibt es einen Hinweis.
			if b.inUse(dk) {
				b.warn("%s: wird im laufenden Betrieb kopiert, weil kein Projekt dazugehört", dk)
			}
			it.Strategy = StratLive
		case "dump":
			// Eine Datenbank ohne Projekt: Dump, wenn ihr Container läuft.
			dump, raw := b.dump("ohne-projekt", it)
			if dump != nil {
				b.plan.Archive = append(b.plan.Archive, *dump)
			}
			if raw != nil {
				raw.Strategy = StratLive
				b.plan.Archive = append(b.plan.Archive, *raw)
			}
			continue
		}
		b.plan.Archive = append(b.plan.Archive, it)
	}
}

func (b *builder) inUse(key string) bool {
	if v := b.inv.Volume(key); v != nil {
		return len(v.UsedBy) > 0
	}
	if p := b.inv.Path(key); p != nil {
		return len(p.Mounts) > 0
	}
	return false
}

// caddyPaths sind die Dateien von Caddy, die mitkommen, wenn Caddy
// übernommen wird: der Ordner des Caddyfiles und eingebundene Dateien
// außerhalb, die zu keinem Projekt gehören.
func (b *builder) caddyPaths() []string {
	c := b.m.Host
	if c == nil || c.Caddy == nil || (c.Caddy.Decision != "take" && c.Caddy.Decision != "archive") {
		return nil
	}
	if b.inv.Caddy == nil || !b.inv.Caddy.Found {
		return nil
	}
	set := map[string]bool{}
	if dir := path.Dir(b.inv.Caddy.Config); dir != "" && dir != "/" && dir != "." {
		set[dir] = true
	}
	for _, imp := range b.inv.Caddy.Imports {
		if imp.Exists && imp.Outside && imp.Project == "" {
			set[imp.Path] = true
		}
	}
	// Zertifikate und ACME-Konto: Auf dem neuen Server haben die Seiten so
	// vom ersten Moment an gültiges HTTPS.
	if d := b.inv.Caddy.DataDir; d != "" {
		set[d] = true
	}
	var paths []string
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// host sichert die Konfiguration von Caddy, wenn es übernommen wird.
func (b *builder) host() {
	if b.m.Host == nil || b.m.Host.Caddy == nil {
		return
	}
	c := b.m.Host.Caddy
	switch c.Decision {
	case "take", "archive":
	case "skip":
		b.skip("host:caddy", c.Reason)
		return
	default:
		return
	}
	if b.inv.Caddy == nil || !b.inv.Caddy.Found {
		b.warn("host:caddy: Caddy wurde nicht gefunden und wird nicht gesichert")
		return
	}
	paths := b.caddyPaths()
	if len(paths) == 0 {
		return
	}
	it := Item{Key: "host:caddy", Title: "Caddy-Konfiguration", Strategy: StratHost, Paths: paths, Size: -1,
		Archive: c.Decision == "archive"}
	b.plan.Archive = append(b.plan.Archive, it)
}

// images plant docker save für Dienste mit image-save und merkt sich Name
// und Digest der übrigen.
func (b *builder) images() {
	seen := map[string]bool{}
	for _, name := range sortedKeys(b.m.Projects) {
		pr := b.m.Projects[name]
		if pr == nil || (pr.Decision != "take" && pr.Decision != "archive") {
			continue
		}
		for _, svc := range sortedKeys(pr.Services) {
			s := pr.Services[svc]
			if s == nil {
				continue
			}
			for _, c := range b.serviceContainers(name, svc) {
				switch s.Image {
				case "image-save":
					if seen[c.ImageID] {
						continue
					}
					seen[c.ImageID] = true
					it := Item{Key: "image:" + c.ImageID, Group: name, Strategy: StratImage, ImageID: c.ImageID, Size: -1,
						Archive: pr.Decision == "archive"}
					if img := b.inv.Image(c.ImageID); img != nil {
						it.Refs = append(it.Refs, img.Tags...)
						it.Size = img.Size
					}
					if len(it.Refs) == 0 {
						it.Title = "Image ohne Namen von " + name + "/" + svc
					} else {
						it.Title = "Image " + strings.Join(it.Refs, ", ")
					}
					it.Filename = path.Join("images", fileName(shortID(c.ImageID))+".tar")
					b.plan.Images = append(b.plan.Images, it)
				case "rebuild":
					ri := RebuildImage{Service: name + "/" + svc, Image: c.Image, ID: c.ImageID}
					if img := b.inv.Image(c.ImageID); img != nil {
						ri.Digests = img.Digests
					}
					b.plan.Rebuild = append(b.plan.Rebuild, ri)
				case "skip":
					b.skip("compose:"+name+"/"+svc, "Dienst nicht übernehmen")
				}
			}
		}
	}
}

func (b *builder) serviceContainers(project, service string) []inventory.Container {
	var out []inventory.Container
	prefix := "compose:" + project + "/" + service
	for _, c := range b.inv.Containers {
		if c.Key == prefix || strings.HasPrefix(c.Key, prefix+"#") {
			out = append(out, c)
		}
	}
	return out
}

// dataPaths löst einen Daten-Schlüssel in Host-Pfade auf.
func (b *builder) dataPaths(key string) ([]string, int64, bool) {
	switch {
	case strings.HasPrefix(key, "volume:"), strings.HasPrefix(key, "anon:"):
		v := b.inv.Volume(key)
		if v == nil {
			b.problem("%s: Volume nicht gefunden; bitte hostdeck discover und hostdeck serve erneut ausführen", key)
			return nil, 0, false
		}
		if v.Mountpoint == "" {
			b.problem("%s: Docker meldet keinen Ordner für dieses Volume (Treiber %s)", key, v.Driver)
			return nil, 0, false
		}
		return []string{v.Mountpoint}, v.Size, true
	case strings.HasPrefix(key, "path:"):
		p := strings.TrimPrefix(key, "path:")
		return []string{p}, b.pathSize(key), true
	}
	b.problem("%s: unbekannte Art von Eintrag", key)
	return nil, 0, false
}

func (b *builder) pathSize(key string) int64 {
	if p := b.inv.Path(key); p != nil {
		return p.Size
	}
	for _, u := range b.inv.Unclaimed {
		if u.Key == key {
			return u.Size
		}
	}
	return inventory.Unknown
}

func isDB(c inventory.Container) bool {
	return c.DB != nil
}

func dumpable(engine string) bool {
	switch engine {
	case "postgres", "mysql", "mariadb", "mongo":
		return true
	}
	return false
}

func dumpExt(engine string) string {
	if engine == "mongo" {
		return ".archive"
	}
	return ".sql"
}

// title macht aus einem Schlüssel eine lesbare Bezeichnung.
func title(key string) string {
	switch {
	case strings.HasPrefix(key, "volume:"):
		return "Volume " + strings.TrimPrefix(key, "volume:")
	case strings.HasPrefix(key, "anon:"):
		return "anonymes Volume " + strings.TrimPrefix(key, "anon:")
	case strings.HasPrefix(key, "path:"):
		return strings.TrimPrefix(key, "path:")
	case strings.HasPrefix(key, "project:"):
		return "Projekt " + strings.TrimPrefix(key, "project:")
	case strings.HasPrefix(key, "compose:"):
		return "Dienst " + strings.TrimPrefix(key, "compose:")
	case strings.HasPrefix(key, "container:"):
		return "Container " + strings.TrimPrefix(key, "container:")
	case strings.HasPrefix(key, "managed:"):
		return "verwalteter Server " + strings.TrimPrefix(key, "managed:")
	}
	return key
}

// fileName macht aus einem Schlüssel einen Dateinamen ohne Schrägstriche.
func fileName(key string) string {
	r := strings.NewReplacer("/", "_", ":", "-", "#", "-", " ", "_")
	return strings.Trim(r.Replace(key), "_-")
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Title macht einen Schlüssel lesbar, etwa „Volume pensum_db“.
func Title(key string) string { return title(key) }
