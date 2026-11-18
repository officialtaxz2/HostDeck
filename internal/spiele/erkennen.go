package spiele

import (
	"bytes"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Zustände eines Spielservers.
const (
	StateRunning = "laeuft"
	StateStart   = "startet"
	StatePaused  = "pausiert"
	StateOff     = "aus"
)

// Woran HostDeck den Spielserver erkannt hat.
const (
	ByImage = "image"
	ByFiles = "dateien"
)

// candidate ist ein Container oder Ordner, der ein Spielserver sein kann.
type candidate struct {
	id        string // Container-Name, bei gestoppten Panel-Servern der Ordner
	container string
	project   string
	panel     string // Container des Panels, das ihn anlegt
	state     string
	kind      string // Art des Spiels, leer: unbekannt
	game      string // aus Image oder Dateien
	found     string // ByImage, ByFiles; leer: noch nicht erkannt
	files     files
}

// files ist, was HostDeck aus dem Datenordner liest: nur Namen und wenige
// Schlüssel, nie Passwörter.
type files struct {
	kind   string
	game   string
	name   string // motd, server-name, hostname
	level  string // Welt
	folder string // Ordner des Source-Spiels
	proxy  bool   // Minecraft-Proxy (Velocity, BungeeCord): kein Spielserver
}

// serverDirName: Panels wie Wings benennen die Ordner ihrer Server nach
// einer UUID (wie in discover).
var serverDirName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// reader liest Dateien des Hosts: nur reguläre Dateien, keine
// symbolischen Verknüpfungen, höchstens limit Byte.
type reader struct{ fs hostfs.FS }

func (r reader) regular(p string) bool {
	fi, err := r.fs.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

// read liest rel unter dem Datenordner dir, ohne Verknüpfungen darunter
// (readBeneath).
func (r reader) read(dir, rel string, limit int64) ([]byte, bool) {
	b, err := r.readBeneath(dir, rel, limit)
	return b, err == nil
}

// subdirs: höchstens n Unterordner von p (ohne Verknüpfungen).
func (r reader) subdirs(p string, n int) []string {
	f, err := os.Open(r.fs.Real(p))
	if err != nil {
		return nil
	}
	defer f.Close()
	es, err := f.ReadDir(256)
	if err != nil && err != io.EOF {
		return nil
	}
	var out []string
	for _, e := range es {
		if e.IsDir() && len(out) < n {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// properties liest key=value-Zeilen und behält nur die gefragten Schlüssel.
func properties(b []byte, keep ...string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		for _, want := range keep {
			if k == want {
				out[k] = unescape(strings.TrimSpace(v))
			}
		}
	}
	return out
}

// unescape löst Escapes von Java-Properties auf (ä, \n, \:).
func unescape(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '\\' || i+1 == len(v) {
			b.WriteByte(c)
			continue
		}
		i++
		switch v[i] {
		case 'u':
			if i+4 < len(v) {
				if n, err := strconv.ParseUint(v[i+1:i+5], 16, 32); err == nil {
					b.WriteRune(rune(n))
					i += 4
					continue
				}
			}
			b.WriteByte('u')
		case 'n', 'r', 't':
			b.WriteByte(' ')
		default:
			b.WriteByte(v[i])
		}
	}
	return b.String()
}

var cfgHostname = regexp.MustCompile(`(?m)^\s*hostname\s+"?([^"\r\n]*)"?`)

// readFiles erkennt ein Spiel an Dateien im Ordner dir.
func (r reader) readFiles(dir string) files {
	if b, ok := r.read(dir, "server.properties", 256<<10); ok {
		p := properties(b, "motd", "level-name", "server-name")
		f := files{kind: KindJava, game: "Minecraft Java", name: p["motd"], level: p["level-name"]}
		if r.regular(dir + "/bedrock_server") {
			f.kind, f.game, f.name = KindBedrock, "Minecraft Bedrock", p["server-name"]
		}
		return f
	}
	// Ein Proxy vor Minecraft-Servern (ohne server.properties) ist kein
	// Spielserver, sondern leitet nur weiter.
	if r.regular(dir + "/velocity.toml") {
		return files{proxy: true}
	}
	if b, ok := r.read(dir, "config.yml", 64<<10); ok && bytes.Contains(b, []byte("listeners:")) && bytes.Contains(b, []byte("servers:")) {
		return files{proxy: true}
	}
	srcds := r.regular(dir+"/srcds_run") || r.regular(dir+"/srcds_linux")
	for _, sub := range r.subdirs(dir, 64) {
		if !r.regular(dir + "/" + sub + "/gameinfo.txt") {
			continue
		}
		f := files{kind: KindSource, folder: sub, game: sourceFolders[strings.ToLower(sub)]}
		if f.game == "" {
			f.game = "Source-Spiel (" + sub + ")"
		}
		if b, ok := r.read(dir, sub+"/cfg/server.cfg", 64<<10); ok {
			if m := cfgHostname.FindSubmatch(b); m != nil {
				f.name = string(m[1])
			}
		}
		return f
	}
	if srcds {
		return files{kind: KindSource, game: "Source-Spiel"}
	}
	return files{}
}

// Datenordner, die ein Spiel-Image oft nutzt; zuerst gelesen.
var dataDests = []string{"/data", "/home/container", "/server", "/minecraft"}

// dataDirs: beschreibbare Ordner eines Containers, in denen ein Spiel
// liegen kann, bevorzugte zuerst, höchstens vier. Systempfade und der
// Docker-Socket nie.
func dataDirs(c dockerapi.OpsContainer) []string {
	type d struct {
		src  string
		rank int
	}
	var ds []d
	for _, m := range c.Mounts {
		if !m.RW || m.Source == "" || (m.Type != "bind" && m.Type != "volume") {
			continue
		}
		src := hostfs.Clean(m.Source)
		if m.Type == "bind" && discover.IsSystemPath(src) {
			continue
		}
		rank := len(dataDests)
		for i, p := range dataDests {
			if hostfs.Clean(m.Destination) == p {
				rank = i
			}
		}
		ds = append(ds, d{src, rank})
	}
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].rank < ds[j].rank })
	var out []string
	for _, x := range ds {
		if len(out) < 4 {
			out = append(out, x.src)
		}
	}
	return out
}

func stateOf(s string) string {
	switch s {
	case "running":
		return StateRunning
	case "restarting":
		return StateStart
	case "paused":
		return StatePaused
	}
	return StateOff
}

// managers: Container, die andere anlegen (Docker-Socket und ein Ordner am
// selben Pfad, Regel ManagerOf wie Bestandsaufnahme und Betrieb).
func managers(list []dockerapi.OpsContainer) (all []inventory.Container, managedBy map[string]string, roots map[string][]string) {
	all = make([]inventory.Container, len(list))
	for i, c := range list {
		ic := inventory.Container{Name: c.Name()}
		for _, m := range c.Mounts {
			src := m.Source
			if m.Type == "bind" {
				src = hostfs.Clean(src)
				if discover.IsDockerSocket(src) {
					ic.DockerSocket = true
				}
			}
			ic.Mounts = append(ic.Mounts, inventory.Mount{Type: m.Type, Source: src, Destination: m.Destination, RW: m.RW})
		}
		all[i] = ic
	}
	managedBy = map[string]string{}
	roots = map[string][]string{}
	for i, c := range list {
		if all[i].DockerSocket {
			for _, m := range all[i].Mounts {
				if m.Type == "bind" && m.Source == hostfs.Clean(m.Destination) && m.Source != "/" && !discover.IsSystemPath(m.Source) {
					roots[all[i].Name] = append(roots[all[i].Name], m.Source)
				}
			}
			continue
		}
		if c.Labels["com.docker.compose.project"] != "" && !strings.EqualFold(c.Labels["com.docker.compose.oneoff"], "true") {
			continue
		}
		if j, _ := discover.ManagerOf(all[i], all); j >= 0 {
			managedBy[all[i].Name] = all[j].Name
		}
	}
	return all, managedBy, roots
}

// fileCache hält Funde aus Dateien vom letzten Durchlauf; was dieser nicht
// mehr braucht, fällt weg.
type fileCache struct{ old, used map[string]files }

func (c *fileCache) get(key string, read func() files) files {
	if c.used == nil {
		c.used = map[string]files{}
	}
	if f, ok := c.used[key]; ok {
		return f
	}
	f, ok := c.old[key]
	if !ok {
		f = read()
	}
	c.used[key] = f
	return f
}

// candidateOf prüft einen Container: Router, Panels und Container mit dem
// Docker-Socket sind nie Spielserver; sonst zählt er, wenn ein Panel ihn
// anlegt oder Image oder Dateien auf ein Spiel deuten.
func (r reader) candidateOf(c dockerapi.OpsContainer, managedBy map[string]string, roots map[string][]string, cache *fileCache) (candidate, bool) {
	name := c.Name()
	if isRouter(c.Image) || len(roots[name]) > 0 {
		return candidate{}, false
	}
	for _, m := range c.Mounts {
		if discover.IsDockerSocket(hostfs.Clean(m.Source)) {
			return candidate{}, false
		}
	}
	cd := candidate{id: name, container: name, project: c.Labels["com.docker.compose.project"], panel: managedBy[name], state: stateOf(c.State)}
	im, isKnown := knownImage(c.Image)
	f := cache.get(c.ID+"|"+c.State, func() files {
		for _, d := range dataDirs(c) {
			if f := r.readFiles(d); f.kind != "" || f.proxy {
				return f
			}
		}
		return files{}
	})
	if f.proxy {
		return candidate{}, false
	}
	cd.files = f
	switch {
	case f.kind != "":
		cd.kind, cd.game, cd.found = f.kind, f.game, ByFiles
	case isKnown:
		cd.kind = im.kind
		if im.sure {
			cd.game, cd.found = im.game, ByImage
			if cd.game == "" {
				cd.game = "Steam-Spiel"
			}
		}
	}
	if cd.panel == "" && cd.kind == "" {
		return candidate{}, false
	}
	return cd, true
}

// detect findet Kandidaten in der Liste der Container und, für gestoppte
// Panel-Server ohne Container, in den Ordnern der Panels.
// cache: Funde aus Dateien je Container-ID und Zustand, damit HostDeck Dateien
// nur nach einer Änderung neu liest.
func detect(list []dockerapi.OpsContainer, r reader, cache *fileCache) []candidate {
	_, managedBy, roots := managers(list)
	var out []candidate
	known := map[string]bool{}
	places := map[string]string{} // Ordner mit Server-Ordnern → Panel
	for panel, rs := range roots {
		for _, root := range rs {
			places[root+"/volumes"] = panel
		}
	}
	for _, c := range list {
		known[c.Name()] = true
		cd, ok := r.candidateOf(c, managedBy, roots, cache)
		if !ok {
			continue
		}
		if cd.panel != "" {
			// Neben dem Ordner eines Panel-Servers liegen oft die übrigen.
			for _, d := range dataDirs(c) {
				if serverDirName.MatchString(path.Base(d)) {
					places[path.Dir(d)] = cd.panel
				}
			}
		}
		out = append(out, cd)
	}
	// Panel-Server, deren Container fehlt (Panels löschen ihn beim Stoppen
	// oft): nur, wenn die Dateien ein Spiel zeigen.
	dirs := make([]string, 0, len(places))
	for d := range places {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		for _, sub := range r.subdirs(d, 256) {
			if !serverDirName.MatchString(sub) || known[sub] {
				continue
			}
			known[sub] = true
			f := cache.get("dir|"+d+"/"+sub, func() files { return r.readFiles(d + "/" + sub) })
			if f.kind == "" {
				continue
			}
			out = append(out, candidate{id: sub, panel: places[d], state: StateOff, kind: f.kind, game: f.game, found: ByFiles, files: f})
		}
	}
	return out
}
