package backup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
	"github.com/officialtaxz2/hostdeck/internal/pc"
	"github.com/officialtaxz2/hostdeck/internal/restic"
	"github.com/officialtaxz2/hostdeck/internal/runlog"
)

func resticBin(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HOSTDECK_RESTIC")
	if p == "" {
		var err error
		if p, err = exec.LookPath("restic"); err != nil {
			t.Skip("restic nicht gefunden (HOSTDECK_RESTIC setzen)")
		}
	}
	if v, err := restic.BinaryVersion(context.Background(), p); err != nil || restic.CheckVersion(v) != nil {
		t.Skipf("restic %s zu alt oder nicht startbar", v)
	}
	return p
}

// miniServer baut einen kleinen Server in einem Temp-Ordner nach: ein
// Projekt „shop“ mit App, Postgres-Datenbank im Volume und einem
// Daten-Ordner, dazu ein altes Verzeichnis fürs Archiv.
type miniServer struct {
	root, result, state string
	inv                 *inventory.Inventory
}

func newMiniServer(t *testing.T) *miniServer {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	shop := filepath.Join(root, "opt", "shop")
	for dir, files := range map[string]map[string]string{
		shop:                                {"compose.yaml": "services: {}\n"},
		filepath.Join(shop, "data"):         {"bestellungen.json": "[1,2,3]\n"},
		filepath.Join(root, "vol", "_data"): {"PG_VERSION": "17\n"},
		filepath.Join(root, "archiv"):       {"alt.txt": "alt\n"},
	} {
		must(os.MkdirAll(dir, 0o755))
		for name, body := range files {
			must(os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
		}
	}
	db := &inventory.DB{Engine: "postgres", Version: "17", Via: "image", DataKey: "volume:shop_db"}
	inv := &inventory.Inventory{
		Version: inventory.SchemaVersion, CreatedAt: time.Now().UTC(),
		Host: inventory.Host{Hostname: "testhost", Root: true},
		Projects: []inventory.Project{{Key: "project:shop", Name: "shop", WorkingDir: shop, Path: shop, DirExists: true,
			Services: []string{"app", "db"}, Containers: []string{"compose:shop/app", "compose:shop/db"}, Running: 2}},
		Containers: []inventory.Container{
			{Key: "compose:shop/app", Name: "shop-app-1", ID: "app1", Project: "project:shop", Service: "app", State: "running", Running: true,
				Image: "registry.example/shop:1", ImageID: "sha256:app", ImageOrigin: inventory.OriginRegistry,
				Mounts: []inventory.Mount{{Type: "bind", Source: filepath.Join(shop, "data"), Destination: "/data", RW: true, DataKey: "path:" + filepath.Join(shop, "data")}}},
			{Key: "compose:shop/db", Name: "shop-db-1", ID: "db1", Project: "project:shop", Service: "db", State: "running", Running: true,
				Image: "postgres:17", ImageID: "sha256:pg", ImageOrigin: inventory.OriginRegistry, DB: db,
				Mounts: []inventory.Mount{{Type: "volume", Volume: "shop_db", Destination: "/var/lib/postgresql/data", RW: true, DataKey: "volume:shop_db"}}},
		},
		Volumes: []inventory.Volume{{Key: "volume:shop_db", Name: "shop_db", Driver: "local", Mountpoint: filepath.Join(root, "vol", "_data"),
			Project: "project:shop", UsedBy: []string{"compose:shop/db"}, Size: 3, DB: db}},
		Images: []inventory.Image{
			{ID: "sha256:app", Tags: []string{"registry.example/shop:1"}, Origin: inventory.OriginRegistry, UsedBy: []string{"compose:shop/app"}},
			{ID: "sha256:pg", Tags: []string{"postgres:17"}, Origin: inventory.OriginRegistry, UsedBy: []string{"compose:shop/db"}},
		},
		Paths: []inventory.Path{
			{Key: "path:" + shop, Path: shop, Kind: inventory.KindProject, Owner: "project:shop", Exists: true, IsDir: true, Size: 20},
			{Key: "path:" + filepath.Join(shop, "data"), Path: filepath.Join(shop, "data"), Kind: inventory.KindData, Owner: "project:shop",
				Exists: true, IsDir: true, Size: 8, Mounts: []inventory.MountRef{{Container: "compose:shop/app", Destination: "/data", RW: true}}},
		},
		Unclaimed: []inventory.Unclaimed{{Key: "path:" + filepath.Join(root, "archiv"), Path: filepath.Join(root, "archiv"), Size: 4}},
	}
	s := &miniServer{root: root, result: filepath.Join(root, "hostdeck-ergebnis"), state: filepath.Join(root, "state"), inv: inv}
	m := manifest.Propose(inv)
	if open := backup.OpenEntries(m); len(open) > 0 {
		t.Fatalf("Vorschlag noch offen: %v", open)
	}
	if d := m.Projects["shop"].Data["volume:shop_db"]; d == nil || d.Strategy != "dump" {
		t.Fatalf("Vorschlag ohne Dump: %+v", m.Projects["shop"].Data)
	}
	b, err := m.Marshal()
	must(err)
	must(os.MkdirAll(s.result, 0o700))
	must(os.WriteFile(filepath.Join(s.result, "manifest.yaml"), b, 0o600))
	return s
}

func (s *miniServer) job(t *testing.T, repo *restic.Repo, in backup.Input, out *bytes.Buffer, d *fakeDocker) *backup.Job {
	return &backup.Job{
		Version: "test", ResultDir: s.result, StateDir: s.state,
		Discover: func(context.Context) (*inventory.Inventory, error) { return s.inv, nil },
		Docker:   d, Repo: repo, PasswordFile: repo.PasswordFile, Input: in, Out: out,
		Self:   []string{"sh", "-c", `printf 'CREATE TABLE bestellungen;\n-- PostgreSQL database cluster dump complete\n'`},
		Settle: time.Millisecond, StartWait: 10 * time.Millisecond, Poll: time.Millisecond,
	}
}

func TestBackupLaufAufDenPC(t *testing.T) {
	bin := resticBin(t)
	s := newMiniServer(t)
	pcDir := t.TempDir()
	rc, err := pc.NewReceiver(pc.RepoDir(pcDir, "testhost"), filepath.Join(pcDir, "berichte"), "hostdeck", "k", "n")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	repo := &restic.Repo{Bin: bin, URL: "rest:" + srv.URL + "/", PasswordFile: filepath.Join(s.state, "passwort"),
		RestUser: "hostdeck", RestPass: "k", CacheDir: filepath.Join(s.state, "cache"), Host: "testhost"}
	ctx := context.Background()
	l := &eventLog{}
	d := &fakeDocker{log: l, running: map[string]bool{"app1": true, "db1": true}}

	// Erster Lauf: Passwort entsteht, wird einmal gezeigt und bestätigt.
	var out, screen bytes.Buffer
	var sent []byte
	j := s.job(t, repo, &fakeInput{answers: []string{"nein", "ja"}}, &out, d)
	j.Screen = &screen
	j.SendReport = func(_ context.Context, run string, body []byte) error { sent = body; return nil }
	stoppedAt := time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC)
	j.UserStopped = func() map[string]time.Time { return map[string]time.Time{"alt-app": stoppedAt} }
	if code := j.Run(ctx); code != backup.ExitOK {
		t.Fatalf("Exit %d:\n%s", code, out.String())
	}
	pw, err := os.ReadFile(repo.PasswordFile)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(repo.PasswordFile); fi.Mode().Perm() != 0o600 {
		t.Errorf("Passwortdatei mit Rechten %v", fi.Mode().Perm())
	}
	if !strings.Contains(screen.String(), strings.TrimSpace(string(pw))) {
		t.Error("neues Passwort nicht angezeigt")
	}
	if strings.Contains(out.String(), strings.TrimSpace(string(pw))) {
		t.Error("neues Passwort steht im Protokoll")
	}
	if _, err := os.Stat(repo.PasswordFile + "-unbestaetigt"); err == nil {
		t.Error("Passwort bestätigt, aber noch als unbestätigt markiert")
	}
	rep, err := os.ReadFile(filepath.Join(s.result, "backup-bericht.txt"))
	if err != nil || !strings.Contains(string(rep), "GÜLTIG") || !bytes.Equal(rep, sent) {
		t.Errorf("Bericht fehlt, ist ungültig oder kam nicht beim PC an:\n%s", rep)
	}
	if strings.Contains(string(rep), strings.TrimSpace(string(pw))) {
		t.Error("Passwort steht im Bericht")
	}
	runs, err := runlog.Load(s.result)
	if err != nil || len(runs) != 1 || !runs[0].Valid || !strings.HasPrefix(runs[0].Verdict, "GÜLTIG:") || runs[0].Items == 0 || runs[0].Size == 0 {
		t.Errorf("Lauf fehlt in %s: %v %+v", runlog.File, err, runs)
	}
	if !d.running["app1"] || !d.running["db1"] {
		t.Error("Container laufen nach dem Lauf nicht wieder")
	}
	before(t, l, "stop app1", "stop db1")

	// Lauf-Datei im Backup: jeder Eintrag mit Snapshot.
	snaps, err := repo.Snapshots(ctx, "sbm-lauf")
	if err != nil || len(snaps) != 1 {
		t.Fatalf("Snapshot der Lauf-Datei: %v %v", snaps, err)
	}
	runDirs, _ := filepath.Glob(filepath.Join(s.state, "laeufe", "*", "lauf.json"))
	if len(runDirs) != 1 {
		t.Fatalf("lauf.json: %v", runDirs)
	}
	var rf backup.RunFile
	b, _ := os.ReadFile(runDirs[0])
	if err := json.Unmarshal(b, &rf); err != nil || !rf.Valid {
		t.Fatalf("lauf.json ungültig: %v %+v", err, rf)
	}
	if !rf.UserStopped["alt-app"].Equal(stoppedAt) {
		t.Errorf("von dir gestoppt fehlt in lauf.json: %v", rf.UserStopped)
	}
	keys := map[string]bool{}
	for _, sn := range rf.Snapshots {
		if sn.Snapshot == "" {
			t.Errorf("%s ohne Snapshot", sn.Key)
		}
		keys[sn.Key] = true
	}
	for _, k := range []string{"project:shop", "volume:shop_db", "path:" + filepath.Join(s.root, "opt", "shop", "data"), "path:" + filepath.Join(s.root, "archiv")} {
		if !keys[k] {
			t.Errorf("%s fehlt in lauf.json", k)
		}
	}

	// Zweiter Lauf mit gespeichertem Passwort.
	out.Reset()
	time.Sleep(1100 * time.Millisecond) // neue Lauf-ID
	if code := s.job(t, repo, &fakeInput{}, &out, d).Run(ctx); code != backup.ExitOK {
		t.Fatalf("zweiter Lauf: Exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Backup-Ordner und Passwort passen") {
		t.Errorf("zweiter Lauf fragt neu nach dem Passwort:\n%s", out.String())
	}

	// Wurde das Notieren nicht bestätigt (Fenster zu), zeigt der nächste
	// Lauf das Passwort noch einmal.
	if err := os.WriteFile(repo.PasswordFile+"-unbestaetigt", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	time.Sleep(1100 * time.Millisecond)
	if code := s.job(t, repo, &fakeInput{answers: []string{"ja"}}, &out, d).Run(ctx); code != backup.ExitOK {
		t.Fatalf("Lauf nach unbestätigtem Passwort: Exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "nicht bestätigt") || !strings.Contains(out.String(), strings.TrimSpace(string(pw))) {
		t.Errorf("Passwort nicht erneut gezeigt:\n%s", out.String())
	}
	if _, err := os.Stat(repo.PasswordFile + "-unbestaetigt"); err == nil {
		t.Error("Markierung nach Bestätigung nicht entfernt")
	}

	// Server neu aufgesetzt: Passwortdatei fehlt, Backup gibt es schon.
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Remove(repo.PasswordFile))
	out.Reset()
	time.Sleep(1100 * time.Millisecond)
	in := &fakeInput{answers: []string{"falsch", strings.TrimSpace(string(pw))}}
	if code := s.job(t, repo, in, &out, d).Run(ctx); code != backup.ExitOK {
		t.Fatalf("Lauf nach Neuaufsetzen: Exit %d:\n%s", code, out.String())
	}
	if len(in.asked) < 2 || !strings.Contains(out.String(), "Das Passwort passt nicht") {
		t.Errorf("falsches Passwort nicht erkannt:\n%s", out.String())
	}
}

func TestVorpruefungStopptBeiOffenemEintrag(t *testing.T) {
	s := newMiniServer(t)
	b, _ := os.ReadFile(filepath.Join(s.result, "manifest.yaml"))
	b = bytes.Replace(b, []byte("strategy: archive"), []byte("strategy: open"), 1)
	os.WriteFile(filepath.Join(s.result, "manifest.yaml"), b, 0o600)
	var out bytes.Buffer
	l := &eventLog{}
	d := &fakeDocker{log: l, running: map[string]bool{"app1": true, "db1": true}}
	repo := &restic.Repo{PasswordFile: filepath.Join(s.state, "passwort")}
	if code := s.job(t, repo, &fakeInput{}, &out, d).Run(context.Background()); code != backup.ExitBlocked {
		t.Fatalf("Exit %d, erwartet %d:\n%s", code, backup.ExitBlocked, out.String())
	}
	if !strings.Contains(out.String(), "noch 1 offen") || len(l.list()) > 0 {
		t.Errorf("offener Eintrag nicht gemeldet oder trotzdem gestoppt:\n%s\n%v", out.String(), l.list())
	}
	if _, err := os.Stat(repo.PasswordFile); err == nil {
		t.Error("Passwortdatei trotz abgebrochener Vorprüfung angelegt")
	}

	// Ein neuer Container seit der Auswahl verhindert den Lauf ebenfalls.
	s2 := newMiniServer(t)
	s2.inv.Containers = append(s2.inv.Containers, inventory.Container{Key: "container:neu", Name: "neu", ID: "neu1", Running: true})
	out.Reset()
	if code := s2.job(t, repo, &fakeInput{}, &out, d).Run(context.Background()); code != backup.ExitBlocked {
		t.Fatalf("neuer Container: Exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Neues dazugekommen") {
		t.Errorf("neuer Container nicht gemeldet:\n%s", out.String())
	}
	// Als Dienst nennt der Hinweis den Leitstand statt Befehle.
	out.Reset()
	jl := s2.job(t, repo, &fakeInput{}, &out, d)
	jl.Leitstand = true
	jl.Run(context.Background())
	if !strings.Contains(out.String(), "„Jetzt neu erfassen“") || strings.Contains(out.String(), "./hostdeck") {
		t.Errorf("Hinweis im Leitstand:\n%s", out.String())
	}

	// --plan zeigt den Plan und braucht kein restic.
	s3 := newMiniServer(t)
	out.Reset()
	j := s3.job(t, repo, &fakeInput{}, &out, d)
	j.PlanOnly, j.Repo = true, nil
	if code := j.Run(context.Background()); code != backup.ExitOK {
		t.Fatalf("Probelauf: Exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "postgres-Dump aus shop-db-1") || len(l.list()) > 0 {
		t.Errorf("Probelauf:\n%s", out.String())
	}
}
