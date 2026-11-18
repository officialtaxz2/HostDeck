package manifest

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// Propose erstellt aus dem Inventar einen Manifest-Vorschlag. Die Regeln sind
// bewusst einfach; was das Tool nicht sicher weiß, wird „open“.
func Propose(inv *inventory.Inventory) *Manifest {
	m := &Manifest{
		Version:    Version,
		Created:    inv.CreatedAt.UTC().Format(time.RFC3339),
		Inventory:  "inventory.json",
		Source:     Source{Hostname: inv.Host.Hostname, OS: inv.Host.OS},
		Projects:   map[string]*Project{},
		Unassigned: map[string]*Data{},
		Containers: map[string]*Standalone{},
	}
	for _, p := range inv.Projects {
		m.Projects[p.Name] = proposeProject(inv, p)
	}
	for _, v := range inv.Volumes {
		if !v.Orphan && v.Project == "" {
			d := volumeData(inv, v)
			d.Note = join(d.Note, "genutzt von "+shortKeys(v.UsedBy))
			m.Unassigned[v.Key] = d
		}
		if !v.Orphan {
			continue
		}
		// Verwaiste Volumes kommen ins Archiv: gesichert, aber nicht
		// wiederhergestellt. Reine Caches fallen weg.
		d := &Data{Strategy: "archive"}
		if isCacheName(v.Name) {
			d.Strategy, d.Reason = "skip", "verwaister Cache"
		}
		if v.DB != nil {
			d.Engine = v.DB.Engine
		} else if len(v.SQLite) > 0 {
			d.Engine = "sqlite"
		}
		note := "von keinem Container genutzt"
		if v.FormerProj != "" {
			note += "; gehörte zu " + strings.TrimPrefix(v.FormerProj, "project:")
		}
		if v.Size >= 0 {
			note += "; " + units.HumanSize(v.Size)
		}
		d.Note = note
		m.Unassigned[v.Key] = d
	}
	for _, e := range inv.Paths {
		if !ownData(e) || strings.HasPrefix(e.Owner, "project:") {
			continue
		}
		d := pathData(inv, e)
		d.Note = join(d.Note, "gehört zu Container "+shortKey(e.Owner))
		m.Unassigned[e.Key] = d
	}
	for _, u := range inv.Unclaimed {
		note := "gehört zu keinem laufenden Projekt"
		strategy := "archive"
		switch u.Area {
		case inventory.AreaScan:
		case inventory.AreaFound:
			note, strategy = "", "open"
		default:
			note = ""
		}
		note = join(note, u.Note)
		if u.Git != nil && u.Git.Remote != "" {
			note = join(note, "Git: "+u.Git.Remote)
		}
		if u.Size >= 0 {
			note = join(note, units.HumanSize(u.Size))
		}
		m.Unassigned[u.Key] = &Data{Strategy: strategy, Note: note}
		for _, c := range u.Caches {
			m.Unassigned["path:"+c.Path] = &Data{Strategy: "skip", Reason: "Cache, lässt sich neu laden"}
		}
	}
	for _, c := range inv.Containers {
		if strings.HasPrefix(c.Key, "container:") {
			m.Containers[c.Key] = &Standalone{Decision: "open", Note: "ohne Compose-Projekt; Image " + c.Image}
		}
	}
	if inv.Caddy != nil && inv.Caddy.Found {
		note := fmt.Sprintf("%d Routen; wird nach den Projekten wiederhergestellt", len(inv.Caddy.Routes))
		m.Host = &Host{Caddy: &Standalone{Decision: "take", Note: note}}
	}
	for _, s := range inv.System {
		m.System = append(m.System, SystemEntry{Container: s.Container, Source: s.Source})
	}
	if len(m.Unassigned) == 0 {
		m.Unassigned = nil
	}
	if len(m.Containers) == 0 {
		m.Containers = nil
	}
	return m
}

func proposeProject(inv *inventory.Inventory, p inventory.Project) *Project {
	pr := &Project{
		Decision: "take",
		Path:     p.Path,
		Domains:  append([]string{}, p.Domains...),
		Services: map[string]*Service{},
		Managed:  map[string]*Managed{},
		Data:     map[string]*Data{},
	}
	var notes []string
	switch {
	case !p.DirExists:
		pr.Decision = "open"
		notes = append(notes, "Compose-Ordner "+p.WorkingDir+" fehlt")
	case p.Running == 0:
		pr.Decision = "open"
		notes = append(notes, "alle Container gestoppt")
	}
	for _, ck := range p.Containers {
		c := inv.Container(ck)
		if c == nil {
			continue
		}
		if !c.Running && p.Running > 0 {
			notes = append(notes, c.Name+" ist gestoppt")
		}
		if _, ok := pr.Services[c.Service]; ok {
			continue
		}
		s := &Service{Image: "rebuild"}
		if c.ImageLocal {
			s.Image = "image-save"
			switch {
			case strings.HasPrefix(c.Image, "sha256:") || !hasTag(inv, c.ImageID):
				s.Note = "Image ohne Namen, wird per ID gesichert"
			case c.ImageOrigin == inventory.OriginUnknown:
				s.Note = "Herkunft unklar, darum zur Sicherheit gesichert"
			}
		}
		pr.Services[c.Service] = s
	}
	if p.Git != nil && p.Git.Dirty > 0 {
		notes = append(notes, fmt.Sprintf("%d nicht committete Änderung(en)", p.Git.Dirty))
	}
	for _, mk := range p.Managed {
		m := inv.ManagedServer(mk)
		if m == nil {
			continue
		}
		mgr := shortKey(m.Manager)
		if mc := inv.Container(m.Manager); mc != nil && mc.Service != "" {
			mgr = mc.Service
		}
		where := m.Path
		if where == "" {
			where = m.Root
		}
		note := "Daten in " + where + "; vor dem Backup über den Verwalter stoppen"
		if !m.Container {
			note = "gerade aus, kein Container; Daten in " + where
		}
		if m.Label != "" {
			note = m.Label + "; " + note
		}
		pr.Managed[m.Name] = &Managed{Manager: mgr, Stop: "manager", Note: note}
	}
	for _, e := range inv.Paths {
		if e.Owner == p.Key && ownData(e) {
			pr.Data[e.Key] = pathData(inv, e)
		}
	}
	for _, v := range inv.Volumes {
		if v.Project == p.Key && !v.Orphan {
			pr.Data[v.Key] = volumeData(inv, v)
		}
	}
	pr.Note = strings.Join(notes, "; ")
	if len(pr.Services) == 0 {
		pr.Services = nil
	}
	if len(pr.Managed) == 0 {
		pr.Managed = nil
	}
	if len(pr.Data) == 0 {
		pr.Data = nil
	}
	return pr
}

func ownData(e inventory.Path) bool {
	return (e.Kind == inventory.KindData || e.Kind == inventory.KindConfig) && e.CoveredBy == ""
}

func hasTag(inv *inventory.Inventory, imageID string) bool {
	for _, img := range inv.Images {
		if img.ID == imageID {
			return len(img.Tags) > 0
		}
	}
	return true
}

func pathData(inv *inventory.Inventory, e inventory.Path) *Data {
	var users []string
	rw := false
	for _, m := range e.Mounts {
		users = append(users, m.Container)
		rw = rw || m.RW
	}
	d := strategyFor(inv, e.DB, e.SQLite, users, rw, e.Kind == inventory.KindConfig)
	var managed []string
	for _, m := range inv.Managed {
		if m.Root == e.Path {
			managed = append(managed, m.Name)
		}
	}
	if len(managed) > 0 {
		d.Strategy = "stop-copy"
		d.Note = join(d.Note, "enthält verwaltete Server ("+strings.Join(managed, ", ")+"); laufende zuerst über den Verwalter stoppen")
	}
	if len(e.Largest) > 0 {
		var parts []string
		for _, l := range e.Largest {
			if len(parts) == 3 {
				break
			}
			parts = append(parts, l.Path+" "+units.HumanSize(l.Size))
		}
		d.Note = join(d.Note, "größte Unterordner: "+strings.Join(parts, ", ")+"; einzelne lassen sich mit eigenem path:-Eintrag und strategy skip ausnehmen")
	}
	return d
}

func volumeData(inv *inventory.Inventory, v inventory.Volume) *Data {
	return strategyFor(inv, v.DB, v.SQLite, v.UsedBy, true, false)
}

// strategyFor wählt die vorgeschlagene Strategie für einen Datenordner.
func strategyFor(inv *inventory.Inventory, db *inventory.DB, sqlite []string, users []string, rw, file bool) *Data {
	running := false
	for _, u := range users {
		if c := inv.Container(u); c != nil && c.Running {
			running = true
		}
	}
	switch {
	case db != nil && isDumpEngine(db.Engine):
		if running {
			return &Data{Strategy: "dump", Engine: db.Engine, Note: versionNote(db)}
		}
		return &Data{Strategy: "stop-copy", Engine: db.Engine, Note: join(versionNote(db), "Datenbank läuft nicht, daher Rohkopie")}
	case db != nil:
		return &Data{Strategy: "stop-copy", Engine: db.Engine, Note: "falls reiner Cache: rebuild"}
	case len(sqlite) > 0:
		return &Data{Strategy: "stop-copy", Engine: "sqlite", Note: "SQLite: " + strings.Join(sqlite, ", ")}
	case file || !rw:
		return &Data{Strategy: "live-copy"}
	default:
		return &Data{Strategy: "stop-copy"}
	}
}

func isCacheName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "cache")
}

func isDumpEngine(e string) bool {
	switch e {
	case "postgres", "mysql", "mariadb", "mongo":
		return true
	}
	return false
}

func versionNote(db *inventory.DB) string {
	if db.Version == "" {
		return ""
	}
	return "Version " + db.Version
}

func join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

func shortKey(k string) string {
	for _, p := range []string{"project:", "compose:", "container:", "managed:"} {
		k = strings.TrimPrefix(k, p)
	}
	return k
}

func shortKeys(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = shortKey(k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
