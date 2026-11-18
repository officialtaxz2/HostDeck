package restore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

type fakeHost struct {
	info  HostInfo
	mount map[string]string // Ordner-Präfix → Einhängepunkt
	free  map[string]int64
	busy  map[int]bool
}

func (f fakeHost) Info(context.Context) HostInfo { return f.info }
func (f fakeHost) Free(p string) (string, int64, int64, error) {
	m := "/"
	for pre, mp := range f.mount {
		if strings.HasPrefix(p, pre) {
			m = mp
		}
	}
	return m, f.free[m], 100 * gib, nil
}
func (f fakeHost) PortBusy(p int) bool { return f.busy[p] }

func levels(ns []Note) string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Level)
	}
	return strings.Join(out, " ")
}

func TestPlanBrauchtPlatz(t *testing.T) {
	rf := &backup.RunFile{Run: "r", Valid: true, Groups: []backup.Group{{Name: "app",
		Live:  []backup.Item{{Key: "path:/opt/app", Paths: []string{"/opt/app"}, Size: 3 * gib}, {Key: "x", Size: -1}},
		Dumps: []backup.Item{{Key: "dump:db", Strategy: backup.StratDump, Size: gib}},
		Stopped: []backup.Item{{Key: "volume:data", Paths: []string{"/var/lib/docker/volumes/data/_data"}, Size: 2 * gib},
			{Key: "alt", Paths: []string{"/srv/alt"}, Size: 50 * gib, Archive: true}},
	}}}
	inv := &inventory.Inventory{Host: inventory.Host{Arch: "amd64"}}
	p := Build(rf, inv)
	got := map[string]int64{}
	for _, s := range p.Space {
		got[s.Path] = s.Bytes
	}
	if got["/opt/app"] != 3*gib || got[DockerDir] != gib || got["/var/lib/docker/volumes/data/_data"] != 2*gib || len(got) != 3 {
		t.Errorf("Platz: %v", got)
	}
	if p.SpaceUnknown != 1 || p.Arch != "amd64" {
		t.Errorf("unbekannt %d, arch %q", p.SpaceUnknown, p.Arch)
	}
}

func TestCheckHost(t *testing.T) {
	p := &Plan{Arch: "amd64",
		Space:  []SpaceNeed{{Path: "/opt/app", Bytes: 3 * gib}, {Path: DockerDir, Bytes: 4 * gib}},
		Groups: []Group{{Name: "app", Services: []string{"web"}, Ports: []int{80, 8080}}, {Name: "aus", Ports: []int{9000}}},
	}
	h := fakeHost{info: HostInfo{OS: "Debian GNU/Linux 13 (trixie)", OSID: "debian", Arch: "amd64", CPUs: 4, RAM: 8 * gib, TimeSync: "ja"},
		free: map[string]int64{"/": 80 * gib}}
	ns := CheckHost(context.Background(), p, h, nil)
	if l := levels(ns); l != "ok ok ok ok" {
		t.Fatalf("alles gut: %s %+v", l, ns)
	}
	if !strings.Contains(ns[1].Text, "Platz auf /: braucht etwa 7") {
		t.Errorf("eine Platte für beides: %q", ns[1].Text)
	}

	// Platte schon vorher fast voll, Bedarf winzig: Der Hinweis nennt die
	// volle Platte, nicht den Bedarf.
	small := &Plan{Arch: "amd64", Space: []SpaceNeed{{Path: "/opt/app", Bytes: 100 << 10}}}
	h.free = map[string]int64{"/": 3 * gib}
	ns = CheckHost(context.Background(), small, h, nil)
	if l := levels(ns); l != "ok warn ok" || !strings.Contains(ns[1].Text, "ist schon wenig frei") {
		t.Fatalf("volle Platte: %s %+v", l, ns)
	}
	h.free = map[string]int64{"/": 80 * gib}

	// Docker auf eigener Platte, die zu klein ist; Port belegt; Uhr aus.
	h.mount = map[string]string{"/var/lib/docker": "/var/lib/docker"}
	h.free = map[string]int64{"/": 80 * gib, "/var/lib/docker": 4 * gib}
	h.busy = map[int]bool{8080: true, 9000: true}
	h.info.TimeSync = "nein"
	ns = CheckHost(context.Background(), p, h, nil)
	if l := levels(ns); l != "ok ok err warn warn" {
		t.Fatalf("Probleme: %s %+v", l, ns)
	}
	if !strings.Contains(ns[3].Text, "8080 (app)") || strings.Contains(ns[3].Text, "9000") {
		t.Errorf("nur Ports laufender Projekte: %q", ns[3].Text)
	}

	// Auffrischen: Die Ports gehören der Probe-Kopie von HostDeck.
	prev := &Record{Groups: []GroupRecord{{Name: "app", Ports: []int{80, 8080}}}}
	for _, n := range CheckHost(context.Background(), p, h, prev) {
		if strings.Contains(n.Text, "belegt") {
			t.Errorf("Ports der eigenen Kopie gelten nicht als belegt: %q", n.Text)
		}
	}

	// Anderer Prozessor mit gesicherten Images.
	p.Images = []Image{{ID: "sha256:1"}}
	h.info.Arch = "arm64"
	if l := levels(CheckHost(context.Background(), p, h, nil)); !strings.HasPrefix(l, "ok err") {
		t.Errorf("anderer Prozessor: %s", l)
	}
	h.info.OSID = "alpine"
	if l := levels(CheckHost(context.Background(), p, h, nil)); !strings.HasPrefix(l, "warn") {
		t.Errorf("anderes System: %s", l)
	}
}

func TestLocalHost(t *testing.T) {
	root := t.TempDir()
	write := func(p, s string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		if err := os.WriteFile(filepath.Join(root, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("/etc/os-release", "PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nID=debian\n")
	write("/proc/meminfo", "MemTotal:        8000000 kB\nMemFree: 1 kB\n")
	write("/proc/self/mounts", "/dev/sda1 / ext4 rw 0 0\n/dev/sdb1 /var/lib/docker ext4 rw 0 0\noverlay /var/lib/docker/overlay2/x/merged overlay rw 0 0\n")
	_ = os.MkdirAll(filepath.Join(root, "var/lib/docker"), 0o755)
	l := LocalHost{Root: root}
	info := l.Info(context.Background())
	if info.OSID != "debian" || info.OS != "Debian GNU/Linux 13 (trixie)" || info.RAM != 8000000*1024 {
		t.Errorf("Info: %+v", info)
	}
	m, free, total, err := l.Free("/var/lib/docker/volumes/x/_data")
	if err != nil || m != "/var/lib/docker" || free <= 0 || total <= 0 {
		t.Errorf("Free: %q %d %d %v", m, free, total, err)
	}
	if m, _, _, _ := l.Free("/opt/neu/ordner"); m != "/" {
		t.Errorf("fehlender Ordner: %q", m)
	}
}
