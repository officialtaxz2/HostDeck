package web

import (
	"path"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
	"github.com/officialtaxz2/hostdeck/internal/runlog"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

// State ist alles, was die Seite anzeigt: das Inventar, aufbereitet für
// Menschen, und daneben die Entscheidungen aus dem Manifest. Werte von
// Umgebungsvariablen kommen darin nicht vor, weil das Inventar keine hat.
type State struct {
	Version    string          `json:"version"`
	Host       HostView        `json:"host"`
	Created    time.Time       `json:"created"`
	Tool       string          `json:"tool"`
	Manifest   ManifestView    `json:"manifest"`
	Findings   []FindingView   `json:"findings"`
	Projects   []ProjectView   `json:"projects"`
	Containers []ContainerView `json:"containers"`
	Loose      []DataView      `json:"loose"`
	Caddy      *CaddyView      `json:"caddy,omitempty"`
	Gone       []GoneView      `json:"gone"`
	System     []SystemView    `json:"system"`
	// Am Server selbst: nur zur Information, Entscheidungen gibt es dafür
	// nicht. Die Dateien liegen in den archivierten Ordnern.
	Tasks    []inventory.Task  `json:"tasks"`
	Checks   []inventory.Check `json:"checks"`
	Settings []SettingGroup    `json:"settings"`
	Space    *inventory.Space  `json:"space,omitempty"`
	// Die letzten Backup-Läufe aus backup-laeufe.json, neueste zuerst.
	Backups     []runlog.Entry `json:"backups"`
	BackupsNote string         `json:"backups_note,omitempty"` // Liste nicht lesbar
	// Limits sind die Voreinstellungen der Überwachung. Ohne Dienst färbt
	// die Seite Platte, Arbeitsspeicher und Prozessor danach (R8); mit
	// Dienst gelten dessen Grenzen.
	Limits ueberwachung.Limits `json:"grenzen"`
}

// SettingGroup fasst geänderte und eigene Dateien eines Ordners in /etc
// zusammen.
type SettingGroup struct {
	Dir      string   `json:"dir"`
	Modified []string `json:"modified"`
	Added    []string `json:"added"`
}

type HostView struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Docker   string `json:"docker,omitempty"` // Version
	NoDocker bool   `json:"ohne_docker,omitempty"`
	Root     bool   `json:"root"` // discover lief als root
}

// Zustände des Manifests.
const (
	ManifestOK      = "vorhanden"
	ManifestMissing = "fehlt"
	ManifestInvalid = "ungueltig"
)

type ManifestView struct {
	State    string     `json:"state"`
	Problems []string   `json:"problems,omitempty"`
	Added    []string   `json:"added"`           // neu seit dem letzten Speichern, steht auf dem Vorschlag
	Saved    *time.Time `json:"saved,omitempty"` // zuletzt gespeichert
}

type FindingView struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

type GitView struct {
	Remote string `json:"remote,omitempty"`
	Branch string `json:"branch,omitempty"`
	Dirty  int    `json:"dirty"`
}

type ProjectView struct {
	Key       string              `json:"key"`
	Name      string              `json:"name"`
	Path      string              `json:"path"`
	DirExists bool                `json:"dir_exists"`
	Domains   []string            `json:"domains"`
	Running   int                 `json:"running"`
	Total     int                 `json:"total"`
	Git       *GitView            `json:"git,omitempty"`
	Size      int64               `json:"size"`
	Largest   []inventory.SubSize `json:"largest,omitempty"`
	Decision  string              `json:"decision"`
	Reason    string              `json:"reason"`
	Note      string              `json:"note,omitempty"`
	New       bool                `json:"new,omitempty"`
	Services  []ServiceView       `json:"services"`
	Managed   []ManagedView       `json:"managed"`
	Data      []DataView          `json:"data"`
	Missing   bool                `json:"missing,omitempty"` // fehlt im Manifest
}

type ServiceView struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Containers []string `json:"containers"`
	Running    bool     `json:"running"`
	ImageName  string   `json:"image_name"`
	ImageID    string   `json:"image_id"`
	ImageSize  int64    `json:"image_size"`
	Local      bool     `json:"local"`
	Origin     string   `json:"origin,omitempty"`      // registry | gebaut | unklar
	OriginNote string   `json:"origin_note,omitempty"` // woran discover die Herkunft erkannt hat
	Source     string   `json:"source,omitempty"`      // Registry-Repository laut Docker
	Image      string   `json:"image"`                 // Entscheidung: image-save | rebuild | skip
	Note       string   `json:"note,omitempty"`
	New        bool     `json:"new,omitempty"`
}

type ManagedView struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Manager   string `json:"manager"`
	Container bool   `json:"container"` // false: Server ist aus, Wings hat den Container gelöscht
	Running   bool   `json:"running"`
	State     string `json:"state,omitempty"`
	Path      string `json:"path,omitempty"` // Datenordner des Servers
	Size      int64  `json:"size"`
	Label     string `json:"label,omitempty"` // etwa die MOTD eines Minecraft-Servers
}

// DataView ist ein Daten-Eintrag: Volume, anonymes Volume oder Pfad.
type DataView struct {
	Key           string              `json:"key"`
	Type          string              `json:"type"` // volume | anon | path
	Label         string              `json:"label"`
	Kind          string              `json:"kind,omitempty"`
	Exists        bool                `json:"exists"`
	Size          int64               `json:"size"`
	Engine        string              `json:"engine,omitempty"`
	EngineVersion string              `json:"engine_version,omitempty"`
	SQLite        []string            `json:"sqlite,omitempty"`
	Users         []string            `json:"users,omitempty"`
	Running       bool                `json:"running"`
	Orphan        bool                `json:"orphan,omitempty"`
	FormerProject string              `json:"former_project,omitempty"`
	Git           *GitView            `json:"git,omitempty"`
	Largest       []inventory.SubSize `json:"largest,omitempty"`
	SplitOf       string              `json:"split_of,omitempty"` // eigener Unterpfad: Schlüssel des Eltern-Eintrags oder des Projekts
	Area          string              `json:"area,omitempty"`     // bei Ordnern am Server selbst: home, web, programme, einstellungen, fund
	Strategy      string              `json:"strategy"`
	Reason        string              `json:"reason"`
	Note          string              `json:"note,omitempty"`
	New           bool                `json:"new,omitempty"`
}

type ContainerView struct {
	Key      string     `json:"key"`
	Name     string     `json:"name"`
	Image    string     `json:"image"`
	State    string     `json:"state"`
	Running  bool       `json:"running"`
	Decision string     `json:"decision"`
	Reason   string     `json:"reason"`
	Note     string     `json:"note,omitempty"`
	New      bool       `json:"new,omitempty"`
	Data     []DataView `json:"data"`
}

type CaddyView struct {
	Config   string      `json:"config"`
	Error    string      `json:"error,omitempty"`
	Routes   []RouteView `json:"routes"`
	Imports  []string    `json:"imports"`
	Decision string      `json:"decision"`
	Reason   string      `json:"reason"`
	Note     string      `json:"note,omitempty"`
	New      bool        `json:"new,omitempty"`
}

type RouteView struct {
	Hosts    []string `json:"hosts"`
	Upstream string   `json:"upstream,omitempty"`
	Target   string   `json:"target,omitempty"`
	Status   string   `json:"status"`
}

type GoneView struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Where string `json:"where"`
}

type SystemView struct {
	Container string `json:"container"`
	Source    string `json:"source"`
}

// buildState baut die Ansicht aus Inventar und (bereits ergänztem) Manifest.
func buildState(inv *inventory.Inventory, m *manifest.Manifest, mv ManifestView, gone []string) *State {
	b := &builder{inv: inv, m: m, isNew: map[string]bool{}, used: map[string]bool{}, gone: map[string]bool{}}
	for _, k := range mv.Added {
		b.isNew[k] = true
	}
	for _, k := range gone {
		b.gone[k] = true
	}
	// Leere Listen als [] statt null, damit die Seite nichts prüfen muss.
	st := &State{
		Host:       HostView{Hostname: inv.Host.Hostname, OS: inv.Host.OS, Docker: inv.Docker.Version, NoDocker: inv.Docker.Missing, Root: inv.Host.Root},
		Created:    inv.CreatedAt,
		Tool:       inv.Tool,
		Manifest:   mv,
		Findings:   []FindingView{},
		Projects:   []ProjectView{},
		Containers: []ContainerView{},
		Loose:      []DataView{},
		Gone:       []GoneView{},
		Limits:     ueberwachung.DefaultLimits,
		System:     []SystemView{},
		Tasks:      append([]inventory.Task{}, inv.Tasks...),
		Checks:     append([]inventory.Check{}, inv.Checks...),
		Settings:   settingGroups(inv.Settings),
		Space:      inv.Space,
	}
	for _, f := range inv.Findings {
		st.Findings = append(st.Findings, FindingView{Level: f.Level, Message: f.Message})
	}
	sort.SliceStable(st.Findings, func(i, j int) bool {
		return st.Findings[i].Level == inventory.LevelWarn && st.Findings[j].Level != inventory.LevelWarn
	})
	for _, p := range inv.Projects {
		st.Projects = append(st.Projects, b.project(p))
	}
	for _, c := range inv.Containers {
		if strings.HasPrefix(c.Key, "container:") {
			st.Containers = append(st.Containers, b.container(c))
		}
	}
	// Was übrig bleibt, sind Daten ohne Projekt: verwaiste Volumes, alte
	// Ordner und alles, was einem nicht erfassten Container gehört.
	for _, k := range sortedKeys(m.Unassigned) {
		if b.used[k] {
			continue
		}
		if dv, ok := b.data(k, m.Unassigned[k]); ok {
			st.Loose = append(st.Loose, dv)
		}
	}
	st.Loose = b.attachSplits(st.Loose, "", "")
	if inv.Caddy != nil && inv.Caddy.Found {
		st.Caddy = b.caddy()
	}
	for _, k := range gone {
		st.Gone = append(st.Gone, GoneView{Key: k, Label: b.goneLabel(k), Where: b.where(k)})
	}
	for _, s := range inv.System {
		st.System = append(st.System, SystemView{Container: b.ctrName(s.Container), Source: s.Source})
	}
	return st
}

type builder struct {
	inv   *inventory.Inventory
	m     *manifest.Manifest
	isNew map[string]bool
	used  map[string]bool // Daten-Schlüssel, die schon bei einem Projekt oder Container stehen
	gone  map[string]bool // stehen nur im Abschnitt „weggefallen“
}

func (b *builder) project(p inventory.Project) ProjectView {
	pv := ProjectView{
		Key:       p.Key,
		Name:      p.Name,
		Path:      p.Path,
		DirExists: p.DirExists,
		Domains:   nonNil(p.Domains),
		Running:   p.Running,
		Total:     len(p.Containers) - finishedJobs(b.inv, p.Containers),
		Size:      inventory.Unknown,
		New:       b.isNew[p.Key],
		Services:  []ServiceView{},
		Managed:   []ManagedView{},
		Data:      []DataView{},
	}
	if p.Git != nil {
		pv.Git = &GitView{Remote: p.Git.Remote, Branch: p.Git.Branch, Dirty: p.Git.Dirty}
	}
	if e := b.inv.Path("path:" + p.Path); e != nil {
		pv.Size, pv.Largest = e.Size, e.Largest
	}
	// Verwaltete Server zählen mit, solange es ihren Container gibt. Server
	// ohne Container sind aus und stehen nur in der Liste.
	for _, mk := range p.Managed {
		m := b.inv.ManagedServer(mk)
		if m == nil {
			continue
		}
		if m.Container {
			pv.Total++
			if m.Running {
				pv.Running++
			}
		}
		where := m.Path
		if where == "" {
			where = m.Root
		}
		pv.Managed = append(pv.Managed, ManagedView{Key: mk, Name: m.Name, Manager: b.ctrName(m.Manager),
			Container: m.Container, Running: m.Running, State: m.State, Path: where, Size: m.Size, Label: m.Label})
	}
	mp := b.m.Projects[p.Name]
	if mp == nil {
		pv.Decision, pv.Missing = "open", true
		return pv
	}
	pv.Decision, pv.Reason, pv.Note = mp.Decision, mp.Reason, mp.Note

	byService := map[string]*ServiceView{}
	for _, ck := range p.Containers {
		c := b.inv.Container(ck)
		if c == nil {
			continue
		}
		sv := byService[c.Service]
		if sv == nil {
			key := "compose:" + p.Name + "/" + c.Service
			sv = &ServiceView{Key: key, Name: c.Service, ImageName: c.Image, ImageID: c.ImageID, Local: c.ImageLocal,
				ImageSize: inventory.Unknown, New: b.isNew[key]}
			untagged := false
			for _, img := range b.inv.Images {
				if img.ID == c.ImageID {
					sv.ImageSize = img.Size
					sv.Origin, sv.OriginNote, sv.Source = img.Origin, img.OriginNote, img.Source
					untagged = len(img.Tags) == 0
					if strings.HasPrefix(sv.ImageName, "sha256:") && len(img.Tags) > 0 {
						sv.ImageName = img.Tags[0]
					}
				}
			}
			switch id := strings.TrimPrefix(c.ImageID, "sha256:"); {
			case strings.HasPrefix(sv.ImageName, "sha256:") && len(id) >= 12:
				sv.ImageName = "Image ohne Namen (" + id[:12] + ")"
			case untagged && len(id) >= 12:
				sv.ImageName += " (Image hat keinen Namen mehr, ID " + id[:12] + ")"
			}
			if ms := mp.Services[c.Service]; ms != nil {
				sv.Image, sv.Note = ms.Image, ms.Note
			}
			byService[c.Service] = sv
		}
		sv.Containers = append(sv.Containers, c.Name)
		sv.Running = sv.Running || c.Running
	}
	for _, name := range sortedKeys(byService) {
		pv.Services = append(pv.Services, *byService[name])
	}
	for _, k := range sortedKeys(mp.Data) {
		b.used[k] = true
		if dv, ok := b.data(k, mp.Data[k]); ok {
			pv.Data = append(pv.Data, dv)
		}
	}
	pv.Data = b.attachSplits(pv.Data, p.Key, p.Path)
	return pv
}

func (b *builder) container(c inventory.Container) ContainerView {
	cv := ContainerView{Key: c.Key, Name: c.Name, Image: c.Image, State: c.State, Running: c.Running,
		Decision: "open", New: b.isNew[c.Key], Data: []DataView{}}
	if mc := b.m.Containers[c.Key]; mc != nil {
		cv.Decision, cv.Reason, cv.Note = mc.Decision, mc.Reason, mc.Note
	}
	for _, k := range sortedKeys(b.m.Unassigned) {
		if b.belongsTo(k, c.Key) {
			b.used[k] = true
			if dv, ok := b.data(k, b.m.Unassigned[k]); ok {
				cv.Data = append(cv.Data, dv)
			}
		}
	}
	return cv
}

// belongsTo: der Daten-Eintrag gehört nur diesem Container.
func (b *builder) belongsTo(key, ctr string) bool {
	if v := b.inv.Volume(key); v != nil {
		return !v.Orphan && v.Project == "" && len(v.UsedBy) == 1 && v.UsedBy[0] == ctr
	}
	if e := b.inv.Path(key); e != nil {
		return e.Owner == ctr
	}
	return false
}

// data baut die Ansicht eines Daten-Eintrags. Weggefallene Einträge
// liefern ok=false; sie erscheinen nur im Abschnitt „weggefallen“.
func (b *builder) data(key string, d *manifest.Data) (DataView, bool) {
	if b.gone[key] || d == nil {
		return DataView{}, false
	}
	dv := DataView{Key: key, Size: inventory.Unknown, Strategy: d.Strategy, Reason: d.Reason, Note: d.Note,
		Engine: d.Engine, New: b.isNew[key]}
	switch {
	case strings.HasPrefix(key, "volume:"), strings.HasPrefix(key, "anon:"):
		dv.Type = "volume"
		if strings.HasPrefix(key, "anon:") {
			dv.Type = "anon"
		}
		dv.Label = strings.TrimPrefix(key, dv.Type+":")
		v := b.inv.Volume(key)
		if v == nil {
			return dv, true
		}
		dv.Exists, dv.Size, dv.Orphan = true, v.Size, v.Orphan
		dv.Label = v.Name
		if v.Anonymous && len(v.Name) == 64 {
			dv.Label = v.Name[:12] + "… (anonym)"
		}
		if dv.Type == "anon" {
			_, dest := splitAnon(key)
			dv.Label = "anonymes Volume " + dest
		}
		if v.FormerProj != "" {
			dv.FormerProject = strings.TrimPrefix(v.FormerProj, "project:")
		}
		b.users(&dv, v.UsedBy)
		b.db(&dv, v.DB, v.SQLite)
	case strings.HasPrefix(key, "path:"):
		dv.Type = "path"
		dv.Label = strings.TrimPrefix(key, "path:")
		if e := b.inv.Path(key); e != nil {
			dv.Exists, dv.Size, dv.Kind, dv.Largest = e.Exists, e.Size, e.Kind, e.Largest
			var users []string
			for _, m := range e.Mounts {
				users = append(users, m.Container)
			}
			b.users(&dv, users)
			b.db(&dv, e.DB, e.SQLite)
			return dv, true
		}
		for _, u := range b.inv.Unclaimed {
			if u.Key == key {
				dv.Exists, dv.Size, dv.Area = true, u.Size, u.Area
				if u.Git != nil {
					dv.Git = &GitView{Remote: u.Git.Remote, Branch: u.Git.Branch, Dirty: u.Git.Dirty}
				}
				return dv, true
			}
		}
		// Nicht im Inventar und nicht weggefallen: ein eigener Unterpfad,
		// den attachSplits seinem Eltern-Eintrag zuordnet.
		dv.SplitOf = splitPending
	default:
		return dv, false
	}
	return dv, true
}

const splitPending = "?"

// attachSplits ordnet eigene Unterpfade dem genauesten Eltern-Eintrag zu,
// sonst dem Projektordner. Ohne passenden Eltern-Eintrag bleiben sie ein
// gewöhnlicher Eintrag.
func (b *builder) attachSplits(list []DataView, projectKey, projectPath string) []DataView {
	var out, splits []DataView
	for _, dv := range list {
		if dv.SplitOf == splitPending {
			splits = append(splits, dv)
		} else {
			out = append(out, dv)
		}
	}
	for _, dv := range splits {
		p := strings.TrimPrefix(dv.Key, "path:")
		dv.SplitOf = ""
		parentPath := ""
		for _, cand := range out {
			cp := strings.TrimPrefix(cand.Key, "path:")
			if cand.Type == "path" && cand.SplitOf == "" && strings.HasPrefix(p, cp+"/") && len(cp) > len(parentPath) {
				dv.SplitOf, parentPath = cand.Key, cp
			}
		}
		if dv.SplitOf == "" && projectPath != "" && strings.HasPrefix(p, projectPath+"/") {
			dv.SplitOf, parentPath = projectKey, projectPath
		}
		if dv.SplitOf != "" {
			dv.Size = b.subSize(parentPath, p)
		}
		out = append(out, dv)
	}
	if out == nil {
		out = []DataView{}
	}
	return out
}

// subSize nimmt die Größe eines verwalteten Servers oder aus der Liste der
// größten Unterordner des Eltern-Ordners, falls er dort steht.
func (b *builder) subSize(parent, p string) int64 {
	for _, m := range b.inv.Managed {
		if m.Path == p {
			return m.Size
		}
	}
	for _, u := range b.inv.Unclaimed {
		for _, c := range u.Caches {
			if c.Path == p {
				return c.Size
			}
		}
	}
	if e := b.inv.Path("path:" + parent); e != nil {
		for _, l := range e.Largest {
			if l.Path == p {
				return l.Size
			}
		}
	}
	return inventory.Unknown
}

func (b *builder) users(dv *DataView, keys []string) {
	for _, k := range keys {
		dv.Users = appendUnique(dv.Users, b.ctrName(k))
		if c := b.inv.Container(k); c != nil && c.Running {
			dv.Running = true
		}
	}
}

func (b *builder) db(dv *DataView, db *inventory.DB, sqlite []string) {
	if db != nil {
		if dv.Engine == "" {
			dv.Engine = db.Engine
		}
		dv.EngineVersion = db.Version
	}
	dv.SQLite = sqlite
}

func (b *builder) caddy() *CaddyView {
	c := b.inv.Caddy
	cv := &CaddyView{Config: c.Config, Error: c.Error, Routes: []RouteView{}, Imports: []string{}, Decision: "open", New: b.isNew["host:caddy"]}
	for _, r := range c.Routes {
		rv := RouteView{Hosts: nonNil(r.Hosts), Upstream: r.Upstream, Status: r.Status}
		switch {
		case strings.HasPrefix(r.Target, "host:"):
			rv.Target = "Dienst " + strings.TrimPrefix(r.Target, "host:")
		case r.Target == "host":
			rv.Target = "Dienst auf dem Host"
		case r.Target != "":
			rv.Target = b.ctrName(r.Target)
		}
		cv.Routes = append(cv.Routes, rv)
	}
	for _, imp := range c.Imports {
		cv.Imports = append(cv.Imports, imp.Path)
	}
	if b.m.Host != nil && b.m.Host.Caddy != nil {
		mc := b.m.Host.Caddy
		cv.Decision, cv.Reason, cv.Note = mc.Decision, mc.Reason, mc.Note
	}
	return cv
}

func (b *builder) ctrName(key string) string {
	if c := b.inv.Container(key); c != nil {
		return c.Name
	}
	for _, p := range []string{"project:", "compose:", "container:", "managed:"} {
		key = strings.TrimPrefix(key, p)
	}
	return key
}

func (b *builder) goneLabel(k string) string {
	for _, p := range []string{"project:", "compose:", "container:", "managed:", "volume:", "anon:", "path:"} {
		if strings.HasPrefix(k, p) {
			return strings.TrimPrefix(k, p)
		}
	}
	return k
}

func (b *builder) where(k string) string {
	switch {
	case strings.HasPrefix(k, "project:"):
		return "Projekt"
	case strings.HasPrefix(k, "compose:"):
		return "Dienst"
	case strings.HasPrefix(k, "managed:"):
		return "verwalteter Container"
	case strings.HasPrefix(k, "container:"):
		return "Container"
	case k == "host:caddy":
		return "Caddy"
	case strings.HasPrefix(k, "volume:"), strings.HasPrefix(k, "anon:"):
		return "Volume"
	}
	// Weggefallen heißt: Auf dem Server ist nichts mehr da. Ob es ein Ordner
	// oder eine Datei war, weiß HostDeck dann nicht mehr.
	return "Pfad"
}

func splitAnon(key string) (ctr, dest string) {
	rest := strings.TrimPrefix(key, "anon:")
	if i := strings.LastIndex(rest, ":/"); i >= 0 {
		return rest[:i], rest[i+1:]
	}
	return rest, ""
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func settingGroups(list []inventory.Setting) []SettingGroup {
	out := []SettingGroup{}
	idx := map[string]int{}
	for _, st := range list {
		dir, name := path.Split(st.Path)
		dir = strings.TrimSuffix(dir, "/")
		i, ok := idx[dir]
		if !ok {
			i = len(out)
			idx[dir] = i
			out = append(out, SettingGroup{Dir: dir, Modified: []string{}, Added: []string{}})
		}
		if st.Change == inventory.ChangeModified {
			out[i].Modified = append(out[i].Modified, name)
		} else {
			out[i].Added = append(out[i].Added, name)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Dir < out[b].Dir })
	return out
}

// finishedJobs zählt Einmal-Jobs, die erledigt sind: beendet mit Code 0 und
// ohne Neustart-Regel, die sie wieder starten würde. Sie zählen nicht als
// gestoppt (etwa ein Aufräum-Job, der beim Start einmal läuft).
func finishedJobs(inv *inventory.Inventory, keys []string) int {
	n := 0
	for _, k := range keys {
		c := inv.Container(k)
		if c == nil || c.Running || c.State != "exited" || c.ExitCode != 0 {
			continue
		}
		switch c.RestartPolicy {
		case "", "no", "on-failure":
			n++
		}
	}
	return n
}
