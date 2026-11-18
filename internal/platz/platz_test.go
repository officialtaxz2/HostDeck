package platz

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("x", n)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMeasureTree(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "opt/app/backups/a.tar.gz"), 3<<20)
	write(t, filepath.Join(root, "opt/app/backups/b.tar.gz"), 2<<20)
	write(t, filepath.Join(root, "opt/app/klein.txt"), 10)
	write(t, filepath.Join(root, "var/log/syslog"), 1<<20)
	if err := os.Link(filepath.Join(root, "opt/app/backups/a.tar.gz"), filepath.Join(root, "opt/hart")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "opt/link")); err != nil {
		t.Fatal(err)
	}
	tr := Measure(context.Background(), "/", Options{Root: root, MinDir: 1})
	if tr.Partial {
		t.Fatal("teilweise")
	}
	opt := tr.Find("/opt")
	if opt == nil || !opt.Dir {
		t.Fatalf("opt fehlt: %+v", opt)
	}
	if opt.Size < 5<<20 || opt.Size > 6<<20 {
		t.Errorf("opt: %d, erwartet gut 5 MB (Hardlink einmal)", opt.Size)
	}
	if opt.Files != 3 {
		t.Errorf("opt Dateien: %d, erwartet 3 (Hardlink einmal, Verknüpfung nicht)", opt.Files)
	}
	if l := tr.Find("/opt/link"); l == nil || l.Link != "/etc" {
		t.Errorf("Verknüpfung: %+v", l)
	}
	bk := tr.Find("/opt/app/backups")
	if bk == nil || len(bk.Kids) != 2 || bk.Kids[0].Name != "a.tar.gz" {
		t.Fatalf("backups: %+v", bk)
	}
	before := tr.Root.Size
	tr.Subtract("/opt/app/backups/a.tar.gz", bk.Kids[0].Size, 1)
	if tr.Find("/opt/app/backups/a.tar.gz") != nil || tr.Root.Size >= before {
		t.Error("Subtract hat nicht abgezogen")
	}
	file := filepath.Join(t.TempDir(), "baum.json.gz")
	if err := tr.Save(file); err != nil {
		t.Fatal(err)
	}
	if got := Load(file); got == nil || got.Find("/var/log/syslog") == nil {
		t.Error("Load")
	}
}

func TestMeasureSkipsMounts(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "mnt/platte/gross"), 2<<20)
	tr := Measure(context.Background(), "/", Options{Root: root, Mounts: []string{"/mnt/platte"}, MinDir: 1})
	e := tr.Find("/mnt/platte")
	if e == nil || !e.Mount || e.Size != 0 {
		t.Fatalf("Einhängepunkt gemessen: %+v", e)
	}
}

func TestMeasurePrunes(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 30; i++ {
		write(t, filepath.Join(root, "viel", "f"+string(rune('a'+i%26))+strings.Repeat("_", i/26)), 100)
	}
	tr := Measure(context.Background(), "/", Options{Root: root, Keep: 5, MinDir: 1})
	v := tr.Find("/viel")
	if v == nil || len(v.Kids) != 5 || v.RestN != 25 || v.RestS != 25*4096 && v.RestS != 25*100 && v.RestS == 0 {
		t.Fatalf("zusammengefasst: %+v", v)
	}
	// Kleine Ordner behalten ihren Inhalt nicht; die Liste misst ihn frisch.
	tr = Measure(context.Background(), "/", Options{Root: root})
	if v := tr.Find("/viel"); v == nil || !v.Flat || len(v.Kids) != 0 {
		t.Fatalf("klein: %+v", v)
	}
}

func facts() Facts {
	return Facts{
		DockerRoot: "/data/docker",
		Mounts:     []string{"/", "/mnt/platte", "/boot/efi"},
		Swaps:      []string{"/swapfile"},
		Own: []Own{
			{Path: "/var/lib/hostdeck", What: "Daten von HostDeck"},
			{Path: "/var/lib/hostdeck/backup", What: "Backups", Page: "#/backup"},
			{Path: "/usr/local/lib/hostdeck", What: "Programm"},
		},
		Projects: []Project{{Name: "web", Dir: "/opt/web", Files: []string{"/opt/web/compose.yaml", "/opt/web/.env"}}},
		Containers: []Container{
			{Name: "wings", Running: true, Binds: []Bind{{Source: "/opt/calagopus/wings/backups", RW: true}, {Source: "/opt/calagopus/config.yml", RW: false}}},
			{Name: "monitor", Running: true, Binds: []Bind{{Source: "/", RW: false}, {Source: "/var/run/docker.sock"}}},
			{Name: "alt", Running: false, Binds: []Bind{{Source: "/srv/bilder", RW: false}}},
		},
	}
}

func TestGuardLevels(t *testing.T) {
	dirs := map[string]bool{"/opt/calagopus/config.yml": false}
	g := NewGuard(facts(), func(p string) bool {
		d, ok := dirs[p]
		return !ok || d
	})
	cases := []struct {
		path  string
		level int
		kind  string
	}{
		{"/", LockSelf, ""},
		{"/etc", LockAll, "System"},
		{"/etc/nginx/site", LockAll, "System"},
		{"/usr/bin", LockAll, "System"},
		{"/usr/local", LockSelf, ""},
		{"/usr/local/bin", LockSelf, ""},
		{"/usr/local/bin/tool", Free, ""},
		{"/usr/local/lib/hostdeck", LockAll, "hostdeck"},
		{"/usr/local/lib/hostdeck/hostdeck", LockAll, "hostdeck"},
		{"/var/lib/hostdeck/backup/x.tar", LockAll, "hostdeck"},
		{"/var/lib/docker", LockAll, "Docker"},
		{"/var/lib/containerd/x", LockAll, "Docker"},
		{"/data/docker/overlay2", LockAll, "Docker"},
		{"/data", LockSelf, ""},
		{"/swapfile", LockAll, "Swap"},
		{"/opt/web/compose.yaml", LockAll, "Projekt web"},
		{"/opt/web/.env", LockAll, "Projekt web"},
		{"/opt/web", LockSelf, "Projekt web"},
		{"/opt/web/daten/alt.log", Free, "Logs"},
		{"/opt", LockSelf, ""},
		{"/opt/calagopus", LockSelf, ""},
		{"/opt/calagopus/wings", LockSelf, ""},
		{"/opt/calagopus/wings/backups", LockSelf, "eingebunden in wings"},
		{"/opt/calagopus/wings/backups/srv1.tar.gz", Free, "Archiv"},
		{"/opt/calagopus/config.yml", LockAll, "eingebunden in wings"},
		{"/mnt/platte", LockSelf, "Einhängepunkt"},
		{"/boot/efi", LockAll, "System"},
		{"/home", LockSelf, ""},
		{"/home/demo", LockSelf, ""},
		{"/home/demo/.ssh", LockAll, "SSH"},
		{"/home/demo/.ssh/authorized_keys", LockAll, "SSH"},
		{"/root/.ssh", LockAll, "SSH"},
		{"/home/demo/film.mkv", Free, ""},
		{"/var/lib/mysql", LockSelf, ""},
		{"/var/lib/mysql/ibdata", Free, ""},
		{"/var/log/syslog.1.gz", Free, "Logs"},
		{"/srv/bilder/a.jpg", Free, "eingebunden in alt"},
		{"/tmp/dump.zip", Free, "Archiv"},
	}
	for _, c := range cases {
		v := g.Check(c.path)
		if v.Level != c.level {
			t.Errorf("%s: Stufe %d (%s), erwartet %d", c.path, v.Level, v.Reason, c.level)
		}
		if c.kind != "" && v.Kind != c.kind {
			t.Errorf("%s: Art %q, erwartet %q", c.path, v.Kind, c.kind)
		}
	}
	g2 := NewGuard(Facts{DockerRoot: "/var/lib/docker", Volumes: []Volume{{Name: "web_db", Path: "/var/lib/docker/volumes/web_db", Users: []string{"web-db-1"}}}}, nil)
	if v := g2.Check("/var/lib/docker/volumes/web_db/_data"); v.Level != LockAll || v.Kind != "Volume web_db · web-db-1" {
		t.Errorf("Volume: %+v", v)
	}
	if v := g.Check("/var/lib/hostdeck/backup"); v.Page != "#/backup" {
		t.Errorf("Seite für Backups: %q", v.Page)
	}
	v := g.Check("/opt/calagopus/wings/backups/srv1.tar.gz")
	if len(v.Warn) != 1 || !strings.Contains(v.Warn[0], "wings (läuft)") || !strings.Contains(v.Warn[0], "im Programm selbst") {
		t.Errorf("Warnung rw: %v", v.Warn)
	}
	v = g.Check("/srv/bilder/a.jpg")
	if len(v.Warn) != 1 || !strings.Contains(v.Warn[0], "Nur lesend") || !strings.Contains(v.Warn[0], "gestoppt") {
		t.Errorf("Warnung ro: %v", v.Warn)
	}
	// Die Einbindung von / (Überwachung) vererbt keine Warnung.
	if v := g.Check("/home/demo/film.mkv"); len(v.Warn) != 0 {
		t.Errorf("breite Einbindung vererbt: %v", v.Warn)
	}
}

func TestInspectHardlinksAndMounts(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a/eins"), 1<<20)
	write(t, filepath.Join(root, "b/zwei"), 1<<20)
	if err := os.Link(filepath.Join(root, "a/eins"), filepath.Join(root, "b/eins-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "a/platte"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sel, err := Inspect(ctx, root, []string{"/b"}, []string{"/a/platte"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sel[0].Frees >= sel[0].Size || sel[0].Frees < 1<<20 {
		t.Errorf("nur b: frei %d, belegt %d; der Hardlink nach a darf nicht zählen", sel[0].Frees, sel[0].Size)
	}
	sel, err = Inspect(ctx, root, []string{"/a", "/b"}, []string{"/a/platte"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total := sel[0].Frees + sel[1].Frees; total < 2<<20 {
		t.Errorf("a und b: frei %d, erwartet beide Dateien", total)
	}
	if len(sel[0].Mounts) != 1 || sel[0].Mounts[0] != "/a/platte" {
		t.Errorf("Einhängepunkt in a: %v", sel[0].Mounts)
	}
	if _, err := Inspect(ctx, root, []string{"/fehlt"}, nil, 0); !errors.Is(err, ErrGone) {
		t.Errorf("fehlt: %v", err)
	}
}
