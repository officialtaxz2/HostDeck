// Package restore spielt einen Lauf von hostdeck backup auf einem Server zurück:
// Ordner, Volumes, Images und Datenbanken, dann startet es die Projekte.
package restore

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Files ist ein Snapshot mit Ordnern oder Dateien, die an ihren alten Ort
// zurückkommen.
type Files struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Snapshot string   `json:"snapshot"`
	Paths    []string `json:"paths"`
}

// Volume ist ein Docker-Volume, dessen Inhalt zurückkommt. Benannte Volumes
// haben Name, anonyme Service und Dest (Ziel im Container).
type Volume struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Snapshot string `json:"snapshot"`
	OldPath  string `json:"old_path"` // Ordner des Volumes auf dem alten Server
	Name     string `json:"name,omitempty"`
	External bool   `json:"external,omitempty"` // nicht von Compose angelegt
	Service  string `json:"service,omitempty"`
	Dest     string `json:"dest,omitempty"`
}

// Dump ist eine Datenbank, deren Dump eingespielt wird.
type Dump struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Snapshot string `json:"snapshot"`
	File     string `json:"file"` // Pfad im Snapshot
	Engine   string `json:"engine"`
	Service  string `json:"service"`
	// Raw heißt: Die Dateien der Datenbank kommen aus dem letzten Lauf
	// zurück; der Dump dient dann nur zum Vergleich der Zeilenzahlen.
	Raw bool `json:"raw,omitempty"`
}

// Image ist ein per docker save gesichertes Image.
type Image struct {
	Key      string   `json:"key"`
	Snapshot string   `json:"snapshot"`
	File     string   `json:"file"`
	ID       string   `json:"id"`
	Refs     []string `json:"refs,omitempty"`
	// Tags sind Namen, unter denen Container dieses Image nutzen, die aber
	// nicht im Image selbst stehen: ein Image ohne Namen, und ein Image,
	// das nach dem Anlegen des Containers umbenannt wurde. Ohne sie
	// versucht Compose, den Namen aus der Compose-Datei zu ziehen.
	Tags []string `json:"tags,omitempty"`
}

// Network ist ein Netz, das nicht Compose anlegt.
type Network struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
}

// Group ist ein Projekt.
// EnvCheck sind die Variablen eines Dienstes, die auf dem alten Server
// einen Wert hatten. Nur Namen, nie Werte.
type EnvCheck struct {
	Service string   `json:"service"`
	Set     []string `json:"set"`
}

type Group struct {
	Name    string   `json:"name"`
	Compose bool     `json:"compose"` // per docker compose startbar
	Dir     string   `json:"dir,omitempty"`
	Files   []string `json:"files,omitempty"` // Compose-Dateien
	// EnvFiles: Env-Dateien, mit denen Compose das Projekt auf dem alten
	// Server angelegt hat; HostDeck gibt sie wieder mit (--env-file).
	EnvFiles []string `json:"env_files,omitempty"`
	// Env: je Dienst die Variablen, die auf dem alten Server einen Wert
	// hatten (nur Namen). Nach dem Anlegen prüft HostDeck, ob sie hier leer sind.
	Env      []EnvCheck `json:"env,omitempty"`
	Manager  bool       `json:"manager,omitempty"`
	Paths    []Files    `json:"paths,omitempty"`
	Volumes  []Volume   `json:"volumes,omitempty"`
	Dumps    []Dump     `json:"dumps,omitempty"`
	Services []string   `json:"services,omitempty"` // liefen beim Backup
	Ports    []int      `json:"ports,omitempty"`    // auf dem Server erreichbar
	// KeptOff sind Container, die du im Leitstand gestoppt hattest: Sie
	// bleiben auch auf dem neuen Server aus.
	KeptOff []string `json:"von_dir_gestoppt,omitempty"`
}

// Plan beschreibt, was hostdeck restore tut.
type Plan struct {
	Run      string    `json:"run"`
	Source   string    `json:"source"` // Rechnername des alten Servers
	Final    bool      `json:"final"`
	Images   []Image   `json:"images,omitempty"`
	Networks []Network `json:"networks,omitempty"`
	Groups   []Group   `json:"groups"`
	Loose    []Files   `json:"loose,omitempty"`   // ohne Projekt, kommt zurück
	Volumes  []Volume  `json:"volumes,omitempty"` // ohne Projekt
	Kept     []string  `json:"kept,omitempty"`    // gesichert, kommt nicht automatisch zurück
	// Caddy: Konfiguration und Zertifikate von Caddy kommen mit; HostDeck
	// installiert Caddy dafür, wenn es fehlt. nil: Caddy kommt nicht mit.
	Caddy    *CaddyPlan `json:"caddy,omitempty"`
	Problems []string   `json:"problems,omitempty"`
	Warnings []string   `json:"warnings,omitempty"`
	// UserStopped kommt aus lauf.json: was du im Leitstand gestoppt
	// hattest, mit Zeitpunkt.
	UserStopped map[string]time.Time `json:"von_dir_gestoppt,omitempty"`
	// Space ist der Platz, den das Zurückspielen etwa braucht, je Ordner
	// auf dem neuen Server; SpaceUnknown zählt Einträge ohne Größe.
	Space        []SpaceNeed `json:"platz,omitempty"`
	SpaceUnknown int         `json:"platz_unbekannt,omitempty"`
	// Arch ist der Prozessor des alten Servers (amd64, arm64), leer wenn
	// unbekannt.
	Arch string `json:"arch,omitempty"`
}

// SpaceNeed ist der Platz, den das Zurückspielen unter Path braucht.
type SpaceNeed struct {
	Path  string `json:"pfad"`
	Bytes int64  `json:"bytes"`
}

// DockerDir ist der Ordner, in dem Docker Images und Volumes ablegt.
const DockerDir = "/var/lib/docker"

// space schätzt aus den Größen im Lauf, wie viel Platz wohin kommt.
func (p *Plan) space(rf *backup.RunFile, inv *inventory.Inventory) {
	need := map[string]int64{}
	for _, g := range rf.Groups {
		for _, list := range [][]backup.Item{g.Live, g.Dumps, g.Stopped} {
			for _, it := range list {
				if it.Archive || it.Strategy == backup.StratHost {
					continue
				}
				if it.Size < 0 {
					p.SpaceUnknown++
					continue
				}
				dir := DockerDir // Dumps landen in der Datenbank, also in einem Volume
				if len(it.Paths) > 0 && it.Strategy != backup.StratDump {
					dir = it.Paths[0]
				}
				need[dir] += it.Size
			}
		}
	}
	for _, im := range p.Images {
		if x := inv.Image(im.ID); x != nil && x.Size > 0 {
			need[DockerDir] += x.Size
		} else {
			p.SpaceUnknown++
		}
	}
	for dir, n := range need {
		p.Space = append(p.Space, SpaceNeed{Path: dir, Bytes: n})
	}
	sort.Slice(p.Space, func(i, j int) bool { return p.Space[i].Path < p.Space[j].Path })
}

// Ports sind die Ports, die die Projekte auf dem neuen Server belegen,
// mit den Projekten dazu.
func (p *Plan) Ports() map[int][]string {
	out := map[int][]string{}
	for _, g := range p.Groups {
		if len(g.Services) == 0 {
			continue
		}
		for _, pt := range g.Ports {
			out[pt] = append(out[pt], g.Name)
		}
	}
	return out
}

// Build macht aus lauf.json und dem Inventar des alten Servers den Plan.
func Build(rf *backup.RunFile, inv *inventory.Inventory) *Plan {
	p := &Plan{Run: rf.Run, Source: rf.Hostname, Final: rf.Final, UserStopped: rf.UserStopped}
	if !rf.Valid {
		p.Warnings = append(p.Warnings, "Dieser Lauf war nicht vollständig gültig; es kommt nur zurück, was gesichert wurde.")
	}
	byGroup := map[string][]backup.RunSnapshot{}
	for _, s := range rf.Snapshots {
		byGroup[s.Group] = append(byGroup[s.Group], s)
	}
	dumpInfo := map[string]backup.Item{}
	for _, g := range rf.Groups {
		for _, it := range g.Dumps {
			dumpInfo[it.Key] = it
		}
	}
	ctrByName := map[string]inventory.Container{}
	for _, c := range inv.Containers {
		ctrByName[c.Name] = c
	}

	for _, rg := range rf.Groups {
		g := Group{Name: rg.Name, Manager: len(rg.Managed) > 0}
		if pr := inv.Project("project:" + rg.Name); pr != nil && pr.WorkingDir != "" && len(pr.ConfigFiles) > 0 {
			g.Compose, g.Dir, g.Files, g.EnvFiles = true, pr.WorkingDir, pr.ConfigFiles, pr.EnvFiles
		} else {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s ist kein Compose-Projekt; seine Daten kommen zurück, den Container legst du selbst neu an.", rg.Name))
		}
		for _, c := range rg.Containers {
			_, byYou := rf.UserStopped[c.Name]
			if byYou {
				g.KeptOff = append(g.KeptOff, c.Name)
			}
			ic := ctrByName[c.Name]
			if c.Running && ic.Service != "" && !byYou {
				g.Services = append(g.Services, ic.Service)
			}
			if ic.Service != "" && len(ic.EnvSet) > 0 {
				g.Env = append(g.Env, EnvCheck{Service: ic.Service, Set: ic.EnvSet})
			}
			if c.Running && !ic.HostNetwork {
				fixed := map[string]bool{}
				for _, pt := range ic.Ports {
					switch {
					case pt.HostPort == 0:
					case !anyAddr[pt.HostIP]:
						if !fixed[pt.HostIP] {
							fixed[pt.HostIP] = true
							p.Warnings = append(p.Warnings, fmt.Sprintf("%s hört nur auf die Adresse %s des alten Servers; auf dem neuen Server startet er erst, wenn du sie in der Compose-Datei anpasst.", c.Name, pt.HostIP))
						}
					case pt.Proto == "tcp":
						g.Ports = append(g.Ports, pt.HostPort)
					}
				}
			}
		}
		g.Ports = uniqInts(g.Ports)
		sort.Strings(g.Services)
		raw := map[string]bool{}
		for _, s := range byGroup[rg.Name] {
			if s.Strategy == backup.StratRaw {
				raw[s.Key] = true
			}
		}
		for _, s := range byGroup[rg.Name] {
			switch s.Strategy {
			case backup.StratImage:
				continue // unten
			case backup.StratDump:
				it := dumpInfo[s.Key]
				svc := ctrByName[it.ContainerName].Service
				if svc == "" {
					p.Problems = append(p.Problems, fmt.Sprintf("%s: Dienst der Datenbank %s unbekannt", rg.Name, it.ContainerName))
					continue
				}
				g.Dumps = append(g.Dumps, Dump{Key: s.Key, Title: backup.Title(s.Key), Snapshot: s.Snapshot,
					File: "/" + s.Filename, Engine: s.Engine, Service: svc, Raw: raw[s.Key]})
				continue
			}
			if v, ok := p.volume(s, inv); ok {
				// Compose legt nur Volumes mit dem Etikett des eigenen
				// Projekts an. Eins von einem anderen (früheren) Projekt
				// steht in der Compose-Datei als external und muss vorher
				// da sein.
				if iv := inv.Volume(s.Key); v.Name != "" && iv != nil && strings.TrimPrefix(iv.FormerProj, "project:") != rg.Name {
					v.External = true
				}
				g.Volumes = append(g.Volumes, v)
				continue
			}
			g.Paths = append(g.Paths, Files{Key: s.Key, Title: title(s), Snapshot: s.Snapshot, Paths: s.Paths})
		}
		// Ordner von oben nach unten: erst der Projektordner, dann was darin liegt.
		sort.SliceStable(g.Paths, func(i, j int) bool { return first(g.Paths[i].Paths) < first(g.Paths[j].Paths) })
		p.Groups = append(p.Groups, g)
	}
	// Verwalter (etwa das Spielserver-Panel) zuerst.
	sort.SliceStable(p.Groups, func(i, j int) bool { return p.Groups[i].Manager && !p.Groups[j].Manager })

	for _, s := range rf.Snapshots {
		switch {
		case s.Strategy == backup.StratImage:
			im := Image{Key: s.Key, Snapshot: s.Snapshot, File: "/" + s.Filename, ID: s.ImageID, Refs: s.Refs}
			have := map[string]bool{}
			for _, r := range s.Refs {
				have[normRef(r)] = true
			}
			for _, c := range inv.Containers {
				if c.ImageID != s.ImageID || c.Image == "" || strings.HasPrefix(c.Image, "sha256:") || have[normRef(c.Image)] {
					continue
				}
				have[normRef(c.Image)] = true
				im.Tags = append(im.Tags, c.Image)
			}
			sort.Strings(im.Tags)
			p.Images = append(p.Images, im)
		case s.Group != "":
		case s.Key == CaddyKey && !s.Archive && s.Strategy == backup.StratHost:
			p.Caddy = &CaddyPlan{Snapshot: s.Snapshot, Paths: s.Paths}
			if inv.Caddy != nil {
				p.Caddy.Config, p.Caddy.UID, p.Caddy.GID = inv.Caddy.Config, inv.Caddy.UID, inv.Caddy.GID
			}
		case s.Archive || s.Strategy == backup.StratHost:
			p.Kept = append(p.Kept, title(s))
		default:
			if v, ok := p.volume(s, inv); ok {
				p.Volumes = append(p.Volumes, v)
				continue
			}
			p.Loose = append(p.Loose, Files{Key: s.Key, Title: title(s), Snapshot: s.Snapshot, Paths: s.Paths})
		}
	}
	sort.Strings(p.Kept)
	p.Networks = externalNetworks(rf, inv)
	p.space(rf, inv)
	p.Arch = inv.Host.Arch
	return p
}

// volume erkennt Volume-Schritte.
func (p *Plan) volume(s backup.RunSnapshot, inv *inventory.Inventory) (Volume, bool) {
	v := Volume{Key: s.Key, Title: backup.Title(s.Key), Snapshot: s.Snapshot, OldPath: first(s.Paths)}
	switch {
	case strings.HasPrefix(s.Key, "volume:"):
		v.Name = strings.TrimPrefix(s.Key, "volume:")
		if iv := inv.Volume(s.Key); iv != nil {
			v.External = iv.FormerProj == ""
		}
		return v, true
	case strings.HasPrefix(s.Key, "anon:compose:"):
		// anon:compose:<projekt>/<dienst>:<ziel>
		rest := strings.TrimPrefix(s.Key, "anon:compose:")
		svc, dest, ok := strings.Cut(rest, ":")
		if !ok {
			break
		}
		if _, after, ok := strings.Cut(svc, "/"); ok {
			svc = after
		}
		v.Service, v.Dest = svc, dest
		return v, true
	case strings.HasPrefix(s.Key, "anon:"):
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s: anonymes Volume ohne Compose-Dienst; es kommt nach %s zurück", backup.Title(s.Key), first(s.Paths)))
	}
	return Volume{}, false
}

// externalNetworks sind Netze ohne Compose-Label, die übernommene
// Container nutzen. Compose legt sie nicht selbst an.
func externalNetworks(rf *backup.RunFile, inv *inventory.Inventory) []Network {
	names := map[string]bool{}
	for _, g := range rf.Groups {
		for _, c := range g.Containers {
			for _, ic := range inv.Containers {
				if ic.Name == c.Name {
					for _, n := range ic.Networks {
						names[n] = true
					}
				}
			}
		}
	}
	var out []Network
	for _, n := range inv.Networks {
		if names[n.Name] && !n.Builtin && n.Project == "" {
			out = append(out, Network{Name: n.Name, Driver: n.Driver})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// anyAddr sind Adressen, die auf jedem Server gleich funktionieren.
var anyAddr = map[string]bool{"": true, "0.0.0.0": true, "::": true, "127.0.0.1": true, "::1": true}

func title(s backup.RunSnapshot) string {
	if strings.HasPrefix(s.Key, "project:") {
		return "Projektordner " + first(s.Paths)
	}
	return backup.Title(s.Key)
}

func first(ps []string) string {
	if len(ps) == 0 {
		return ""
	}
	return ps[0]
}

func uniqInts(xs []int) []int {
	sort.Ints(xs)
	var out []int
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// Write zeigt den Plan lesbar.
func (p *Plan) Write(w io.Writer) {
	kind := "normaler Lauf, Dienste liefen weiter"
	if p.Final {
		kind = "letzter Lauf, Dienste waren aus"
	}
	fmt.Fprintf(w, "Lauf %s von %s (%s)\n", p.Run, p.Source, kind)
	if len(p.Images) > 0 {
		var ns []string
		for _, im := range p.Images {
			n := strings.Join(im.Refs, ", ")
			switch {
			case n == "" && len(im.Tags) > 0:
				n = "als " + strings.Join(im.Tags, ", ")
			case n == "":
				n = "ohne Namen"
			case len(im.Tags) > 0:
				n += " (auch als " + strings.Join(im.Tags, ", ") + ")"
			}
			ns = append(ns, n)
		}
		fmt.Fprintf(w, "\nImages laden: %s\n", strings.Join(ns, "; "))
	}
	if len(p.Networks) > 0 {
		var ns []string
		for _, n := range p.Networks {
			ns = append(ns, n.Name)
		}
		fmt.Fprintf(w, "Netze anlegen: %s\n", strings.Join(ns, ", "))
	}
	fmt.Fprintf(w, "\nProjekte, in dieser Reihenfolge\n")
	for _, g := range p.Groups {
		fmt.Fprintf(w, "  %s", g.Name)
		if g.Compose {
			fmt.Fprintf(w, " (Compose in %s)", g.Dir)
		}
		fmt.Fprintln(w)
		for _, f := range g.Paths {
			fmt.Fprintf(w, "    Dateien:   %s\n", f.Title)
		}
		for _, v := range g.Volumes {
			fmt.Fprintf(w, "    Volume:    %s\n", v.Title)
		}
		for _, d := range g.Dumps {
			how := "Dump einspielen"
			if d.Raw {
				how = "Dateien aus dem letzten Lauf, Dump nur zum Vergleich"
			}
			fmt.Fprintf(w, "    Datenbank: %s im Dienst %s (%s, %s)\n", d.Title, d.Service, d.Engine, how)
		}
		if g.Compose {
			fmt.Fprintf(w, "    danach starten: %s\n", strings.Join(g.Services, ", "))
		}
		if len(g.KeptOff) > 0 {
			fmt.Fprintf(w, "    bleibt aus, wie von dir im Leitstand gestoppt: %s\n", strings.Join(g.KeptOff, ", "))
		}
	}
	if len(p.Loose)+len(p.Volumes) > 0 {
		fmt.Fprintf(w, "\nOhne Projekt\n")
		for _, f := range p.Loose {
			fmt.Fprintf(w, "  %s\n", f.Title)
		}
		for _, v := range p.Volumes {
			fmt.Fprintf(w, "  %s\n", v.Title)
		}
	}
	if p.Caddy != nil {
		fmt.Fprintf(w, "\nCaddy samt Zertifikaten: %s\n", strings.Join(p.Caddy.Paths, ", "))
		fmt.Fprintf(w, "  fehlt Caddy hier, installiert HostDeck es aus der offiziellen Quelle (dl.cloudsmith.io, Schlüssel geprüft)\n")
	}
	if len(p.Kept) > 0 {
		fmt.Fprintf(w, "\nGesichert, kommt aber nicht automatisch zurück (Archiv)\n")
		for _, k := range p.Kept {
			fmt.Fprintf(w, "  %s\n", k)
		}
	}
	if len(p.Warnings) > 0 {
		fmt.Fprintf(w, "\nHinweise\n")
		for _, s := range p.Warnings {
			fmt.Fprintf(w, "  %s\n", s)
		}
	}
	if len(p.Problems) > 0 {
		fmt.Fprintf(w, "\nSo kann es nicht losgehen:\n")
		for _, s := range p.Problems {
			fmt.Fprintf(w, "  - %s\n", s)
		}
	}
}

// Targets sind alle Orte, an die der Plan Dateien schreibt (ohne Volumes,
// deren Ordner erst Docker festlegt).
func (p *Plan) Targets() []string {
	var out []string
	add := func(fs []Files) {
		for _, f := range fs {
			for _, x := range f.Paths {
				out = append(out, path.Clean(x))
			}
		}
	}
	for _, g := range p.Groups {
		add(g.Paths)
	}
	add(p.Loose)
	sort.Strings(out)
	return out
}

// VolumeNames sind die benannten Volumes des Plans.
func (p *Plan) VolumeNames() []string {
	var out []string
	for _, g := range p.Groups {
		for _, v := range g.Volumes {
			if v.Name != "" {
				out = append(out, v.Name)
			}
		}
	}
	for _, v := range p.Volumes {
		if v.Name != "" {
			out = append(out, v.Name)
		}
	}
	sort.Strings(out)
	return out
}

// DumpVolumes sind die benannten Volumes von Datenbanken: In sie spielt
// restore nur ein, wenn sie leer sind, egal ob Dump oder Rohdateien.
func (p *Plan) DumpVolumes() []string {
	var out []string
	for _, g := range p.Groups {
		for _, d := range g.Dumps {
			if name, ok := strings.CutPrefix(d.Key, "volume:"); ok {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// normRef bringt einen Image-Namen in die Form, die Docker selbst
// speichert: „nginx“ und „docker.io/library/nginx:latest“ sind derselbe
// Name wie „nginx:latest“.
func normRef(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref // mit Prüfsumme: genau so
	}
	name, tag := ref, ""
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		name, tag = ref[:i], ref[i+1:]
	}
	if tag == "" {
		tag = "latest"
	}
	name = strings.TrimPrefix(name, "docker.io/")
	name = strings.TrimPrefix(name, "index.docker.io/")
	name = strings.TrimPrefix(name, "library/")
	return name + ":" + tag
}
