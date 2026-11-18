package discover

import (
	"context"
	"path"
	"sort"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// systemPrefixes werden nie gesichert, auch wenn ein Container sie einbindet.
// Sie stehen im Manifest nur zur Information unter „system“.
var systemPrefixes = []string{
	"/proc", "/sys", "/dev", "/run", "/var/run",
	"/var/lib/docker", "/var/lib/containerd", "/lib/modules", "/boot",
	"/etc/localtime", "/etc/timezone", "/etc/machine-id", "/etc/hostname",
	"/etc/hosts", "/etc/resolv.conf",
}

// IsSystemPath meldet Pfade des Systems (/proc, /var/lib/docker, …), die
// nie Daten eines Projekts sind.
func IsSystemPath(p string) bool { return isSystemPath(p) }

func isDockerSocket(p string) bool {
	b := path.Base(p)
	return b == "docker.sock" || b == "containerd.sock"
}

func isSystemPath(p string) bool {
	p = hostfs.Clean(p)
	if p == "/" || isDockerSocket(p) {
		return true
	}
	for _, sp := range systemPrefixes {
		if hostfs.Under(p, sp) {
			return true
		}
	}
	return false
}

func (s *scan) isSystem(p string) bool {
	if isSystemPath(p) {
		return true
	}
	if rd := s.inv.Docker.RootDir; rd != "" && hostfs.Under(hostfs.Clean(p), hostfs.Clean(rd)) {
		return true
	}
	return false
}

type bindClass struct {
	kind      string
	coveredBy string
	owner     string
}

// paths ordnet jeden eingebundenen Host-Pfad genau einem Eintrag zu. Regeln
// stehen in docs/manifest-v1.md, Abschnitt „Regeln für Pfade“.
func (s *scan) paths() {
	entries := map[string]*inventory.Path{}
	newEntry := func(p, kind string) *inventory.Path {
		e := &inventory.Path{
			Key:      "path:" + p,
			Path:     p,
			Kind:     kind,
			Mounts:   []inventory.MountRef{},
			Excludes: []string{},
			Size:     inventory.Unknown,
		}
		if fi, err := s.d.FS.Stat(p); err == nil {
			e.Exists = true
			e.IsDir = fi.IsDir()
		}
		entries[p] = e
		return e
	}

	// Projektordner, innerster zuerst.
	type pdir struct{ dir, key string }
	var pdirs []pdir
	for _, p := range s.inv.Projects {
		if p.Path == "" {
			continue
		}
		if e := entries[p.Path]; e != nil {
			s.addFinding(inventory.LevelWarn, "ordner-geteilt", p.Key, "Die Projekte "+strings.TrimPrefix(e.Owner, "project:")+" und "+p.Name+" liegen im selben Ordner "+p.Path+"; er wird nur einmal gesichert, Eigentümer ist "+strings.TrimPrefix(e.Owner, "project:")+".")
			continue
		}
		e := newEntry(p.Path, inventory.KindProject)
		e.Owner = p.Key
		pdirs = append(pdirs, pdir{p.Path, p.Key})
	}
	sort.Slice(pdirs, func(i, j int) bool { return len(pdirs[i].dir) > len(pdirs[j].dir) })
	innermost := func(src string) (string, string) {
		for _, pd := range pdirs {
			if hostfs.Under(src, pd.dir) {
				return pd.dir, pd.key
			}
		}
		return "", ""
	}
	projectsUnder := func(src string) []string {
		var out []string
		for _, pd := range pdirs {
			if hostfs.StrictlyUnder(pd.dir, src) {
				out = append(out, pd.key)
			}
		}
		return out
	}

	// Durchgang 1: jeden Bind-Mount einordnen.
	classes := map[string]bindClass{}
	rank := map[string]int{inventory.KindConfig: 1, inventory.KindData: 2, inventory.KindParent: 3}
	for ci := range s.inv.Containers {
		c := &s.inv.Containers[ci]
		owner := c.Project
		if owner == "" {
			owner = c.Key
		}
		for mi := range c.Mounts {
			m := &c.Mounts[mi]
			if m.Type != "bind" {
				continue
			}
			src := m.Source
			if s.isSystem(src) {
				m.System = true
				s.inv.System = append(s.inv.System, inventory.SystemMount{Container: c.Key, Source: src, Destination: m.Destination})
				continue
			}
			if c.ManagedBy != "" && hostfs.Under(src, c.ManagerPath) {
				continue
			}
			if _, isProject := entries[src]; isProject && entries[src].Kind == inventory.KindProject {
				continue
			}
			var cl bindClass
			if under := projectsUnder(src); len(under) > 0 {
				cl = bindClass{kind: inventory.KindParent}
			} else {
				fi, err := s.d.FS.Stat(src)
				isDir := err != nil || fi.IsDir() // fehlende Quellen legt Docker als Ordner an
				dir, _ := innermost(src)
				switch {
				case dir != "" && isDir && (m.RW || s.isDBMount(c, m)):
					cl = bindClass{kind: inventory.KindData, owner: owner}
				case dir != "":
					cl = bindClass{kind: inventory.KindConfig, coveredBy: "path:" + dir, owner: owner}
				case isDir:
					cl = bindClass{kind: inventory.KindData, owner: owner}
				default:
					cl = bindClass{kind: inventory.KindConfig, owner: owner}
				}
			}
			prev, seen := classes[src]
			if !seen {
				classes[src] = cl
				continue
			}
			if prev.owner != "" && cl.owner != "" && prev.owner != cl.owner {
				s.addFinding(inventory.LevelWarn, "pfad-geteilt", "path:"+src, "Der Ordner "+src+" wird von "+short(prev.owner)+" und "+short(cl.owner)+" genutzt; Eigentümer ist "+short(prev.owner)+".")
				cl.owner = prev.owner
			}
			if rank[cl.kind] > rank[prev.kind] || (cl.kind == prev.kind && cl.coveredBy == "" && prev.coveredBy != "") {
				classes[src] = cl
			}
		}
	}
	for src, cl := range classes {
		e := newEntry(src, cl.kind)
		e.Owner = cl.owner
		e.CoveredBy = cl.coveredBy
		if cl.kind == inventory.KindParent {
			e.Contains = projectsUnder(src)
		}
	}

	// Durchgang 2: Mounts ihren Einträgen zuordnen.
	for ci := range s.inv.Containers {
		c := &s.inv.Containers[ci]
		for mi := range c.Mounts {
			m := &c.Mounts[mi]
			if m.Type != "bind" || m.System {
				continue
			}
			ref := inventory.MountRef{Container: c.Key, Destination: m.Destination, RW: m.RW}
			src := m.Source
			if c.ManagedBy != "" && hostfs.Under(src, c.ManagerPath) {
				src = c.ManagerPath
			}
			e := entries[src]
			if e == nil {
				continue
			}
			e.Mounts = append(e.Mounts, ref)
			switch {
			case e.Kind == inventory.KindParent:
			case e.CoveredBy != "":
				m.DataKey = e.CoveredBy
			default:
				m.DataKey = e.Key
			}
			if !e.Exists {
				s.addFinding(inventory.LevelWarn, "quelle-fehlt", e.Key, "Der eingebundene Pfad "+src+" von "+short(c.Key)+" existiert nicht. Docker legt ihn beim nächsten Start leer an.")
			}
		}
	}

	// Der genaueste Pfad gewinnt: Einträge mit eigener Strategie werden aus
	// dem nächsten umgebenden Eintrag herausgeschnitten.
	own := func(e *inventory.Path) bool { return e.Kind != inventory.KindParent && e.CoveredBy == "" }
	for _, e := range entries {
		if !own(e) {
			continue
		}
		var best *inventory.Path
		for _, a := range entries {
			if a == e || !own(a) || !hostfs.StrictlyUnder(e.Path, a.Path) {
				continue
			}
			if best == nil || len(a.Path) > len(best.Path) {
				best = a
			}
		}
		if best != nil {
			best.Excludes = append(best.Excludes, e.Key)
		}
	}
	for _, e := range entries {
		s.inv.Paths = append(s.inv.Paths, *e)
	}
	sort.Slice(s.inv.Paths, func(i, j int) bool { return s.inv.Paths[i].Path < s.inv.Paths[j].Path })
}

// short kürzt einen Schlüssel für Meldungen.
func short(key string) string {
	for _, p := range []string{"project:", "compose:", "container:", "managed:"} {
		if strings.HasPrefix(key, p) {
			return strings.TrimPrefix(key, p)
		}
	}
	return key
}

// ignoredScanDirs tauchen nie als „nicht zugeordnet“ auf.
var ignoredScanDirs = map[string]bool{
	"/opt/containerd": true,
	"lost+found":      true,
}

// unclaimed sucht in den Scan-Ordnern nach Ordnern, die keinem Eintrag
// gehören, etwa alte Projekte ohne laufende Container.
func (s *scan) unclaimed(ctx context.Context) {
	covered := func(p string) bool {
		for _, e := range s.inv.Paths {
			if e.Kind != inventory.KindParent && e.CoveredBy == "" && hostfs.Under(p, e.Path) {
				return true
			}
		}
		return false
	}
	leadsTo := func(p string) bool {
		for _, e := range s.inv.Paths {
			if hostfs.StrictlyUnder(e.Path, p) || (e.Kind == inventory.KindParent && e.Path == p) {
				return true
			}
		}
		for _, c := range s.inv.Containers {
			if c.ManagerPath != "" && hostfs.StrictlyUnder(c.ManagerPath, p) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		ents, err := s.d.FS.ReadDir(dir)
		if err != nil {
			return
		}
		for _, de := range ents {
			p := path.Join(dir, de.Name())
			if !de.IsDir() || ignoredScanDirs[p] || ignoredScanDirs[de.Name()] || seen[p] {
				continue
			}
			seen[p] = true
			switch {
			case covered(p):
			case leadsTo(p) && depth < 4:
				walk(p, depth+1)
			case leadsTo(p):
			default:
				u := inventory.Unclaimed{Key: "path:" + p, Path: p, Size: inventory.Unknown}
				if s.d.Git != nil && s.d.FS.Exists(path.Join(p, ".git")) {
					if g, err := s.d.Git.Inspect(ctx, p); err == nil {
						u.Git = g
					}
				}
				s.inv.Unclaimed = append(s.inv.Unclaimed, u)
			}
		}
	}
	for _, root := range s.o.ScanRoots {
		root = hostfs.Clean(root)
		if !s.d.FS.IsDir(root) || covered(root) {
			continue
		}
		walk(root, 0)
	}
}
