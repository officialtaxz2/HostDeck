package platz

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/aufraeumen"
)

func newSvc(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "opt/calagopus/wings/backups/srv1.tar.gz"), 3<<20)
	write(t, filepath.Join(root, "opt/calagopus/wings/backups/srv2.tar.gz"), 2<<20)
	write(t, filepath.Join(root, "opt/web/compose.yaml"), 100)
	write(t, filepath.Join(root, "opt/web/daten/gross.bin"), 2<<20)
	write(t, filepath.Join(root, "etc/hosts"), 100)
	write(t, filepath.Join(root, "var/lib/hostdeck/backup/b.tar"), 1<<20)
	write(t, filepath.Join(root, "tmp/klein/a"), 10)
	s := &Service{
		Root: root,
		File: filepath.Join(t.TempDir(), "baum.json.gz"),
		Facts: func(context.Context) Facts {
			return Facts{
				Own:        []Own{{Path: "/var/lib/hostdeck", What: "hostdeck"}},
				Projects:   []Project{{Name: "web", Dir: "/opt/web", Files: []string{"/opt/web/compose.yaml"}}},
				Containers: []Container{{Name: "wings", Running: true, Binds: []Bind{{Source: "/opt/calagopus/wings/backups", RW: true}}}},
			}
		},
		Mounts: func() []string { return []string{"/"} },
		Open:   func() []OpenFile { return nil },
		Disk:   func(string) (int64, int64, error) { return 100 << 30, 50 << 30, nil },
	}
	return s, root
}

func TestServiceListing(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	l, err := s.Listing(ctx, "/")
	if err != nil || !l.NoTree || len(l.Rows) != 0 {
		t.Fatalf("vor der ersten Messung: %+v %v", l, err)
	}
	if err := s.Measure(ctx, "Test"); err != nil {
		t.Fatal(err)
	}
	if m := s.Measuring(); m.Running || m.Seen == 0 {
		t.Errorf("Messung: %+v", m)
	}
	l, err = s.Listing(ctx, "/")
	if err != nil || l.NoTree || l.Live || l.At == nil || len(l.Rows) == 0 || l.Rows[0].Name != "opt" {
		t.Fatalf("/: %+v %v", l, err)
	}
	if l.Disk == nil || l.Self.Guard.Level != LockSelf {
		t.Errorf("/: Platte %v, Schutz %+v", l.Disk, l.Self.Guard)
	}
	l, err = s.Listing(ctx, "/opt/calagopus/wings/backups")
	if err != nil || len(l.Rows) != 2 {
		t.Fatalf("backups: %+v %v", l, err)
	}
	if l.Self.Guard.Level != LockSelf || l.Rows[0].Guard.Level != Free || len(l.Rows[0].Guard.Warn) != 1 || l.Rows[0].Guard.Kind != "Archiv" {
		t.Errorf("Schutz: %+v / %+v", l.Self.Guard, l.Rows[0].Guard)
	}
	// Kleine Ordner misst die Liste frisch.
	l, err = s.Listing(ctx, "/tmp/klein")
	if err != nil || !l.Live || len(l.Rows) != 1 || l.Rows[0].Name != "a" {
		t.Fatalf("klein: %+v %v", l, err)
	}
	if _, err := s.Listing(ctx, "/gibtsnicht"); !errors.Is(err, ErrGone) {
		t.Errorf("fehlt: %v", err)
	}
	if _, err := s.Listing(ctx, "/etc/hosts"); !errors.Is(err, ErrNotDir) {
		t.Errorf("Datei: %v", err)
	}
	// Der gemerkte Baum übersteht einen Neustart.
	s2 := &Service{Root: s.Root, File: s.File}
	if s2.Tree() == nil {
		t.Error("Baum nicht gespeichert")
	}
}

func TestServicePlanBlocks(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	p, err := s.Plan(ctx, []string{"/etc", "/opt/web", "/opt/web/compose.yaml", "/var/lib/hostdeck/backup/b.tar", "/opt/calagopus/wings/backups", "/opt/calagopus/wings/backups/srv1.tar.gz", "/opt/calagopus/wings/backups/srv1.tar.gz", "/opt/web/daten", "/opt/web/daten/gross.bin", "/weg"})
	if err != nil {
		t.Fatal(err)
	}
	blocked := map[string]string{}
	for _, b := range p.Blocked {
		blocked[b.Path] = b.Why
	}
	for _, w := range []string{"/etc", "/opt/web", "/opt/web/compose.yaml", "/var/lib/hostdeck/backup/b.tar", "/opt/calagopus/wings/backups", "/weg"} {
		if blocked[w] == "" {
			t.Errorf("%s nicht gesperrt", w)
		}
	}
	if len(p.Items) != 2 || p.Stand == "" {
		t.Fatalf("Einträge: %+v Stand %q", p.Items, p.Stand)
	}
	// /opt/web/daten/gross.bin liegt in /opt/web/daten und kommt mit.
	if !strings.Contains(strings.Join(p.Notes, " "), "kommt damit mit") {
		t.Errorf("Hinweise: %v", p.Notes)
	}
	var warned bool
	for _, it := range p.Items {
		if it.Path == "/opt/calagopus/wings/backups/srv1.tar.gz" && len(it.Warn) > 0 && strings.Contains(it.Warn[0], "wings") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("keine Warnung zum Container: %+v", p.Items)
	}
	if !strings.Contains(p.TrashWhy, "Papierkorb") || p.TrashOK {
		t.Errorf("ohne Papierkorb: %q", p.TrashWhy)
	}
	if _, err := s.Plan(ctx, nil); !errors.Is(err, ErrNothing) {
		t.Errorf("leer: %v", err)
	}
}

func TestServiceRun(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Löschen nur unter Linux")
	}
	s, root := newSvc(t)
	ctx := context.Background()
	if err := s.Measure(ctx, "Test"); err != nil {
		t.Fatal(err)
	}
	var unselected []string
	s.Unselect = func(p []string) (int, error) { unselected = append(unselected, p...); return 1, nil }
	if _, err := s.Run(ctx, "falsch", false, nil); !errors.Is(err, ErrExpired) {
		t.Fatalf("falscher Stand: %v", err)
	}
	p, err := s.Plan(ctx, []string{"/opt/calagopus/wings/backups/srv1.tar.gz", "/opt/web/daten"})
	if err != nil || p.Stand == "" {
		t.Fatal(p, err)
	}
	before := s.Tree().Root.Size
	msg, err := s.Run(ctx, p.Stand, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Gelöscht: 2 Einträge") || !strings.Contains(msg, "1 Eintrag aus deiner Auswahl") {
		t.Errorf("Ergebnis: %s", msg)
	}
	for _, p := range []string{"opt/calagopus/wings/backups/srv1.tar.gz", "opt/web/daten"} {
		if _, err := os.Lstat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s noch da", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "opt/calagopus/wings/backups/srv2.tar.gz")); err != nil {
		t.Error("Nachbar gelöscht")
	}
	sort.Strings(unselected)
	if strings.Join(unselected, ",") != "/opt/calagopus/wings/backups/srv1.tar.gz,/opt/web/daten" {
		t.Errorf("ausgetragen: %v", unselected)
	}
	tr := s.Tree()
	if tr.Find("/opt/web/daten") != nil || tr.Root.Size >= before-(4<<20) {
		t.Errorf("Baum nicht nachgeführt: %d → %d", before, tr.Root.Size)
	}
	if Load(s.File).Find("/opt/web/daten") != nil {
		t.Error("gespeicherter Baum nicht nachgeführt")
	}
	// Ein Stand gilt nur einmal.
	if _, err := s.Run(ctx, p.Stand, false, nil); !errors.Is(err, ErrExpired) {
		t.Errorf("zweimal: %v", err)
	}
}

func TestServiceRunChecksAgain(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Löschen nur unter Linux")
	}
	s, root := newSvc(t)
	ctx := context.Background()
	// Ersetzt seit der Vorschau.
	p, _ := s.Plan(ctx, []string{"/opt/web/daten/gross.bin"})
	write(t, filepath.Join(root, "opt/web/daten/neu"), 10)
	os.Rename(filepath.Join(root, "opt/web/daten/neu"), filepath.Join(root, "opt/web/daten/gross.bin"))
	if _, err := s.Run(ctx, p.Stand, false, nil); !errors.Is(err, ErrChanged) {
		t.Fatalf("ersetzt: %v", err)
	}
	// Gewachsen seit der Vorschau (mehr als 10 % und 50 MB).
	write(t, filepath.Join(root, "tmp/wachsen/a"), 1<<20)
	p, _ = s.Plan(ctx, []string{"/tmp/wachsen"})
	write(t, filepath.Join(root, "tmp/wachsen/b"), 60<<20)
	if _, err := s.Run(ctx, p.Stand, false, nil); err == nil || !strings.Contains(err.Error(), "gewachsen") {
		t.Fatalf("gewachsen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tmp/wachsen/a")); err != nil {
		t.Fatal("trotzdem gelöscht")
	}
	// Inzwischen geschützt (etwa ein neuer Container bindet es ein).
	p, _ = s.Plan(ctx, []string{"/tmp/wachsen"})
	facts := s.Facts
	s.Facts = func(ctx context.Context) Facts {
		f := facts(ctx)
		f.Containers = append(f.Containers, Container{Name: "neu", Binds: []Bind{{Source: "/tmp/wachsen"}}})
		return f
	}
	if _, err := s.Run(ctx, p.Stand, false, nil); err == nil || !strings.Contains(err.Error(), "eingebunden") {
		t.Fatalf("inzwischen geschützt: %v", err)
	}
	// Abgelaufen.
	s.Facts = facts
	p, _ = s.Plan(ctx, []string{"/tmp/wachsen"})
	s.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := s.Run(ctx, p.Stand, false, nil); !errors.Is(err, ErrExpired) {
		t.Fatalf("abgelaufen: %v", err)
	}
}

func TestHidden(t *testing.T) {
	h := hidden([]OpenFile{
		{Path: "/var/log/alt.log", Proc: "rsyslogd", PID: 10, Deleted: true, Size: 3 << 20, Dev: 1},
		{Path: "/var/log/alt.log", Proc: "rsyslogd", PID: 10, Deleted: true, Size: 3 << 20, Dev: 1},
		{Path: "/tmp/x", Proc: "java", PID: 20, Deleted: true, Size: 10 << 20, Dev: 1},
		{Path: "/tmp/klein", Proc: "sh", PID: 30, Deleted: true, Size: 10, Dev: 1},
		{Path: "/etc/passwd", Proc: "sh", PID: 30},
	})
	if len(h) != 2 || h[0].Proc != "java" || h[1].Size != 3<<20 || h[1].Files != 1 {
		t.Errorf("%+v", h)
	}
	w := openWarnings("/var/log", []OpenFile{{Path: "/var/log/syslog", Proc: "rsyslogd"}, {Path: "/var/logs/x"}})
	if len(w) != 1 || !strings.Contains(w[0], "rsyslogd") || strings.Contains(w[0], "logs") {
		t.Errorf("%v", w)
	}
}

func TestServiceRunTrash(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Löschen nur unter Linux")
	}
	bin := os.Getenv("HOSTDECK_RESTIC")
	if bin == "" {
		t.Skip("HOSTDECK_RESTIC nicht gesetzt")
	}
	s, root := newSvc(t)
	ctx := context.Background()
	dir := t.TempDir()
	s.Trash = &aufraeumen.Service{
		Trash: &aufraeumen.ResticTrash{Dir: dir, Host: "test", Bin: func(context.Context) (string, error) { return bin, nil }},
		Dir:   dir,
		Disk:  s.Disk,
	}
	p, err := s.Plan(ctx, []string{"/opt/web/daten"})
	if err != nil || !p.TrashOK || p.TrashUntil == "" {
		t.Fatalf("%+v %v", p, err)
	}
	msg, err := s.Run(ctx, p.Stand, true, nil)
	if err != nil || !strings.Contains(msg, "Im Papierkorb bis") {
		t.Fatalf("%s %v", msg, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "opt/web/daten")); !os.IsNotExist(err) {
		t.Fatal("nicht gelöscht")
	}
	var es []aufraeumen.Entry
	b, _ := os.ReadFile(filepath.Join(dir, "inhalt.json"))
	if err := json.Unmarshal(b, &es); err != nil || len(es) != 1 || es[0].Path != filepath.Join(root, "opt/web/daten") {
		t.Fatalf("Papierkorb: %s %v", b, err)
	}
	if _, err := s.Trash.Restore(ctx, es[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "opt/web/daten/gross.bin")); err != nil || fi.Size() != 2<<20 {
		t.Fatalf("zurück: %v %v", fi, err)
	}
}

// Über eine Verknüpfung bekannte Orte sind auch am echten Ort geschützt.
func TestSchutzUeberVerknuepfung(t *testing.T) {
	s, root := newSvc(t)
	write(t, filepath.Join(root, "data/stacks/shop/compose.yaml"), 10)
	write(t, filepath.Join(root, "data/stacks/shop/daten/a"), 10)
	write(t, filepath.Join(root, "data/wings/backups/b.tar"), 10)
	write(t, filepath.Join(root, "data/hostdeck/x"), 10)
	for link, target := range map[string]string{"home/u/stacks": "../../data/stacks", "srv/wings": "../data/wings", "var/lib/hostdeck2": "../../data/hostdeck"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, link)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	s.Facts = func(context.Context) Facts {
		return Facts{
			Own:        []Own{{Path: "/var/lib/hostdeck2", What: "hostdeck"}},
			Projects:   []Project{{Name: "shop", Dir: "/home/u/stacks/shop", Files: []string{"/home/u/stacks/shop/compose.yaml"}}},
			Containers: []Container{{Name: "wings", Running: true, Binds: []Bind{{Source: "/srv/wings/backups", RW: true}}}},
		}
	}
	g := s.guardFor(context.Background(), true)
	for p, want := range map[string]int{
		"/data/stacks/shop":              LockSelf,
		"/data/stacks/shop/compose.yaml": LockAll,
		"/data/stacks":                   LockSelf,
		"/data/wings/backups":            LockSelf,
		"/data/hostdeck/x":               LockAll,
		"/data/stacks/shop/daten":        Free,
	} {
		if v := g.Check(p); v.Level != want {
			t.Errorf("%s: Stufe %d, erwartet %d (%s)", p, v.Level, want, v.Reason)
		}
	}
	if v := g.Check("/data/wings/backups/b.tar"); v.Level != Free || len(v.Warn) == 0 {
		t.Errorf("Inhalt der Einbindung: %+v", v)
	}
	if v := g.Check("/data/stacks/shop/daten"); v.Kind != "Projekt shop" {
		t.Errorf("Art: %+v", v)
	}
}

// Ein Volume mit o=bind schützt seinen Ordner.
func TestBindVolumeGeschuetzt(t *testing.T) {
	g := NewGuard(Facts{
		DockerRoot: "/var/lib/docker",
		Volumes:    []Volume{{Name: "daten", Path: "/var/lib/docker/volumes/daten", Device: "/opt/daten", Users: []string{"app"}}},
		Containers: []Container{{Name: "app", Running: true, Binds: []Bind{{Source: "/opt/daten", RW: true}}}},
	}, nil)
	if v := g.Check("/opt/daten"); v.Level != LockSelf || v.Kind != "Volume daten" {
		t.Errorf("Ordner des Volumes: %+v", v)
	}
	if v := g.Check("/opt/daten/x"); v.Level != Free || len(v.Warn) == 0 {
		t.Errorf("Inhalt: %+v", v)
	}
	// Auch ohne Container bleibt der Ordner selbst.
	g = NewGuard(Facts{Volumes: []Volume{{Name: "alt", Device: "/srv/alt"}}}, nil)
	if v := g.Check("/srv/alt"); v.Level != LockSelf {
		t.Errorf("Volume ohne Container: %+v", v)
	}
}

// Antwortet Docker nicht, zeigt die Liste das und HostDeck löscht und holt
// nichts zurück.
func TestSchutzUnvollstaendig(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	facts := s.Facts
	p, err := s.Plan(ctx, []string{"/opt/web/daten"})
	if err != nil || p.Stand == "" {
		t.Fatalf("%+v %v", p, err)
	}
	s.Facts = func(ctx context.Context) Facts {
		f := facts(ctx)
		f.Incomplete = "Docker antwortet gerade nicht"
		return f
	}
	if _, err := s.Run(ctx, p.Stand, false, nil); !errors.Is(err, ErrIncomplete) {
		t.Errorf("Run: %v", err)
	}
	if _, err := s.Plan(ctx, []string{"/opt/web/daten"}); !errors.Is(err, ErrIncomplete) {
		t.Errorf("Plan: %v", err)
	}
	if err := s.CheckRestore(ctx, "/opt/web/daten"); !errors.Is(err, ErrIncomplete) {
		t.Errorf("CheckRestore: %v", err)
	}
	l, err := s.Listing(ctx, "/opt")
	if err != nil || !strings.Contains(l.Problem, "Docker") {
		t.Errorf("Listing: %q %v", l.Problem, err)
	}
}

// Zurückholen nur an Orte, die sich auch löschen ließen.
func TestCheckRestore(t *testing.T) {
	s, root := newSvc(t)
	ctx := context.Background()
	if err := os.Symlink("web", filepath.Join(root, "opt/umweg")); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckRestore(ctx, "/opt/web/daten"); err != nil {
		t.Errorf("frei: %v", err)
	}
	if err := s.CheckRestore(ctx, "/opt/calagopus/wings/backups/neu"); err != nil {
		t.Errorf("Inhalt einer Einbindung: %v", err)
	}
	for _, p := range []string{"/opt/web", "/opt/web/compose.yaml", "/var/lib/hostdeck/backup", "/etc/x"} {
		if err := s.CheckRestore(ctx, p); err == nil {
			t.Errorf("%s: nicht abgelehnt", p)
		}
	}
	if err := s.CheckRestore(ctx, "/opt/umweg/daten"); !errors.Is(err, ErrViaLink) {
		t.Errorf("über Verknüpfung: %v", err)
	}
}

// Pfade durch eine Verknüpfung nimmt weder die Liste noch die Vorschau an.
func TestPfadDurchVerknuepfung(t *testing.T) {
	s, root := newSvc(t)
	ctx := context.Background()
	if err := os.Symlink("web", filepath.Join(root, "opt/umweg")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Listing(ctx, "/opt/umweg/daten"); !errors.Is(err, ErrViaLink) {
		t.Errorf("Listing: %v", err)
	}
	p, err := s.Plan(ctx, []string{"/opt/umweg/daten"})
	if err != nil || p.Stand != "" || len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0].Why, "Verknüpfung") {
		t.Errorf("Plan: %+v %v", p, err)
	}
	// Die Verknüpfung selbst lässt sich löschen.
	p, err = s.Plan(ctx, []string{"/opt/umweg"})
	if err != nil || p.Stand == "" {
		t.Errorf("Verknüpfung selbst: %+v %v", p, err)
	}
}

// Der Papierkorb geht nur auf derselben Platte.
func TestPapierkorbAndererPlatte(t *testing.T) {
	s, _ := newSvc(t)
	dir := t.TempDir()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, dev, _, _ := statOf(fi)
	if why := s.otherDisk(dir, []Sel{{Path: "/a", Ident: Ident{Dev: dev}}}); why != "" {
		t.Errorf("gleiche Platte: %s", why)
	}
	if runtime.GOOS == "linux" {
		if why := s.otherDisk(dir, []Sel{{Path: "/a", Ident: Ident{Dev: dev + 1}}}); !strings.Contains(why, "anderen Platte") {
			t.Errorf("andere Platte: %q", why)
		}
	}
}

// Dateien direkt unter / gehören zum System.
func TestDateienUnterWurzel(t *testing.T) {
	g := NewGuard(Facts{}, func(p string) bool { return p != "/vmlinuz" && p != "/initrd.img" })
	for _, p := range []string{"/vmlinuz", "/initrd.img", "/bin.usr-is-merged", "/bin.usr-is-merged/x"} {
		if v := g.Check(p); v.Level != LockAll {
			t.Errorf("%s: %+v", p, v)
		}
	}
	if v := g.Check("/daten"); v.Level == LockAll {
		t.Errorf("/daten: %+v", v)
	}
}

// Neues, Zurückgeholtes und Verschwundenes zeigt die Liste ohne neue
// Messung.
func TestListeGleichtAb(t *testing.T) {
	s, root := newSvc(t)
	ctx := context.Background()
	if err := s.Measure(ctx, "Test"); err != nil {
		t.Fatal(err)
	}
	size := func(dir, name string) int64 {
		t.Helper()
		l, err := s.Listing(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range l.Rows {
			if r.Name == name {
				return r.Size
			}
		}
		return -1
	}
	webBefore := size("/opt", "web")
	// Neu angelegt: erscheint in /opt/web, und /opt zählt es mit.
	write(t, filepath.Join(root, "opt/web/neu/gross.bin"), 3<<20)
	if n := size("/opt/web", "neu"); n < 3<<20 {
		t.Errorf("neuer Ordner: %d", n)
	}
	if n := size("/opt", "web"); n < webBefore+3<<20 {
		t.Errorf("/opt/web nicht nachgeführt: %d (vorher %d)", n, webBefore)
	}
	// Verschwunden: ist heraus.
	if err := os.RemoveAll(filepath.Join(root, "opt/web/daten")); err != nil {
		t.Fatal(err)
	}
	if n := size("/opt/web", "daten"); n != -1 {
		t.Errorf("verschwundener Ordner noch da: %d", n)
	}
	// Zurückgeholt (umbenannt an den alten Ort): Changed führt alles darüber
	// nach, ohne dass jemand den Ordner öffnet.
	tmp := filepath.Join(root, "tmp/zurueck")
	write(t, filepath.Join(tmp, "x.bin"), 2<<20)
	before := size("/", "opt")
	if err := os.Rename(tmp, filepath.Join(root, "opt/calagopus/wings/backups/zurueck")); err != nil {
		t.Fatal(err)
	}
	s.Changed(ctx, filepath.Join(root, "opt/calagopus/wings/backups/zurueck"))
	if n := s.Tree().Find("/opt").Size; n < before+2<<20 {
		t.Errorf("/opt nach dem Zurückholen: %d (vorher %d)", n, before)
	}
	if e := s.Tree().Find("/opt/calagopus/wings/backups/zurueck"); e == nil || e.Size < 2<<20 {
		t.Errorf("zurückgeholter Ordner fehlt im Baum: %+v", e)
	}
	// Der Abgleich ist gemerkt: Der Baum auf der Platte kennt es auch.
	if e := Load(s.File).Find("/opt/web/neu"); e == nil {
		t.Error("Baum nicht gespeichert")
	}
}

// Dateien unter / mit eigenem Grund, Warnung für
// Daten von Diensten, SSH-Zugänge in jedem Home-Ordner.
func TestSchutzDiensteUndHomes(t *testing.T) {
	g := NewGuard(Facts{Homes: map[string]string{"git": "/srv/git", "root": "/root", "nobody": "/nonexistent", "x": "/"}}, func(p string) bool { return p != "/backup.tar.gz" })
	if v := g.Check("/backup.tar.gz"); v.Level != LockAll || !strings.Contains(v.Reason, "direkt unter /") || v.Kind != "Archiv" {
		t.Errorf("/backup.tar.gz: %+v", v)
	}
	if v := g.Check("/var/lib/caddy/certs/a.pem"); v.Level != Free || len(v.Warn) != 1 || !strings.Contains(v.Warn[0], "caddy") {
		t.Errorf("Dienst: %+v", v)
	}
	if v := g.Check("/var/lib/caddy"); v.Level != LockSelf {
		t.Errorf("/var/lib/caddy: %+v", v)
	}
	if v := g.Check("/srv/git/.ssh/authorized_keys"); v.Level != LockAll {
		t.Errorf("SSH im Home: %+v", v)
	}
	if v := g.Check("/srv/git"); v.Level != LockSelf {
		t.Errorf("Home: %+v", v)
	}
	if v := g.Check("/srv/git/repos"); v.Level != Free {
		t.Errorf("Inhalt des Home: %+v", v)
	}
}

// Text für Einhängepunkte.
func TestMountWhy(t *testing.T) {
	if s := mountWhy(Sel{Path: "/mnt/extra/x.bin", Mounts: []string{"/mnt/extra/x.bin"}}); strings.Contains(s, "darin hängt") {
		t.Errorf("selbst eingehängt: %s", s)
	}
	if s := mountWhy(Sel{Path: "/srv", Mounts: []string{"/srv/daten"}}); !strings.Contains(s, "darin hängt /srv/daten") {
		t.Errorf("darin: %s", s)
	}
}

// Ein geänderter Eintrag mit zu vielen Einträgen behält seinen alten Wert;
// die übrigen gleicht die Liste trotzdem ab (Nachprüfung ui11, NP1). Dazu
// lesen Listen gleichzeitig mit dem Abgleich (NP2, mit -race).
func TestAbgleichMitGrossemNachbarn(t *testing.T) {
	old := refreshLimit
	refreshLimit = 5
	defer func() { refreshLimit = old }()
	s, root := newSvc(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		write(t, filepath.Join(root, "opt/viele", "f"+strings.Repeat("x", i)), 10)
	}
	write(t, filepath.Join(root, "opt/viele/gross.bin"), 2<<20)
	if err := s.Measure(ctx, "Test"); err != nil {
		t.Fatal(err)
	}
	vieleBefore := s.Tree().Find("/opt/viele").Size
	write(t, filepath.Join(root, "opt/viele/neu"), 10) // viele ist geändert
	write(t, filepath.Join(root, "opt/neu/a.bin"), 2<<20)
	done := make(chan bool)
	go func() {
		for i := 0; i < 20; i++ {
			_, _ = s.Listing(ctx, "/opt")
		}
		done <- true
	}()
	l, err := s.Listing(ctx, "/opt")
	<-done
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int64{}
	for _, r := range l.Rows {
		found[r.Name] = r.Size
	}
	if found["neu"] < 2<<20 {
		t.Errorf("neuer Ordner fehlt: %v", found)
	}
	if found["viele"] != vieleBefore {
		t.Errorf("viele: %d, alter Wert %d", found["viele"], vieleBefore)
	}
	s.mu.Lock()
	_, ok := s.refreshed["/opt"]
	s.mu.Unlock()
	if !ok {
		t.Error("Abgleich nicht gemerkt")
	}
}
