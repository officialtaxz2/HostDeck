// Package discover erfasst, was auf einem Server läuft, und schreibt das
// Ergebnis in ein inventory.Inventory. Es ändert nichts am Server: Docker wird
// nur über den lesenden Client abgefragt, Dateien werden nur gelesen.
package discover

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Docker ist der Teil der Docker-API, den discover braucht. Alle Methoden
// lesen nur.
type Docker interface {
	Version(ctx context.Context) (dockerapi.Version, error)
	Info(ctx context.Context) (dockerapi.Info, error)
	Containers(ctx context.Context, withSize bool) ([]dockerapi.ContainerSummary, error)
	Inspect(ctx context.Context, id string) (dockerapi.Container, error)
	Volumes(ctx context.Context) ([]dockerapi.Volume, error)
	VolumeSizes(ctx context.Context) (map[string]int64, error)
	Images(ctx context.Context, apiVersion string) ([]dockerapi.Image, error)
	Networks(ctx context.Context) ([]dockerapi.Network, error)
}

// Git liest den Stand eines Git-Repos. Ist dir kein Repo, gibt Inspect nil
// ohne Fehler zurück.
type Git interface {
	Inspect(ctx context.Context, dir string) (*inventory.Git, error)
}

// Caddy übersetzt eine Caddy-Konfiguration in JSON (caddy adapt).
type Caddy interface {
	Adapt(ctx context.Context, configPath string) ([]byte, error)
}

type Deps struct {
	Docker Docker
	FS     hostfs.FS
	Git    Git
	Caddy  Caddy
	// Statfs meldet Größe und freien Platz des Dateisystems, auf dem ein
	// Pfad liegt. Ohne Angabe fragt discover das System.
	Statfs func(path string) (total, free int64, err error)
}

type Options struct {
	ToolVersion string
	ScanRoots   []string      // Ordner, in denen nach nicht zugeordneten Daten gesucht wird
	CaddyConfig string        // Pfad zum Caddyfile
	NoSizes     bool          // keine Größen berechnen
	SizeTimeout time.Duration // Zeitlimit pro Ordner
	Euid        int
	Now         func() time.Time
	Log         io.Writer // Fortschritt, nie Inhalte
	// UserStopped: Docker-Namen der Container, die du im Leitstand gestoppt
	// hast. Eine Route dorthin ist pausiert, keine Warnung.
	UserStopped func() map[string]time.Time
}

func (o *Options) defaults() {
	if len(o.ScanRoots) == 0 {
		o.ScanRoots = []string{"/opt", "/srv"}
	}
	if o.CaddyConfig == "" {
		o.CaddyConfig = "/etc/caddy/Caddyfile"
	}
	if o.SizeTimeout == 0 {
		o.SizeTimeout = 3 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.ToolVersion == "" {
		o.ToolVersion = "HostDeck dev"
	}
}

type scan struct {
	d   Deps
	o   Options
	inv *inventory.Inventory

	raw      map[string]dockerapi.Container // nach Container-Schlüssel
	byID     map[string]string              // Container-ID -> Schlüssel
	projects map[string]*projectBuild
	dpkg     *dpkgDB // nil, wenn es keine Paketdatenbank von Debian/Ubuntu gibt

	stoppedByYou map[string]bool // nach Docker-Namen, einmal gelesen
}

type projectBuild struct {
	name        string
	workDirs    map[string]int
	configFiles map[string]bool
	envFiles    map[string]bool
}

// Run erfasst den Server.
func Run(ctx context.Context, d Deps, o Options) (*inventory.Inventory, error) {
	o.defaults()
	s := &scan{
		d: d,
		o: o,
		inv: &inventory.Inventory{
			Version:   inventory.SchemaVersion,
			Tool:      o.ToolVersion,
			CreatedAt: o.Now().UTC().Truncate(time.Second),
		},
		raw:      map[string]dockerapi.Container{},
		byID:     map[string]string{},
		projects: map[string]*projectBuild{},
	}
	s.logf("Lese Host-Daten")
	s.host()
	s.logf("Frage Docker ab (nur lesend)")
	if err := s.docker(ctx); err != nil {
		if !s.dockerMissing(err) {
			return nil, fmt.Errorf("Docker nicht erreichbar: %w", err)
		}
		s.inv.Docker = inventory.Docker{Missing: true}
		s.logf("Docker ist hier nicht installiert; HostDeck erfasst den Server ohne Container")
	} else {
		if err := s.containers(ctx); err != nil {
			return nil, err
		}
		s.managed()
		s.managedServers()
		if err := s.images(ctx); err != nil {
			return nil, err
		}
		if err := s.volumes(ctx); err != nil {
			return nil, err
		}
		if err := s.networks(ctx); err != nil {
			return nil, err
		}
	}
	s.logf("Ordne Projekte und Git-Stände zu")
	s.buildProjects(ctx)
	s.dbFromImages()
	s.logf("Ordne Pfade ihren Eigentümern zu")
	s.paths()
	s.attachDatabases()
	s.logf("Suche Datenbanken und SQLite-Dateien")
	s.signatures()
	s.logf("Lese offene Ports")
	s.listeners()
	s.logf("Lese Caddy-Konfiguration")
	s.caddy(ctx)
	s.unclaimed(ctx)
	s.logf("Prüfe Home-Ordner, Cronjobs, Dienste und Systemeinstellungen")
	s.hostScan()
	if !o.NoSizes {
		s.logf("Berechne Größen (niedrige Priorität, kann einige Minuten dauern)")
		s.sizes(ctx)
	}
	s.findings()
	s.sort()
	return s.inv, nil
}

func (s *scan) logf(format string, args ...any) {
	fmt.Fprintf(s.o.Log, format+"\n", args...)
}

func (s *scan) host() {
	h := &s.inv.Host
	h.Arch = runtime.GOARCH
	h.Root = s.o.Euid == 0
	if b, err := s.d.FS.ReadFile("/proc/sys/kernel/hostname", 256); err == nil {
		h.Hostname = strings.TrimSpace(string(b))
	}
	if b, err := s.d.FS.ReadFile("/proc/sys/kernel/osrelease", 256); err == nil {
		h.Kernel = strings.TrimSpace(string(b))
	}
	if b, err := s.d.FS.ReadFile("/etc/os-release", 16<<10); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"'`)
			switch k {
			case "PRETTY_NAME":
				h.OS = v
			case "ID":
				h.OSID = v
			case "VERSION_ID":
				h.OSVersion = v
			}
		}
	}
}

func (s *scan) docker(ctx context.Context) error {
	v, err := s.d.Docker.Version(ctx)
	if err != nil {
		return err
	}
	info, err := s.d.Docker.Info(ctx)
	if err != nil {
		return err
	}
	s.inv.Docker = inventory.Docker{
		Version:       v.Version,
		APIVersion:    v.APIVersion,
		RootDir:       info.DockerRootDir,
		StorageDriver: info.Driver,
	}
	if s.inv.Host.Hostname == "" {
		s.inv.Host.Hostname = info.Name
	}
	return nil
}

// dockerMissing: Der Socket fehlt und auch sonst deutet nichts auf Docker
// hin. Ein installiertes Docker, das gerade nicht läuft, bleibt ein Fehler;
// sonst fehlten in der Bestandsaufnahme still alle Projekte.
func (s *scan) dockerMissing(err error) bool {
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	for _, p := range []string{"/var/lib/docker", "/usr/bin/dockerd", "/usr/sbin/dockerd", "/usr/local/bin/dockerd", "/etc/docker"} {
		if _, err := s.d.FS.Lstat(p); err == nil {
			return false
		}
	}
	return true
}

func (s *scan) sort() {
	inv := s.inv
	sort.Slice(inv.Projects, func(i, j int) bool { return inv.Projects[i].Key < inv.Projects[j].Key })
	sort.Slice(inv.Containers, func(i, j int) bool { return inv.Containers[i].Key < inv.Containers[j].Key })
	sort.Slice(inv.Managed, func(i, j int) bool { return inv.Managed[i].Key < inv.Managed[j].Key })
	sort.Slice(inv.Volumes, func(i, j int) bool { return inv.Volumes[i].Key < inv.Volumes[j].Key })
	sort.Slice(inv.Images, func(i, j int) bool { return imageLabel(inv.Images[i]) < imageLabel(inv.Images[j]) })
	sort.Slice(inv.Networks, func(i, j int) bool { return inv.Networks[i].Name < inv.Networks[j].Name })
	sort.Slice(inv.Paths, func(i, j int) bool { return inv.Paths[i].Path < inv.Paths[j].Path })
	sort.Slice(inv.System, func(i, j int) bool {
		if inv.System[i].Container != inv.System[j].Container {
			return inv.System[i].Container < inv.System[j].Container
		}
		return inv.System[i].Source < inv.System[j].Source
	})
	sort.Slice(inv.Unclaimed, func(i, j int) bool { return inv.Unclaimed[i].Path < inv.Unclaimed[j].Path })
	sort.Slice(inv.Listeners, func(i, j int) bool {
		a, b := inv.Listeners[i], inv.Listeners[j]
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		return a.Addr < b.Addr
	})
	if inv.Caddy != nil {
		sort.SliceStable(inv.Caddy.Routes, func(i, j int) bool {
			return strings.Join(inv.Caddy.Routes[i].Hosts, ",") < strings.Join(inv.Caddy.Routes[j].Hosts, ",")
		})
	}
	for i := range inv.Projects {
		p := &inv.Projects[i]
		sort.Strings(p.Containers)
		sort.Strings(p.Managed)
		sort.Strings(p.Services)
		sort.Strings(p.Domains)
	}
	for i := range inv.Paths {
		sort.Strings(inv.Paths[i].Excludes)
		sort.Strings(inv.Paths[i].Contains)
		sort.Slice(inv.Paths[i].Mounts, func(a, b int) bool {
			ma, mb := inv.Paths[i].Mounts[a], inv.Paths[i].Mounts[b]
			if ma.Container != mb.Container {
				return ma.Container < mb.Container
			}
			return ma.Destination < mb.Destination
		})
	}
	for i := range inv.Volumes {
		sort.Strings(inv.Volumes[i].UsedBy)
	}
	for i := range inv.Images {
		sort.Strings(inv.Images[i].UsedBy)
	}
}

// imageLabel ist der Name, unter dem ein Image angezeigt wird.
func imageLabel(img inventory.Image) string {
	if len(img.Tags) > 0 {
		return img.Tags[0]
	}
	return shortID(img.ID)
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
