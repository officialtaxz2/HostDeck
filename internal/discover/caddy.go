package discover

import (
	"context"
	"encoding/json"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/ort"
)

// caddy liest die Routen aus der Konfiguration, die Caddy wirklich lädt
// (caddy adapt löst alle Imports auf), und prüft, wohin sie zeigen.
func (s *scan) caddy(ctx context.Context) {
	cfg := hostfs.Clean(s.o.CaddyConfig)
	c := &inventory.Caddy{Config: cfg, Imports: []inventory.CaddyImport{}, Files: []string{}, Routes: []inventory.Route{}}
	if !s.d.FS.Exists(cfg) {
		s.inv.Caddy = c
		return
	}
	c.Found = true
	s.inv.Caddy = c
	if ents, err := s.d.FS.ReadDir(path.Dir(cfg)); err == nil {
		for _, de := range ents {
			if !de.IsDir() {
				c.Files = append(c.Files, path.Join(path.Dir(cfg), de.Name()))
			}
		}
	}
	s.caddyImports(c, cfg, path.Dir(cfg), map[string]bool{}, 0)
	c.DataDir, c.UID, c.GID = s.caddyUser()

	if s.d.Caddy == nil {
		return
	}
	out, err := s.d.Caddy.Adapt(ctx, cfg)
	if err != nil {
		c.Error = err.Error()
		s.addFinding(inventory.LevelWarn, "caddy-adapt", "host:caddy", "caddy adapt ist fehlgeschlagen: "+err.Error()+". Die Caddy-Routen fehlen im Bericht.")
		return
	}
	routes, err := parseCaddyRoutes(out)
	if err != nil {
		c.Error = err.Error()
		s.addFinding(inventory.LevelWarn, "caddy-adapt", "host:caddy", "Die Ausgabe von caddy adapt ist unlesbar: "+err.Error())
		return
	}
	routes = withoutOwnRoutes(routes)
	for i := range routes {
		s.resolveRoute(&routes[i])
	}
	c.Routes = routes
	for _, r := range routes {
		if r.Target == "" || r.Status != inventory.RouteOK {
			continue
		}
		ct := s.inv.Container(r.Target)
		if ct == nil || ct.Project == "" {
			continue
		}
		if p := s.inv.Project(ct.Project); p != nil {
			for _, h := range r.Hosts {
				p.Domains = appendUnique(p.Domains, h)
			}
		}
	}
}

// caddyUser liest den Benutzer caddy, den das Paket anlegt: seine
// Nummern und den Datenordner von Caddy unter seinem Home
// (.local/share/caddy, leer wenn es ihn nicht gibt).
func (s *scan) caddyUser() (dataDir string, uid, gid int) {
	b, ok := s.readRegular("/etc/passwd", 4<<20)
	if !ok {
		return "", 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 || f[0] != "caddy" {
			continue
		}
		uid, _ = strconv.Atoi(f[2])
		gid, _ = strconv.Atoi(f[3])
		if path.IsAbs(f[5]) {
			d := path.Join(f[5], ".local/share/caddy")
			if fi, err := s.d.FS.Lstat(d); err == nil && fi.IsDir() {
				dataDir = d
			}
		}
		return dataDir, uid, gid
	}
	return "", 0, 0
}

// caddyImports sucht import-Zeilen mit Dateipfaden. Snippet-Importe wie
// „import logging“ werden übergangen.
func (s *scan) caddyImports(c *inventory.Caddy, file, baseDir string, seen map[string]bool, depth int) {
	if depth > 5 || seen[file] {
		return
	}
	seen[file] = true
	b, err := s.d.FS.ReadFile(file, 1<<20)
	if err != nil {
		return
	}
	snippets := map[string]bool{}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "(") {
			if i := strings.Index(t, ")"); i > 1 {
				snippets[t[1:i]] = true
			}
		}
	}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if i := strings.Index(t, "#"); i >= 0 {
			t = strings.TrimSpace(t[:i])
		}
		f := strings.Fields(t)
		if len(f) < 2 || f[0] != "import" || snippets[f[1]] {
			continue
		}
		arg := f[1]
		if !strings.ContainsAny(arg, "/*.") {
			continue // Snippet aus einer anderen Datei
		}
		p := arg
		if !path.IsAbs(p) {
			p = path.Join(path.Dir(file), p)
		}
		p = hostfs.Clean(p)
		var targets []string
		if strings.ContainsAny(p, "*?[") {
			targets, _ = s.d.FS.Glob(p)
			if len(targets) == 0 {
				continue // leerer Glob ist für Caddy in Ordnung
			}
		} else {
			targets = []string{p}
		}
		for _, t := range targets {
			imp := inventory.CaddyImport{Path: t, From: file, Exists: s.d.FS.Exists(t), Outside: !hostfs.Under(t, baseDir)}
			if e := s.innermostOwn(t); e != nil && e.Kind == inventory.KindProject {
				imp.Project = e.Owner
			}
			c.Imports = append(c.Imports, imp)
			if imp.Exists {
				s.caddyImports(c, t, baseDir, seen, depth+1)
			}
		}
	}
}

func (s *scan) innermostOwn(p string) *inventory.Path {
	var best *inventory.Path
	for i := range s.inv.Paths {
		e := &s.inv.Paths[i]
		if e.Kind == inventory.KindParent || e.CoveredBy != "" || !hostfs.Under(p, e.Path) {
			continue
		}
		if best == nil || len(e.Path) > len(best.Path) {
			best = e
		}
	}
	return best
}

type caddyConfig struct {
	Apps struct {
		HTTP struct {
			Servers map[string]struct {
				Routes []caddyRoute `json:"routes"`
			} `json:"servers"`
		} `json:"http"`
	} `json:"apps"`
}

type caddyRoute struct {
	Match []struct {
		Host []string `json:"host"`
	} `json:"match"`
	Handle []caddyHandler `json:"handle"`
}

type caddyHandler struct {
	Handler   string       `json:"handler"`
	Routes    []caddyRoute `json:"routes"`
	Upstreams []struct {
		Dial string `json:"dial"`
	} `json:"upstreams"`
}

// parseCaddyRoutes zieht aus dem JSON von caddy adapt nur Hosts und Ziele.
// Alles andere, auch eventuelle Zugangsdaten, wird verworfen.
func parseCaddyRoutes(b []byte) ([]inventory.Route, error) {
	var cfg caddyConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	var out []inventory.Route
	seen := map[string]bool{}
	add := func(r inventory.Route) {
		id := strings.Join(r.Hosts, ",") + "|" + r.Handler + "|" + r.Upstream
		if !seen[id] {
			seen[id] = true
			out = append(out, r)
		}
	}
	var walk func(routes []caddyRoute, hosts []string)
	walk = func(routes []caddyRoute, hosts []string) {
		for _, rt := range routes {
			h := hosts
			var mh []string
			for _, m := range rt.Match {
				mh = append(mh, m.Host...)
			}
			if len(mh) > 0 {
				h = mh
			}
			for _, hd := range rt.Handle {
				switch hd.Handler {
				case "subroute":
					walk(hd.Routes, h)
				case "reverse_proxy":
					for _, u := range hd.Upstreams {
						add(inventory.Route{Hosts: hostsOrAll(h), Handler: hd.Handler, Upstream: u.Dial})
					}
				case "file_server", "static_response":
					add(inventory.Route{Hosts: hostsOrAll(h), Handler: hd.Handler, Status: inventory.RouteNoProxy})
				}
			}
		}
	}
	names := make([]string, 0, len(cfg.Apps.HTTP.Servers))
	for n := range cfg.Apps.HTTP.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		walk(cfg.Apps.HTTP.Servers[n].Routes, nil)
	}
	return out, nil
}

// withoutOwnRoutes lässt den Eintrag des Leitstands weg: Er gehört zu HostDeck
// selbst, nicht zu den Aufgaben des Servers.
func withoutOwnRoutes(routes []inventory.Route) []inventory.Route {
	var kept []inventory.Route
	for _, r := range routes {
		if r.Upstream != "unix/"+ort.CaddySocket {
			kept = append(kept, r)
		}
	}
	return kept
}

func hostsOrAll(h []string) []string {
	if len(h) == 0 {
		return []string{"*"}
	}
	out := append([]string{}, h...)
	sort.Strings(out)
	return out
}

// byDockerName sucht den Container, den ein Name im Docker-Netz meint: den
// Container-Namen selbst oder den einzigen Compose-Dienst dieses Namens.
func (s *scan) byDockerName(name string) *inventory.Container {
	var hit *inventory.Container
	n := 0
	for i := range s.inv.Containers {
		c := &s.inv.Containers[i]
		if c.Name == name {
			return c
		}
		if c.Service == name {
			hit = c
			n++
		}
	}
	if n == 1 {
		return hit
	}
	return nil
}

// resolveRoute prüft, ob hinter einem Proxy-Ziel etwas lauscht.
func (s *scan) resolveRoute(r *inventory.Route) {
	if r.Handler != "reverse_proxy" {
		return
	}
	host, portStr, err := net.SplitHostPort(r.Upstream)
	port, perr := strconv.Atoi(portStr)
	if err != nil || perr != nil || strings.Contains(r.Upstream, "{") {
		r.Status = inventory.RouteExternal
		return
	}
	r.Port = port
	if !isLoopback(host) && !isWildcard(host) {
		// Caddy im Container: Das Ziel ist ein Docker-Name oder der Name
		// eines Compose-Dienstes im selben Netz.
		// Heißen mehrere Dienste so, bleibt das Ziel offen.
		if net.ParseIP(host) == nil {
			if c := s.byDockerName(host); c != nil {
				r.Target, r.Status = c.Key, inventory.RouteOK
				if !c.Running {
					r.Status = inventory.RouteStopped
				}
				return
			}
		}
		r.Status = inventory.RouteExternal
		return
	}
	var stopped string
	for _, c := range s.inv.Containers {
		for _, p := range c.Ports {
			if p.Proto != "tcp" || p.HostPort != port || !(isLoopback(p.HostIP) || isWildcard(p.HostIP)) {
				continue
			}
			if c.Running {
				r.Target, r.Status = c.Key, inventory.RouteOK
				return
			}
			if stopped == "" {
				stopped = c.Key
			}
		}
	}
	for _, l := range s.inv.Listeners {
		if l.Proto == "tcp" && l.Port == port && (isLoopback(l.Addr) || isWildcard(l.Addr)) {
			r.Status = inventory.RouteOK
			switch {
			case l.Container != "":
				r.Target = l.Container
			case l.Process != "":
				r.Target = "host:" + l.Process
			default:
				r.Target = "host"
			}
			return
		}
	}
	if stopped != "" {
		r.Target, r.Status = stopped, inventory.RouteStopped
		return
	}
	r.Status = inventory.RouteNothing
}
