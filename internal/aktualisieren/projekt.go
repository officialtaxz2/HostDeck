package aktualisieren

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

// Docker ist der Teil von dockerapi.Ops, den das Aktualisieren braucht.
type Docker interface {
	List(ctx context.Context) ([]dockerapi.OpsContainer, error)
	Inspect(ctx context.Context, id string) (dockerapi.Container, error)
	ImageInspect(ctx context.Context, id string) (dockerapi.ImageInfo, error)
}

const (
	labelProject    = "com.docker.compose.project"
	labelService    = "com.docker.compose.service"
	labelOneoff     = "com.docker.compose.oneoff"
	labelWorkingDir = "com.docker.compose.project.working_dir"
	labelFiles      = "com.docker.compose.project.config_files"
	labelEnvFile    = "com.docker.compose.project.environment_file"
)

// Project ist ein Compose-Projekt, so wie es gerade läuft.
type Project struct {
	Name     string
	Dir      string   // Arbeitsordner von Compose
	Files    []string // Compose-Dateien
	EnvFiles []string
	Services []Service
}

// Service ist ein Dienst des Projekts mit seinem Container.
type Service struct {
	Name        string
	Container   string // Name des Containers
	ContainerID string
	Ref         string // Image, wie konfiguriert, etwa postgres:17
	ImageID     string // Image, mit dem der Container läuft
	Running     bool
	Job         bool   // einmaliger Job, sauber beendet
	Health      bool   // hat einen Healthcheck
	Build       *Build // nur bei Diensten, die Compose selbst baut
}

// Build sagt, woraus Compose einen Dienst baut.
type Build struct {
	Context string // Ordner oder URL
	Local   bool   // Context ist ein Ordner auf dem Server
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ValidName sagt, ob s als Name eines Projekts oder Dienstes taugt (so wie
// Compose Namen bildet).
func ValidName(s string) bool { return validName.MatchString(s) }

// projects liest alle Compose-Projekte mit ihren Diensten aus Docker. Das
// Bauen kennt es noch nicht; das liest load aus der Compose-Konfiguration.
func projects(ctx context.Context, d Docker) (map[string]*Project, error) {
	list, err := d.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("Docker antwortet nicht: %w", err)
	}
	out := map[string]*Project{}
	for _, c := range list {
		name := c.Labels[labelProject]
		svc := c.Labels[labelService]
		if name == "" || svc == "" || strings.EqualFold(c.Labels[labelOneoff], "true") || !ValidName(name) {
			continue
		}
		p := out[name]
		if p == nil {
			p = &Project{Name: name, Dir: c.Labels[labelWorkingDir]}
			p.Files = splitList(c.Labels[labelFiles])
			p.EnvFiles = splitList(c.Labels[labelEnvFile])
			out[name] = p
		}
		// Mehrere Container eines Dienstes (scale): der erste zählt.
		dup := false
		for _, s := range p.Services {
			if s.Name == svc {
				dup = true
			}
		}
		if dup {
			continue
		}
		p.Services = append(p.Services, Service{Name: svc, Container: c.Name(), ContainerID: c.ID, Ref: c.Image, ImageID: c.ImageID, Running: c.State == "running" || c.State == "restarting"})
	}
	for _, p := range out {
		sort.Slice(p.Services, func(i, j int) bool { return p.Services[i].Name < p.Services[j].Name })
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// Updater bringt Projekte auf einen neuen Stand.
type Updater struct {
	check sync.Mutex // ein Nachsehen zur Zeit

	Docker   Docker
	CLI      CLI
	Registry *Registry
	// StateDir hält den Rückfall je Projekt und den letzten Prüfstand.
	StateDir string
	// Domains nennt die Domains eines Projekts (aus der Bestandsaufnahme);
	// Probe fragt sie über Caddy ab. Beide dürfen fehlen.
	Domains func(project string) []string
	Probe   caddy.Prober
	// Wait begrenzt, wie lange HostDeck nach dem Neustart auf gesunde Dienste
	// wartet; Settle ist, wie lange sie dafür ruhig laufen müssen.
	Wait, Settle, Poll time.Duration // 0: 120 s, 10 s, 2 s
	// GitCheck prüft einen Git-Ordner, bevor HostDeck dort Git aufruft
	// (Standard CheckGitDir; Tests setzen eine Attrappe).
	GitCheck func(dir string) (GitRepo, error)
}

// load liest ein Projekt mit allem, was das Aktualisieren braucht: Images
// der Container, Healthchecks und was Compose selbst baut.
func (u *Updater) load(ctx context.Context, name string) (*Project, error) {
	all, err := projects(ctx, u.Docker)
	if err != nil {
		return nil, err
	}
	p := all[name]
	if p == nil {
		return nil, fmt.Errorf("%w: Compose-Projekt %s", ErrNotFound, name)
	}
	if p.Dir == "" || len(p.Files) == 0 {
		return nil, fmt.Errorf("Projekt %s: Docker kennt seinen Compose-Ordner nicht (Container ohne Compose-Angaben)", name)
	}
	for i := range p.Services {
		s := &p.Services[i]
		v, err := u.Docker.Inspect(ctx, s.ContainerID)
		if err != nil {
			return nil, fmt.Errorf("Container %s: %w", s.Container, err)
		}
		s.Ref = v.Config.Image
		s.ImageID = v.Image
		s.Running = v.State.Running
		s.Health = v.State.Health != nil
		pol := v.HostConfig.RestartPolicy.Name
		s.Job = !v.State.Running && v.State.Status == "exited" && v.State.ExitCode == 0 && (pol == "" || pol == "no" || pol == "on-failure")
	}
	builds, err := u.composeBuilds(ctx, p)
	if err != nil {
		return nil, err
	}
	for i := range p.Services {
		if b, ok := builds[p.Services[i].Name]; ok {
			b := b
			p.Services[i].Build = &b
		}
	}
	return p, nil
}

// composeArgs sind die Schalter, mit denen Compose genau dieses Projekt
// anspricht, so wie es angelegt wurde.
func composeArgs(p *Project, rest ...string) []string {
	args := []string{"compose", "--project-name", p.Name, "--project-directory", p.Dir}
	for _, f := range p.Files {
		args = append(args, "--file", f)
	}
	for _, e := range p.EnvFiles {
		args = append(args, "--env-file", e)
	}
	return append(args, rest...)
}

// composeBuilds liest aus der Compose-Konfiguration, welche Dienste Compose
// selbst baut und woraus. Die Ausgabe von „compose config“ enthält die
// Werte der Umgebung; HostDeck liest daraus nur image und build und behält
// nichts davon.
func (u *Updater) composeBuilds(ctx context.Context, p *Project) (map[string]Build, error) {
	out, err := u.CLI.Run(ctx, p.Dir, nil, "docker", composeArgs(p, "config", "--format", "json")...)
	if err != nil {
		return nil, fmt.Errorf("Compose-Konfiguration von %s nicht lesbar: %v", p.Name, short(err))
	}
	var cf struct {
		Services map[string]struct {
			Build *struct {
				Context string `json:"context"`
			} `json:"build"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &cf); err != nil {
		return nil, fmt.Errorf("Compose-Konfiguration von %s nicht lesbar", p.Name)
	}
	res := map[string]Build{}
	for name, s := range cf.Services {
		if s.Build == nil {
			continue
		}
		ctxDir := s.Build.Context
		if ctxDir == "" {
			ctxDir = p.Dir
		}
		local := !strings.Contains(ctxDir, "://") && !strings.HasPrefix(ctxDir, "git@")
		if local && !filepath.IsAbs(ctxDir) {
			ctxDir = filepath.Join(p.Dir, ctxDir)
		}
		res[name] = Build{Context: ctxDir, Local: local}
	}
	return res, nil
}

// short kürzt eine Fehlermeldung für die Anzeige.
func short(err error) string {
	s := err.Error()
	if len(s) > 400 {
		s = s[:400] + " …"
	}
	return s
}

// dataImages sind Images, die Daten in eigenem Format halten. Ein neues
// Image kann die Daten umstellen; zurück geht dann nur mit dem Backup.
var dataImages = []string{"postgres", "postgis", "mysql", "mariadb", "mongo", "redis", "valkey", "keydb", "influxdb", "elasticsearch", "opensearch", "clickhouse", "cassandra", "couchdb", "neo4j", "timescale"}

// IsDatabase sagt, ob ref ein Datenbank-Image ist.
func IsDatabase(ref string) bool {
	r, err := ParseRef(ref)
	if err != nil {
		return false
	}
	base := r.Repo[strings.LastIndex(r.Repo, "/")+1:]
	for _, d := range dataImages {
		if base == d || strings.HasPrefix(base, d+"-") || strings.HasPrefix(base, d+"_") {
			return true
		}
	}
	return false
}
