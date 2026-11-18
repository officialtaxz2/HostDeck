package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/aufraeumen"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/platz"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

// Compose-Labels an Containern.
const (
	lblProject = "com.docker.compose.project"
	lblWorkDir = "com.docker.compose.project.working_dir"
	lblFiles   = "com.docker.compose.project.config_files"
	lblEnv     = "com.docker.compose.project.environment_file"
)

// spaceService baut „Ordner und Dateien“ unter Speicherplatz.
func spaceService(sock, data, result string, trash *aufraeumen.Service) *platz.Service {
	return &platz.Service{
		File:     filepath.Join(data, "platz", "baum.json.gz"),
		Facts:    spaceFacts(sock, data, result),
		Disk:     diskDF,
		Trash:    trash,
		Unselect: func(paths []string) (int, error) { return web.UnselectUnder(result, paths) },
	}
}

// ownPlaces sind Orte von HostDeck selbst, je mit der Seite im Leitstand, wo
// es dafür einen eigenen Weg gibt.
func ownPlaces(data string) []platz.Own {
	own := []platz.Own{
		{Path: data, What: "Daten von HostDeck"},
		{Path: filepath.Join(data, ort.BackupRepo), What: "Backups auf dem Server", Page: "#/backup"},
		{Path: filepath.Join(data, ort.TrashDir), What: "Papierkorb", Page: "#/platz"},
		{Path: filepath.Join(data, ort.MonitorDir), What: "Verlauf der Überwachung", Page: "#/ueberwachung"},
		{Path: filepath.Join(data, ort.BenchDir), What: "Ergebnisse des Leistungstests", Page: "#/leistungstest"},
		{Path: ort.ProgDir, What: "Programm HostDeck"},
		{Path: ort.BinLink, What: "Programm HostDeck"},
	}
	for _, u := range append(append([]string(nil), ort.Units...), ort.ShieldUnit) {
		own = append(own, platz.Own{Path: filepath.Join(ort.UnitDir, u), What: "Dienst von HostDeck"})
	}
	if dir, err := stateDir(); err == nil {
		own = append(own, platz.Own{Path: dir, What: "Daten von HostDeck"})
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		own = append(own, platz.Own{Path: exe, What: "Programm HostDeck"})
	}
	return own
}

// dockerDown steht im Schutz, wenn Docker da ist, aber nicht antwortet.
const dockerDown = "Docker antwortet gerade nicht; ohne Docker weiß HostDeck nicht, welche Ordner Container benutzen"

// spaceFacts sammelt, was der Schutz braucht: Docker-Wurzel, Container
// mit Einbindungen, Volumes, Compose-Projekte samt Dateien und die Orte von
// HostDeck. Docker-Wurzel und Projekte kommen notfalls aus der letzten
// Bestandsaufnahme. Gibt es Docker hier, antwortet aber nicht, ist der
// Schutz unvollständig (Incomplete): Dann zeigt der Leitstand die Liste,
// löscht aber nichts.
func spaceFacts(sock, data, result string) func(ctx context.Context) platz.Facts {
	return func(ctx context.Context) platz.Facts {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		f := platz.Facts{Own: ownPlaces(data), Homes: passwdHomes("/etc/passwd")}
		var inv inventory.Inventory
		if b, err := os.ReadFile(filepath.Join(result, "inventory.json")); err == nil {
			_ = json.Unmarshal(b, &inv)
		}
		dockerHere := inv.Docker.RootDir != ""
		if _, err := os.Stat(sock); err == nil {
			dockerHere = true
		}
		if info, err := dockerapi.New(sock).Info(ctx); err == nil && info.DockerRootDir != "" {
			f.DockerRoot = info.DockerRootDir
		} else {
			f.DockerRoot = inv.Docker.RootDir
		}
		projects := map[string]*platz.Project{}
		addProject := func(name, dir string, files []string) {
			if name == "" {
				return
			}
			p := projects[name]
			if p == nil {
				p = &platz.Project{Name: name}
				projects[name] = p
			}
			if p.Dir == "" && dir != "" {
				p.Dir = dir
				files = append(files, filepath.Join(dir, ".env"))
			}
			for _, x := range files {
				if x = strings.TrimSpace(x); x != "" {
					p.Files = append(p.Files, x)
				}
			}
		}
		for _, p := range inv.Projects {
			addProject(p.Name, p.WorkingDir, append(append([]string(nil), p.ConfigFiles...), p.EnvFiles...))
		}
		// Je Volume die Container, die es einbinden (Index in f.Containers).
		type volUse struct {
			ci int
			rw bool
		}
		uses := map[string][]volUse{}
		conts, err := dockerapi.NewOps(sock).List(ctx)
		if err != nil && dockerHere {
			f.Incomplete = dockerDown
		}
		for _, c := range conts {
			ct := platz.Container{Name: c.Name(), Running: c.State == "running"}
			for _, m := range c.Mounts {
				switch m.Type {
				case "bind":
					ct.Binds = append(ct.Binds, platz.Bind{Source: m.Source, RW: m.RW})
				case "volume":
					if m.Name != "" {
						uses[m.Name] = append(uses[m.Name], volUse{len(f.Containers), m.RW})
					}
				}
			}
			f.Containers = append(f.Containers, ct)
			l := c.Labels
			addProject(l[lblProject], l[lblWorkDir], append(splitList(l[lblFiles]), splitList(l[lblEnv])...))
		}
		vols, err := dockerapi.NewCleaner(sock).Volumes(ctx)
		if err != nil && dockerHere {
			f.Incomplete = dockerDown
		}
		for _, v := range vols {
			// Ein Volume mit o=bind bindet einen Ordner des Servers ein;
			// für den Schutz ist das wie eine Einbindung in jeden Container,
			// der das Volume benutzt.
			device := ""
			if bindVolume(v.Options) {
				device = v.Options["device"]
			}
			var u []string
			for _, use := range uses[v.Name] {
				ct := &f.Containers[use.ci]
				u = append(u, ct.Name)
				if device != "" {
					ct.Binds = append(ct.Binds, platz.Bind{Source: device, RW: use.rw})
				}
			}
			sort.Strings(u)
			if v.Mountpoint == "" && device == "" {
				continue
			}
			dir := v.Mountpoint
			if filepath.Base(dir) == "_data" {
				dir = filepath.Dir(dir)
			}
			f.Volumes = append(f.Volumes, platz.Volume{Name: v.Name, Path: dir, Users: u, Device: device})
		}
		names := make([]string, 0, len(projects))
		for n := range projects {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			f.Projects = append(f.Projects, *projects[n])
		}
		return f
	}
}

// passwdHomes liest die Home-Ordner der Benutzer aus file (Format von
// /etc/passwd); nur absolute Pfade.
func passwdHomes(file string) map[string]string {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 6 && f[0] != "" && strings.HasPrefix(f[5], "/") {
			out[f[0]] = f[5]
		}
	}
	return out
}

// bindVolume sagt, ob die Optionen eines Volumes (Treiber local) einen
// Ordner des Servers einbinden: o enthält bind oder rbind.
func bindVolume(opt map[string]string) bool {
	if opt["device"] == "" || !strings.HasPrefix(opt["device"], "/") {
		return false
	}
	for _, o := range strings.Split(opt["o"], ",") {
		if o = strings.TrimSpace(o); o == "bind" || o == "rbind" {
			return true
		}
	}
	return false
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
