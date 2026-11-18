package discover

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

func TestEngineFromImage(t *testing.T) {
	cases := map[string][2]string{
		"postgres:17-alpine":                       {"postgres", "17"},
		"postgres:17.2":                            {"postgres", "17"},
		"pgautoupgrade/pgautoupgrade:18-alpine":    {"postgres", "18"},
		"docker.io/library/postgres:15@sha256:abc": {"postgres", "15"},
		"mysql:8.4":                                {"mysql", "8.4"},
		"mariadb:11.4.2":                           {"mariadb", "11.4"},
		"redis:7-alpine":                           {"redis", "7"},
		"valkey/valkey:8-alpine":                   {"valkey", "8"},
		"postgres":                                 {"postgres", ""},
		"postgres:latest":                          {"postgres", ""},
		"localhost:5000/postgres:16":               {"postgres", "16"},
		"pensum-app:latest":                        {"", ""},
		"sha256:eb4d35b7452d":                      {"", ""},
		"ghcr.io/x/mysql-backup:1":                 {"", ""},
	}
	for ref, want := range cases {
		e, v := engineFromImage(ref)
		if e != want[0] || v != want[1] {
			t.Errorf("%s: %s %s, erwartet %s %s", ref, e, v, want[0], want[1])
		}
	}
}

func TestParseProcAddr(t *testing.T) {
	cases := map[string]struct {
		addr string
		port int
	}{
		"0100007F:0BBD":                         {"127.0.0.1", 3005},
		"00000000:0016":                         {"0.0.0.0", 22},
		"00000000000000000000000000000000:0016": {"::", 22},
		"00000000000000000000000001000000:1F90": {"::1", 8080},
		"0000000000000000FFFF00000100007F:0050": {"127.0.0.1", 80},
	}
	for in, want := range cases {
		addr, port, ok := parseProcAddr(in)
		if !ok || addr != want.addr || port != want.port {
			t.Errorf("%s: %s %d %v, erwartet %s %d", in, addr, port, ok, want.addr, want.port)
		}
	}
	if _, _, ok := parseProcAddr("kaputt"); ok {
		t.Error("ungültige Adresse akzeptiert")
	}
}

func TestStripCredentials(t *testing.T) {
	cases := map[string]string{
		"https://x-access-token:ghp_GEHEIM@github.com/officialtaxz2/a.git": "https://github.com/officialtaxz2/a.git",
		"https://demo@github.com/officialtaxz2/a.git":                      "https://github.com/officialtaxz2/a.git",
		"git@github.com:officialtaxz2/a.git":                               "git@github.com:officialtaxz2/a.git",
		"ssh://git@github.com/officialtaxz2/a.git":                         "ssh://git@github.com/officialtaxz2/a.git",
		"ssh://git:pw@github.com/officialtaxz2/a.git":                      "ssh://github.com/officialtaxz2/a.git",
		"https://github.com/officialtaxz2/a.git":                           "https://github.com/officialtaxz2/a.git",
	}
	for in, want := range cases {
		if got := StripCredentials(in); got != want {
			t.Errorf("%s: %s, erwartet %s", in, got, want)
		}
	}
}

func TestSystempfade(t *testing.T) {
	yes := []string{"/", "/proc", "/proc/1", "/sys/fs/cgroup", "/var/run/docker.sock", "/run/docker.sock", "/home/x/docker.sock",
		"/var/lib/docker", "/var/lib/docker/containers", "/etc/localtime", "/etc/hosts", "/dev/dri"}
	no := []string{"/opt/docker", "/opt/calagopus/wings", "/etc/caddy", "/var/lib/dockerdata", "/procs", "/srv", "/etc/hostsfile"}
	for _, p := range yes {
		if !isSystemPath(p) {
			t.Errorf("%s soll Systempfad sein", p)
		}
	}
	for _, p := range no {
		if isSystemPath(p) {
			t.Errorf("%s soll kein Systempfad sein", p)
		}
	}
}

func TestContainerKey(t *testing.T) {
	l := map[string]string{labelProject: "pensum", labelService: "db", labelNumber: "1"}
	if k := containerKey("pensum-db-1", l); k != "compose:pensum/db" {
		t.Error(k)
	}
	l[labelNumber] = "2"
	if k := containerKey("pensum-db-2", l); k != "compose:pensum/db#2" {
		t.Error(k)
	}
	l[labelOneoff] = "True"
	if k := containerKey("pensum-db-run-abc", l); k != "container:pensum-db-run-abc" {
		t.Error(k)
	}
	if k := containerKey("c324292c", nil); k != "container:c324292c" {
		t.Error(k)
	}
}

func TestCaddyImports(t *testing.T) {
	root := t.TempDir()
	write := func(p, c string) {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	write("/etc/caddy/Caddyfile", `(gemeinsam) {
	encode gzip
}
import gemeinsam
import sites/*.caddy
import leer/*.caddy
import /opt/docker/fehlt.caddy
# import /etc/caddy/auskommentiert.caddy
a.de {
	import gemeinsam
	import snippets.caddy # Kommentar
}
`)
	write("/etc/caddy/sites/a.caddy", "import ../tief.caddy\n")
	write("/etc/caddy/sites/b.caddy", "b.de {}\n")
	write("/etc/caddy/tief.caddy", "c.de {}\n")
	write("/etc/caddy/snippets.caddy", "(x) {}\n")
	s := &scan{d: Deps{FS: hostfs.New(root)}, inv: &inventory.Inventory{}}
	c := &inventory.Caddy{}
	s.caddyImports(c, "/etc/caddy/Caddyfile", "/etc/caddy", map[string]bool{}, 0)
	var got []string
	for _, imp := range c.Imports {
		mark := ""
		if !imp.Exists {
			mark = "!"
		}
		got = append(got, imp.Path+mark)
	}
	want := "/etc/caddy/sites/a.caddy /etc/caddy/tief.caddy /etc/caddy/sites/b.caddy /opt/docker/fehlt.caddy! /etc/caddy/snippets.caddy"
	if strings.Join(got, " ") != want {
		t.Errorf("Imports:\n%s\nerwartet\n%s", strings.Join(got, " "), want)
	}
}

func TestCaddyRoutenOhneGeheimnisse(t *testing.T) {
	js := `{"apps":{"http":{"servers":{"a":{"routes":[
		{"match":[{"host":["x.de"]}],"handle":[{"handler":"subroute","routes":[{"handle":[
			{"handler":"authentication","providers":{"http_basic":{"accounts":[{"password":"geheim"}]}}},
			{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:1"},{"dial":"localhost:2"}]}]}]}]},
		{"match":[{"host":["y.de","www.y.de"]}],"handle":[{"handler":"static_response","headers":{"Location":["https://z.de"]}}]},
		{"handle":[{"handler":"file_server","root":"/srv"}]}
	]}}}}}`
	routes, err := parseCaddyRoutes([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range routes {
		got = append(got, strings.Join(r.Hosts, ",")+" "+r.Handler+" "+r.Upstream)
	}
	want := "x.de reverse_proxy localhost:1|x.de reverse_proxy localhost:2|www.y.de,y.de static_response |* file_server "
	if strings.Join(got, "|") != want {
		t.Errorf("Routen: %q", strings.Join(got, "|"))
	}
}

func TestEintragDesLeitstandsZaehltNicht(t *testing.T) {
	js := `{"apps":{"http":{"servers":{"a":{"routes":[
		{"match":[{"host":["hostdeck.x.de"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"unix//run/hostdeck-caddy/web.sock"}]}]}]}]},
		{"match":[{"host":["x.de"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:1"}]}]}
	]}}}}}`
	routes, err := parseCaddyRoutes([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	routes = withoutOwnRoutes(routes)
	if len(routes) != 1 || routes[0].Hosts[0] != "x.de" {
		t.Fatalf("Routen: %+v", routes)
	}
}

func TestDirSizeMitAusnahmenUndGroesstenOrdnern(t *testing.T) {
	root := t.TempDir()
	write := func(p string, n int) {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, make([]byte, n), 0o644)
	}
	write("/data/volumes/welt.dat", 64<<10)
	write("/data/backups/b1.tar", 256<<10)
	write("/data/backups/b2.tar", 256<<10)
	write("/data/db/PG_VERSION", 1<<10)
	os.Link(filepath.Join(root, "/data/backups/b1.tar"), filepath.Join(root, "/data/backups/hardlink.tar"))
	os.Symlink("/etc", filepath.Join(root, "/data/link"))

	s := &scan{d: Deps{FS: hostfs.New(root)}, o: Options{SizeTimeout: time.Minute}, inv: &inventory.Inventory{}}
	all, partial, largest := s.dirSize(context.Background(), "/data", nil)
	without, _, _ := s.dirSize(context.Background(), "/data", []string{"/data/backups"})
	if partial || all <= without || without < 64<<10 {
		t.Fatalf("Größen: alle %d, ohne backups %d, partial %v", all, without, partial)
	}
	if all > 700<<10 {
		t.Errorf("Hardlink doppelt gezählt oder Symlink verfolgt: %d", all)
	}
	if largest != nil {
		t.Errorf("größte Unterordner erst ab 1 GB, bekommen %v", largest)
	}
	old := largeDir
	largeDir = 1
	defer func() { largeDir = old }()
	_, _, largest = s.dirSize(context.Background(), "/data", nil)
	if len(largest) == 0 || largest[0].Path != "/data/backups" {
		t.Errorf("größter Unterordner: %+v", largest)
	}
}

func TestEchtesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git fehlt")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	run("add", "a.txt")
	run("commit", "-q", "-m", "init")
	run("remote", "add", "origin", "https://x-access-token:ghp_GEHEIM@github.com/officialtaxz2/pensum.git")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(dir, "neu.txt"), []byte("c"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)

	idx := filepath.Join(dir, ".git", "index")
	before, _ := os.Stat(idx)
	time.Sleep(10 * time.Millisecond)

	g, err := ExecGit{}.Inspect(context.Background(), filepath.Join(dir, "sub"))
	if err != nil || g == nil {
		t.Fatalf("Inspect: %v %v", g, err)
	}
	if g.Dirty != 2 || g.Branch != "main" || len(g.Commit) != 40 {
		t.Errorf("Git-Stand: %+v", g)
	}
	if g.Remote != "https://github.com/officialtaxz2/pensum.git" {
		t.Errorf("Remote mit Zugangsdaten: %s", g.Remote)
	}
	if want, _ := filepath.EvalSymlinks(dir); g.Root != want && g.Root != dir {
		t.Errorf("Root %s, erwartet %s", g.Root, dir)
	}
	after, _ := os.Stat(idx)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("git status hat den Index neu geschrieben")
	}
	if g, err := (ExecGit{}).Inspect(context.Background(), t.TempDir()); g != nil || err != nil {
		t.Errorf("kein Repo: %v %v", g, err)
	}
}

func TestGitLaeuftAlsEigentuemer(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("braucht root")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git fehlt")
	}
	// Der Eigentümer muss den Ordner erreichen können; t.TempDir liegt
	// womöglich unter einem Ordner, den nur root betreten darf.
	base, err := os.MkdirTemp("/tmp", "hostdeck-git-")
	if err != nil {
		t.Skip(err)
	}
	defer os.RemoveAll(base)
	os.Chmod(base, 0o755)
	dir := filepath.Join(base, "repo")
	os.Mkdir(dir, 0o755)
	marker := filepath.Join(dir, "marker") // für den Eigentümer beschreibbar
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	// Eine böswillige Repo-Konfiguration: fsmonitor führt einen Befehl aus.
	cfg := filepath.Join(dir, ".git", "config")
	f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("[core]\n\tfsmonitor = \"touch " + marker + "\"\n")
	f.Close()
	filepath.Walk(dir, func(p string, _ os.FileInfo, _ error) error { return os.Chown(p, 1000, 1000) })

	g, err := ExecGit{}.Inspect(context.Background(), dir)
	if err != nil || g == nil {
		t.Fatalf("Inspect als Eigentümer: %v %v", g, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("core.fsmonitor wurde ausgeführt")
	}

	// Kommt der Eigentümer nicht an den Ordner, meldet Inspect einen
	// Fehler statt „kein Repo“. Das zeigt auch, dass git nicht als root
	// läuft: root käme hinein.
	os.Chmod(base, 0o700)
	if g, err := (ExecGit{}).Inspect(context.Background(), dir); g != nil || err == nil {
		t.Errorf("unerreichbares Repo: %v %v", g, err)
	}
}

func TestPortBereiche(t *testing.T) {
	var ports []inventory.Port
	for p := 51000; p <= 51100; p++ {
		ports = append(ports, inventory.Port{HostPort: p, Proto: "udp"}, inventory.Port{HostPort: p, Proto: "udp", HostIP: "::"})
	}
	ports = append(ports, inventory.Port{HostPort: 8080, Proto: "tcp"}, inventory.Port{HostPort: 443, Proto: "tcp"}, inventory.Port{HostPort: 444, Proto: "tcp"}, inventory.Port{HostPort: 52000, Proto: "udp"})
	got := portRanges(ports)
	want := "443/tcp, 444/tcp, 8080/tcp, 51000–51100/udp (101 Ports), 52000/udp"
	if got != want {
		t.Errorf("portRanges = %q, erwartet %q", got, want)
	}
}

func TestOrdnerBuendeln(t *testing.T) {
	paths := []string{"/opt/Guides", "/opt/docker/drink-manual-backup", "/opt/docker/nekoNew/neko-a-1", "/opt/docker/nekoNew/neko-b-2",
		"/opt/docker/neko", "/opt/docker/tmp", "/opt/docker/nekoNew/neko_x", "/opt/docker/nekoNew/neko-c"}
	got := strings.Join(groupSiblings(paths, 3), " | ")
	// Verschiedene Ordner in /opt/docker bleiben einzeln, nur die
	// gleichartigen neko-Ordner werden gebündelt.
	want := "/opt/Guides | /opt/docker/drink-manual-backup | /opt/docker/nekoNew/neko-… (3 Ordner) | /opt/docker/neko | /opt/docker/tmp | /opt/docker/nekoNew/neko_x"
	if got != want {
		t.Errorf("groupSiblings = %q, erwartet %q", got, want)
	}
	if got := shortList([]string{"a", "b", "c", "d"}, 2); got != "a, b und 2 weitere" {
		t.Errorf("shortList = %q", got)
	}
	if got := shortList([]string{"a", "b"}, 2); got != "a, b" {
		t.Errorf("shortList = %q", got)
	}
}

func TestPlatzBuendel(t *testing.T) {
	for in, want := range map[string]string{
		"/var/lib/mysql/ibdata1":        "/var/lib/mysql",
		"/var/lib/foo.db":               "/var/lib/foo.db",
		"/usr/local/go/bin/go":          "/usr/local/go",
		"/mnt/alt/a/b":                  "/mnt/alt",
		"/minecraft/world/region/r.mca": "/minecraft",
		"/backup.tar.gz":                "/backup.tar.gz",
		"/opt/notizen.txt":              "/opt/notizen.txt",
	} {
		if got := spaceBucket(in); got != want {
			t.Errorf("spaceBucket(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

func TestVonHandInstalliert(t *testing.T) {
	for in, want := range map[string]string{
		"Commandline: apt install -y caddy ufw":              "caddy ufw",
		"Commandline: apt-get install git=1:2.43 htop:amd64": "git htop",
		"Commandline: apt-get install --reinstall bash":      "",
		"Commandline: apt install ./paket.deb":               "",
		"Commandline: /usr/bin/unattended-upgrade":           "",
		"Commandline: apt remove nginx":                      "",
	} {
		if got := strings.Join(installedByHand(in), " "); got != want {
			t.Errorf("%q: %q, erwartet %q", in, got, want)
		}
	}
}

func TestSSHRootMitPasswort(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc/ssh"), 0o755)
	os.WriteFile(filepath.Join(root, "etc/ssh/sshd_config"), []byte("PermitRootLogin yes\nPasswordAuthentication yes\n"), 0o644)
	s := &scan{d: Deps{FS: hostfs.New(root)}, inv: &inventory.Inventory{}}
	s.checkSSH()
	if len(s.inv.Checks) != 1 || !strings.Contains(s.inv.Checks[0].Detail, "root darf sich mit Passwort anmelden") {
		t.Errorf("Checkliste: %+v", s.inv.Checks)
	}
}

func TestKernelUndBootPakete(t *testing.T) {
	for p, want := range map[string]bool{
		"linux-generic-hwe-24.04": true, "grub-pc": true, "shim-signed": true, "qemu-guest-agent": true,
		"linux-firmware": true, "caddy": false, "docker-ce": false, "cpufrequtils": false, "linuxlogo": false,
	} {
		if machinePackage(p) != want {
			t.Errorf("machinePackage(%q) != %v", p, want)
		}
	}
}

// Die Daten von HostDeck (samt Backup) erscheinen nie als Fund in der Auswahl.
func TestPlatzOhneHostdeckSelbst(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"var/lib/hostdeck/backup/data/00/abc", "var/lib/eigen/daten.bin"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(strings.Repeat("x", 64<<10)), 0o644)
	}
	old := foundThreshold
	foundThreshold = 1
	defer func() { foundThreshold = old }()
	s := &scan{d: Deps{FS: hostfs.New(root), Statfs: func(string) (int64, int64, error) { return 100 << 30, 50 << 30, nil }}, inv: &inventory.Inventory{}}
	s.o.SizeTimeout = time.Minute
	s.space(context.Background())
	var found []string
	for _, u := range s.inv.Unclaimed {
		found = append(found, u.Path)
	}
	if got := strings.Join(found, ","); strings.Contains(got, "/var/lib/hostdeck") || !strings.Contains(got, "/var/lib/eigen") {
		t.Errorf("Funde: %s", got)
	}
}

// Die größten Ordner zeigen, was hinter System und Daten steckt; die
// Daten von HostDeck je Unterordner.
func TestPlatzGroessteOrdner(t *testing.T) {
	root := t.TempDir()
	for p, n := range map[string]int{"var/lib/hostdeck/backup/data/00/abc": 300, "var/lib/hostdeck/ueberwachung/v.json": 20, "usr/bin/prog": 100, "var/lib/eigen/daten.bin": 200, "opt/app/x": 50} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(strings.Repeat("x", n<<10)), 0o644)
	}
	oldF, oldT := foundThreshold, topMin
	foundThreshold, topMin = 1, 10<<10
	defer func() { foundThreshold, topMin = oldF, oldT }()
	s := &scan{d: Deps{FS: hostfs.New(root), Statfs: func(string) (int64, int64, error) { return 100 << 30, 50 << 30, nil }}, inv: &inventory.Inventory{}}
	s.o.SizeTimeout = time.Minute
	s.space(context.Background())
	got := map[string]string{}
	var order []string
	for _, p := range s.inv.Space.Top {
		got[p.Path] = p.Kind
		order = append(order, p.Path)
	}
	for p, k := range map[string]string{"/var/lib/hostdeck/backup": "hostdeck", "/var/lib/hostdeck/ueberwachung": "hostdeck", "/usr": "system", "/var/lib/eigen": "daten"} {
		if got[p] != k {
			t.Errorf("%s: %q, erwartet %q (%v)", p, got[p], k, order)
		}
	}
	if len(order) == 0 || order[0] != "/var/lib/hostdeck/backup" {
		t.Errorf("Reihenfolge: %v", order)
	}
}
