package discover

import (
	"path"
	"regexp"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// engines ordnet Image-Namen einer Datenbank zu.
var engines = map[string]string{
	"postgres":           "postgres",
	"postgis":            "postgres",
	"pgautoupgrade":      "postgres",
	"timescaledb":        "postgres",
	"mysql":              "mysql",
	"mariadb":            "mariadb",
	"mongo":              "mongo",
	"redis":              "redis",
	"redis-stack-server": "redis",
	"valkey":             "valkey",
}

// dataDirs sind die üblichen Datenordner im Container.
var dataDirs = map[string]string{
	"postgres": "/var/lib/postgresql",
	"mysql":    "/var/lib/mysql",
	"mariadb":  "/var/lib/mysql",
	"mongo":    "/data/db",
	"redis":    "/data",
	"valkey":   "/data",
}

// IsDumpEngine meldet, ob für die Engine ein Dump möglich ist.
func IsDumpEngine(engine string) bool {
	switch engine {
	case "postgres", "mysql", "mariadb", "mongo":
		return true
	}
	return false
}

var versionRe = regexp.MustCompile(`^(\d+)(?:\.(\d+))?`)

// engineFromImage erkennt Engine und Hauptversion am Image-Namen, etwa
// postgres:17-alpine -> postgres 17, mysql:8.4 -> mysql 8.4.
func engineFromImage(ref string) (engine, version string) {
	ref, _, _ = strings.Cut(ref, "@")
	if strings.HasPrefix(ref, "sha256:") {
		return "", ""
	}
	repo, tag := ref, ""
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		repo, tag = ref[:i], ref[i+1:]
	}
	engine = engines[path.Base(repo)]
	if engine == "" {
		return "", ""
	}
	if m := versionRe.FindStringSubmatch(tag); m != nil {
		version = m[1]
		if (engine == "mysql" || engine == "mariadb") && m[2] != "" {
			version += "." + m[2]
		}
	}
	return engine, version
}

func (s *scan) dbFromImages() {
	for i := range s.inv.Containers {
		c := &s.inv.Containers[i]
		if e, v := engineFromImage(c.Image); e != "" {
			c.DB = &inventory.DB{Engine: e, Version: v, Via: "image"}
		}
	}
}

func (s *scan) isDBMount(c *inventory.Container, m *inventory.Mount) bool {
	return c.DB != nil && dataMount(c) == m
}

// dataMount sucht den Mount, unter dem die Datenbank ihre Daten ablegt.
func dataMount(c *inventory.Container) *inventory.Mount {
	if c.DB == nil {
		return nil
	}
	dir := dataDirs[c.DB.Engine]
	var best *inventory.Mount
	for i := range c.Mounts {
		m := &c.Mounts[i]
		if m.Type != "bind" && m.Type != "volume" {
			continue
		}
		d := hostfs.Clean(m.Destination)
		if hostfs.Under(d, dir) || hostfs.StrictlyUnder(dir, d) {
			if best == nil || len(d) > len(best.Destination) {
				best = m
			}
		}
	}
	return best
}

// attachDatabases hängt die Datenbank-Erkennung an den Eintrag, der ihre
// Daten sichert, und liest bei PostgreSQL die echte Version aus PG_VERSION.
func (s *scan) attachDatabases() {
	for i := range s.inv.Containers {
		c := &s.inv.Containers[i]
		m := dataMount(c)
		if c.DB == nil {
			continue
		}
		if m == nil {
			if c.DB.Engine != "redis" && c.DB.Engine != "valkey" {
				s.addFinding(inventory.LevelWarn, "db-ohne-volume", c.Key, "Die Datenbank in "+short(c.Key)+" hat keinen eingebundenen Datenordner. Ihre Daten liegen im Container und gehen beim Neuanlegen verloren.")
			}
			continue
		}
		c.DB.DataKey = m.DataKey
		hostDir := m.Source
		if m.Type == "volume" {
			if v := s.inv.Volume(m.DataKey); v != nil {
				hostDir = v.Mountpoint
			}
		}
		if c.DB.Engine == "postgres" {
			if v := s.pgVersion(hostDir); v != "" {
				c.DB.Version = v
			}
		}
		db := *c.DB
		db.DataKey = ""
		switch {
		case strings.HasPrefix(m.DataKey, "path:"):
			if e := s.inv.Path(m.DataKey); e != nil && e.Kind != inventory.KindProject {
				e.DB = &db
			}
		default:
			if v := s.inv.Volume(m.DataKey); v != nil {
				v.DB = &db
			}
		}
	}
}

// pgVersion liest PG_VERSION im Datenordner. Ab PostgreSQL 18 liegt er eine
// Ebene tiefer (/var/lib/postgresql/18/docker).
func (s *scan) pgVersion(dir string) string {
	db := s.detectEngine(dir)
	if db != nil && db.Engine == "postgres" {
		return db.Version
	}
	return ""
}

// detectEngine erkennt eine Datenbank an typischen Dateien, höchstens drei
// Ebenen tief. Gelesen wird nur PG_VERSION.
func (s *scan) detectEngine(dir string) *inventory.DB {
	type item struct {
		dir   string
		depth int
	}
	queue := []item{{dir, 0}}
	visited := 0
	for len(queue) > 0 && visited < 500 {
		it := queue[0]
		queue = queue[1:]
		ents, err := s.d.FS.ReadDir(it.dir)
		if err != nil {
			continue
		}
		names := map[string]bool{}
		for _, de := range ents {
			visited++
			names[de.Name()] = true
		}
		switch {
		case names["PG_VERSION"]:
			b, _ := s.d.FS.ReadFile(path.Join(it.dir, "PG_VERSION"), 32)
			v := strings.TrimSpace(string(b))
			if m := versionRe.FindStringSubmatch(v); m != nil && m[1] != "9" {
				v = m[1]
			}
			return &inventory.DB{Engine: "postgres", Version: v, Via: "dateien"}
		case names["aria_log_control"] && (names["ibdata1"] || names["mysql"]):
			return &inventory.DB{Engine: "mariadb", Via: "dateien"}
		case names["mysql.ibd"] || names["ibdata1"]:
			return &inventory.DB{Engine: "mysql", Via: "dateien"}
		case names["WiredTiger"]:
			return &inventory.DB{Engine: "mongo", Via: "dateien"}
		case names["dump.rdb"] || names["appendonly.aof"] || names["appendonlydir"]:
			return &inventory.DB{Engine: "redis", Via: "dateien"}
		}
		if it.depth < 3 {
			for _, de := range ents {
				if de.IsDir() {
					queue = append(queue, item{path.Join(it.dir, de.Name()), it.depth + 1})
				}
			}
		}
	}
	return nil
}

var sqliteMagic = []byte("SQLite format 3\x00")

// skipDirs werden bei der SQLite-Suche übersprungen.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, ".cache": true}

// findSQLite sucht SQLite-Dateien, höchstens vier Ebenen tief und mit
// begrenzter Anzahl geprüfter Einträge. Gelesen werden nur die ersten 16 Byte.
func (s *scan) findSQLite(dir string, exclude []string) []string {
	ex := map[string]bool{}
	for _, e := range exclude {
		ex[strings.TrimPrefix(e, "path:")] = true
	}
	type item struct {
		dir   string
		depth int
	}
	var found []string
	queue := []item{{dir, 0}}
	visited := 0
	for len(queue) > 0 && visited < 3000 && len(found) < 10 {
		it := queue[0]
		queue = queue[1:]
		ents, err := s.d.FS.ReadDir(it.dir)
		if err != nil {
			continue
		}
		for _, de := range ents {
			visited++
			p := path.Join(it.dir, de.Name())
			if de.IsDir() {
				if it.depth < 4 && !skipDirs[de.Name()] && !ex[p] {
					queue = append(queue, item{p, it.depth + 1})
				}
				continue
			}
			if !de.Type().IsRegular() {
				continue
			}
			info, err := de.Info()
			if err != nil || info.Size() < 512 {
				continue
			}
			head, err := s.d.FS.ReadHead(p, len(sqliteMagic))
			if err == nil && string(head) == string(sqliteMagic) {
				rel := strings.TrimPrefix(p, dir+"/")
				found = append(found, rel)
			}
		}
	}
	return found
}

// signatures sucht in Datenordnern und Volumes ohne erkannte Datenbank nach
// Datenbank-Dateien und SQLite.
func (s *scan) signatures() {
	for i := range s.inv.Paths {
		e := &s.inv.Paths[i]
		if !e.Exists || !e.IsDir || e.CoveredBy != "" || e.Kind == inventory.KindParent {
			continue
		}
		switch e.Kind {
		case inventory.KindData:
			if e.DB == nil {
				e.DB = s.detectEngine(e.Path)
			}
			if e.DB == nil {
				e.SQLite = s.findSQLite(e.Path, e.Excludes)
			}
		case inventory.KindProject:
			e.SQLite = s.findSQLite(e.Path, e.Excludes)
		}
	}
	for i := range s.inv.Volumes {
		v := &s.inv.Volumes[i]
		if v.Mountpoint == "" {
			continue
		}
		if v.DB == nil {
			v.DB = s.detectEngine(v.Mountpoint)
		}
		if v.DB == nil {
			v.SQLite = s.findSQLite(v.Mountpoint, nil)
		}
	}
}
