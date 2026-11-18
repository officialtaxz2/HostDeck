package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/restic"
)

const testDump = `\connect shop
COPY public.kunden (id, name) FROM stdin;
1	Anna
2	Ben
\.
COPY public.leer (id) FROM stdin;
\.
`

// fakeStore ist ein Backup im Speicher. Restore legt für den Projektordner
// die Compose-Datei an, wie es das echte Zurückspielen täte.
type fakeStore struct {
	pwFile, pw string
	snaps      []restic.Snapshot
	files      map[string]string // "snapshot|datei"
	composeDir string
	compose    string // Inhalt der compose.yaml nach dem Zurückspielen
	calls      []string
}

func (s *fakeStore) CheckPassword(context.Context) error {
	b, err := os.ReadFile(s.pwFile)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != s.pw {
		return &restic.Error{Op: "cat", Code: 12}
	}
	return nil
}

func (s *fakeStore) Snapshots(context.Context, ...string) ([]restic.Snapshot, error) {
	return s.snaps, nil
}

func (s *fakeStore) Dump(_ context.Context, snap, file string, w io.Writer) error {
	c, ok := s.files[snap+"|"+file]
	if !ok {
		return fmt.Errorf("%s fehlt in %s", file, snap)
	}
	_, err := io.WriteString(w, c)
	return err
}

func (s *fakeStore) Restore(_ context.Context, snap, sub, target string) error {
	s.calls = append(s.calls, fmt.Sprintf("restore %s %s → %s", snap, sub, target))
	if snap == "s1" {
		if err := os.MkdirAll(s.composeDir, 0o755); err != nil {
			return err
		}
		c := s.compose
		if c == "" {
			c = "services: {}\n"
		}
		return os.WriteFile(filepath.Join(s.composeDir, "compose.yaml"), []byte(c), 0o644)
	}
	return nil
}

type fakeDocker struct {
	dir      string
	calls    []string
	volumes  map[string]string
	running  map[string]bool
	created  bool
	counts   map[string]Counts // je Container
	preData  Counts            // Inhalt der Datenbank direkt nach dem Anlegen
	loaded   string
	replayed string
	// replayStderr schreibt die Datenbank beim Einspielen auf stderr.
	replayStderr string
	// loadIDs meldet docker load; tagOnly: nur diese ID lässt sich taggen
	// (containerd-Speicher).
	loadIDs []string
	tagOnly string
	// images: was es hier gibt (ID oder Name); imageIDs: Name → ID.
	images   map[string]bool
	imageIDs map[string]string
	// env: je Container die Namen mit Wert, wie Docker sie meldet.
	env map[string][]string
}

func newFakeDocker(dir string) *fakeDocker {
	return &fakeDocker{dir: dir, volumes: map[string]string{}, running: map[string]bool{}, counts: map[string]Counts{},
		images: map[string]bool{}, imageIDs: map[string]string{}}
}

func (d *fakeDocker) call(f string, args ...any) { d.calls = append(d.calls, fmt.Sprintf(f, args...)) }

func (d *fakeDocker) Version(context.Context) (string, string, error) { return "29.0.0", "2.30.0", nil }

func (d *fakeDocker) ComposeCreate(_ context.Context, g Group) error {
	d.call("create %s", g.Name)
	d.created = true
	d.volumes["shop_pg"] = filepath.Join(d.dir, "volumes", "shop_pg")
	d.counts["shop-db"] = d.preData
	return nil
}

func (d *fakeDocker) ComposeUp(_ context.Context, g Group, services []string, alone bool) error {
	d.call("up %s %s %v", g.Name, strings.Join(services, " "), alone)
	for _, s := range services {
		d.running[g.Name+"-"+s] = true
		// Ein leerer Datenordner heißt: Das Image legt eine leere Datenbank an.
		if dir, ok := d.volumes["shop_pg"]; ok && s == "db" {
			if empty, err := dirEmpty(dir); err == nil && empty {
				d.counts["shop-db"] = nil
			}
		}
	}
	return nil
}

func (d *fakeDocker) Load(_ context.Context, r io.Reader) ([]string, error) {
	b, err := io.ReadAll(r)
	d.loaded = string(b)
	d.call("load")
	return d.loadIDs, err
}

func (d *fakeDocker) Tag(_ context.Context, id, ref string) error {
	d.call("tag %s %s", id, ref)
	if d.tagOnly != "" && id != d.tagOnly {
		return fmt.Errorf("docker tag: Error response from daemon: No such image: %s", id)
	}
	return nil
}

func (d *fakeDocker) ImageExists(_ context.Context, ref string) bool { return d.images[ref] }
func (d *fakeDocker) ImageID(_ context.Context, ref string) string {
	if d.images[ref] {
		return d.imageIDs[ref]
	}
	return ""
}

func (d *fakeDocker) NetworkExists(context.Context, string) bool { return false }

func (d *fakeDocker) NetworkCreate(_ context.Context, n Network) error {
	d.call("netz %s", n.Name)
	return nil
}

func (d *fakeDocker) VolumeCreate(_ context.Context, name string) error {
	d.call("volume %s", name)
	d.volumes[name] = filepath.Join(d.dir, "volumes", name)
	return nil
}

func (d *fakeDocker) VolumeMountpoint(_ context.Context, name string) (string, bool, error) {
	dir, ok := d.volumes[name]
	return dir, ok, nil
}

func (d *fakeDocker) ServiceContainer(_ context.Context, project, service string) (string, error) {
	if !d.created {
		return "", fmt.Errorf("kein Container für %s/%s", project, service)
	}
	return project + "-" + service, nil
}

func (d *fakeDocker) EnvSet(_ context.Context, id string) ([]string, error) {
	set, ok := d.env[id]
	if !ok {
		return nil, fmt.Errorf("kein Container %s", id)
	}
	return set, nil
}

func (d *fakeDocker) MountSource(_ context.Context, id, dest string) (string, error) {
	return filepath.Join(d.dir, "anon", id+strings.ReplaceAll(dest, "/", "_")), nil
}

func (d *fakeDocker) Running(_ context.Context, id string) (bool, error) { return d.running[id], nil }

func (d *fakeDocker) VolumeUsers(_ context.Context, name string) ([]string, error) {
	if name == "shop_pg" && d.running["shop-db"] {
		return []string{"shop-db"}, nil
	}
	return nil, nil
}

func (d *fakeDocker) Stop(_ context.Context, ids []string) error {
	d.call("stop %s", strings.Join(ids, " "))
	for _, id := range ids {
		d.running[id] = false
	}
	return nil
}

func (d *fakeDocker) Exec(_ context.Context, id, script string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	switch script {
	case pgReady:
		if !d.running[id] {
			return 1, nil
		}
		return 0, nil
	case pgCount:
		var keys []string
		for k := range d.counts[id] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			db, t, _ := strings.Cut(k, "/")
			fmt.Fprintf(stdout, "%s|%s|%d\n", db, t, d.counts[id][k])
		}
		return 0, nil
	case pgReplay:
		b, err := io.ReadAll(stdin)
		d.replayed = string(b)
		c := NewDumpCounter("postgres")
		c.Write(b)
		d.counts[id] = c.Counts()
		io.WriteString(stderr, d.replayStderr)
		d.call("einspielen %s", id)
		return 0, err
	}
	return 127, nil
}

func (d *fakeDocker) ProjectContainers(_ context.Context, project string) ([]ContainerState, error) {
	if !d.created {
		return nil, nil
	}
	var cs []ContainerState
	for _, s := range []string{"db", "web"} {
		id := project + "-" + s
		st := "created"
		if d.running[id] {
			st = "running"
		}
		cs = append(cs, ContainerState{ID: id, Name: id + "-1", Service: s, Running: d.running[id], State: st})
	}
	return cs, nil
}

type fakeInput struct {
	answers   []string
	questions []backup.Question
}

func (f *fakeInput) Ask(_ context.Context, q backup.Question) (string, error) {
	f.questions = append(f.questions, q)
	if len(f.answers) == 0 {
		return "", io.EOF
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, nil
}

type jobEnv struct {
	job    *Job
	store  *fakeStore
	docker *fakeDocker
	input  *fakeInput
	out    *bytes.Buffer
	root   string // Ordner, der für „/“ des alten Servers steht
}

func newJobEnv(t *testing.T, answers ...string) *jobEnv {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	shop := filepath.Join(root, "opt", "shop")
	rf := &backup.RunFile{
		Run: "20261101-100000", Valid: true, Hostname: "alt",
		Groups: []backup.Group{{Name: "shop", Containers: []backup.Ctr{{Name: "shop-web-1", Running: true}, {Name: "shop-db-1", Running: true}},
			Dumps: []backup.Item{{Key: "volume:shop_pg", ContainerName: "shop-db-1"}}}},
		Snapshots: []backup.RunSnapshot{
			{Key: "project:shop", Group: "shop", Strategy: backup.StratFolder, Snapshot: "s1", Paths: []string{shop}},
			{Key: "anon:compose:shop/web:/cache", Group: "shop", Strategy: backup.StratStop, Snapshot: "s3", Paths: []string{"/var/lib/docker/volumes/f00/_data"}},
			{Key: "volume:shop_pg", Group: "shop", Strategy: backup.StratDump, Snapshot: "s4", Filename: "dumps/shop/volume-shop_pg.sql", Engine: "postgres"},
			{Key: "image:sha256:bbb", Strategy: backup.StratImage, Snapshot: "i2", Filename: "images/bbb.tar", ImageID: "sha256:bbb"},
		},
	}
	inv := &inventory.Inventory{
		Projects: []inventory.Project{{Key: "project:shop", WorkingDir: shop, ConfigFiles: []string{filepath.Join(shop, "compose.yaml")}}},
		Containers: []inventory.Container{
			{Name: "shop-web-1", Service: "web", ImageID: "sha256:bbb", Image: "shop-web:lokal", Networks: []string{"proxy"}},
			{Name: "shop-db-1", Service: "db"},
		},
		Volumes:  []inventory.Volume{{Key: "volume:shop_pg", Name: "shop_pg", FormerProj: "shop"}},
		Networks: []inventory.Network{{Name: "proxy", Driver: "bridge"}},
	}
	rb, _ := json.Marshal(rf)
	ib, _ := json.Marshal(inv)
	lauf := "/root/.hostdeck/laeufe/20261101-100000"
	store := &fakeStore{
		pwFile: filepath.Join(state, "passwort"), pw: "richtig", composeDir: shop,
		snaps: []restic.Snapshot{{ID: "lauf1", Short: "lauf1", Time: time.Date(2026, 11, 1, 10, 5, 0, 0, time.UTC), Paths: []string{lauf}, Host: "alt"}},
		files: map[string]string{
			"lauf1|" + lauf + "/lauf.json":      string(rb),
			"lauf1|" + lauf + "/inventory.json": string(ib),
			"s4|/dumps/shop/volume-shop_pg.sql": testDump,
			"i2|/images/bbb.tar":                "TAR",
		},
	}
	docker := newFakeDocker(root)
	in := &fakeInput{answers: answers}
	out := &bytes.Buffer{}
	job := &Job{Version: "test", StateDir: state, Repo: store, PasswordFile: store.pwFile, Docker: docker, Input: in, Out: out,
		Hostname: "neu", Settle: time.Millisecond, ReadyWait: time.Second}
	return &jobEnv{job: job, store: store, docker: docker, input: in, out: out, root: root}
}

func TestRestoreAllesGut(t *testing.T) {
	e := newJobEnv(t, "falsch", "richtig", "ja")
	code := e.job.Run(context.Background())
	if code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if len(e.input.questions) != 3 || !e.input.questions[0].Secret || strings.Contains(e.input.questions[2].Text, "\n") {
		t.Errorf("Fragen: %+v", e.input.questions)
	}
	if !strings.Contains(e.out.String(), "Das Passwort passt nicht.") {
		t.Errorf("falsches Passwort nicht gemeldet:\n%s", e.out)
	}
	wantDocker := []string{"netz proxy", "load", "tag sha256:bbb shop-web:lokal", "create shop", "up shop db true", "einspielen shop-db", "up shop db web false"}
	if !slices.Equal(e.docker.calls, wantDocker) {
		t.Errorf("Docker:\n%q\nerwartet\n%q", e.docker.calls, wantDocker)
	}
	anon := filepath.Join(e.root, "anon", "shop-web_cache")
	wantStore := []string{"restore s1  → /", "restore s3 /var/lib/docker/volumes/f00/_data → " + anon}
	if !slices.Equal(e.store.calls, wantStore) {
		t.Errorf("Backup:\n%q\nerwartet\n%q", e.store.calls, wantStore)
	}
	if e.docker.loaded != "TAR" || e.docker.replayed != testDump {
		t.Errorf("Image %q, Dump %q", e.docker.loaded, e.docker.replayed)
	}
	for _, s := range []string{"✓ 2 Tabellen, 2 Zeilen, wie im Backup", "✓ shop: alle Dienste laufen", "Alles zurückgespielt"} {
		if !strings.Contains(e.out.String(), s) {
			t.Errorf("Ausgabe ohne %q:\n%s", s, e.out)
		}
	}
	rec, err := LoadRecord(e.job.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Run != "20261101-100000" || rec.Source != "alt" || len(rec.Groups) != 1 {
		t.Fatalf("Protokoll: %+v", rec)
	}
	g := rec.Groups[0]
	if !slices.Equal(g.Services, []string{"db", "web"}) || len(g.DBs) != 1 || !g.DBs[0].Replayed ||
		g.DBs[0].Counts["shop/public.kunden"] != 2 || len(g.Errors) != 0 {
		t.Errorf("Protokoll Gruppe: %+v", g)
	}
	// Für „Probe-Kopie entfernen“: was HostDeck angelegt hat, samt Datenbank.
	if g.Dir == "" || !slices.Contains(g.Volumes, "shop_pg") {
		t.Errorf("angelegt: Ordner %q, Volumes %v", g.Dir, g.Volumes)
	}
	if fi, err := os.Stat(RecordPath(e.job.StateDir)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("Protokoll-Datei: %v %v", fi.Mode(), err)
	}

	// hostdeck verify danach
	var out bytes.Buffer
	if code := Verify(context.Background(), e.docker, e.job.StateDir, &out); code != ExitOK {
		t.Errorf("verify Exit %d:\n%s", code, out.String())
	}
	e.docker.counts["shop-db"]["shop/public.kunden"] = 5
	out.Reset()
	if code := Verify(context.Background(), e.docker, e.job.StateDir, &out); code != ExitOK || !strings.Contains(out.String(), "1 Tabellen sind seitdem gewachsen") {
		t.Errorf("verify mit neuen Zeilen, Exit %d:\n%s", code, out.String())
	}
	e.docker.counts["shop-db"]["shop/public.kunden"] = 1
	e.docker.running["shop-web"] = false
	out.Reset()
	if code := Verify(context.Background(), e.docker, e.job.StateDir, &out); code != ExitFailed ||
		!strings.Contains(out.String(), "✗ web: created") || !strings.Contains(out.String(), "weniger Zeilen") {
		t.Errorf("verify mit Fehlern, Exit %d:\n%s", code, out.String())
	}
}

func TestRestoreSchonBelegt(t *testing.T) {
	e := newJobEnv(t, "ja")
	os.MkdirAll(e.store.composeDir, 0o755)
	os.WriteFile(filepath.Join(e.store.composeDir, "alt.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Dir(e.store.pwFile), 0o700)
	os.WriteFile(e.store.pwFile, []byte("richtig\n"), 0o600)
	pgdir := filepath.Join(e.root, "volumes", "shop_pg")
	os.MkdirAll(pgdir, 0o755)
	os.WriteFile(filepath.Join(pgdir, "PG_VERSION"), []byte("16"), 0o644)
	e.docker.volumes["shop_pg"] = pgdir
	e.job.Hostname = "alt"

	code := e.job.Run(context.Background())
	if code != ExitBlocked || !strings.Contains(e.out.String(), "Nicht gestartet.") {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if len(e.input.questions) != 1 || !strings.Contains(e.input.questions[0].Text, "Tippe überschreiben") {
		t.Errorf("Fragen: %+v", e.input.questions)
	}
	for _, s := range []string{e.store.composeDir + " (Ordner mit Inhalt)", "Volume shop_pg hat schon eine Datenbank", "derselbe Rechnername"} {
		if !strings.Contains(e.out.String(), s) {
			t.Errorf("Ausgabe ohne %q:\n%s", s, e.out)
		}
	}
	if len(e.store.calls) > 0 || len(e.docker.calls) > 0 {
		t.Errorf("ohne Zustimmung darf nichts passieren: %v %v", e.store.calls, e.docker.calls)
	}
}

func TestRestoreDatenbankNichtLeer(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	e.docker.preData = Counts{"shop/public.kunden": 7}
	code := e.job.Run(context.Background())
	if code != ExitFailed {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if slices.Contains(e.docker.calls, "einspielen shop-db") {
		t.Error("in eine Datenbank mit Daten darf kein Dump")
	}
	if !strings.Contains(e.out.String(), "enthält schon Daten (7 Zeilen)") || !strings.Contains(e.out.String(), "nicht alles ist in Ordnung") {
		t.Errorf("Ausgabe:\n%s", e.out)
	}
	rec, err := LoadRecord(e.job.StateDir)
	if err != nil || rec.Groups[0].DBs[0].Replayed || len(rec.Groups[0].Errors) == 0 {
		t.Errorf("Protokoll: %+v %v", rec, err)
	}
}

func TestRestoreProbelauf(t *testing.T) {
	e := newJobEnv(t, "richtig")
	e.job.PlanOnly = true
	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if len(e.store.calls) > 0 || len(e.docker.calls) > 0 || len(e.input.questions) != 1 {
		t.Errorf("Probelauf hat etwas getan: %v %v %v", e.store.calls, e.docker.calls, e.input.questions)
	}
	if _, err := LoadRecord(e.job.StateDir); err == nil {
		t.Error("Probelauf hat ein Protokoll geschrieben")
	}
}

func TestRestoreLaufWahl(t *testing.T) {
	e := newJobEnv(t, "richtig", "7", "2", "nein")
	older := e.store.snaps[0]
	older.ID, older.Time = "lauf0", older.Time.Add(-24*time.Hour)
	e.store.snaps = append(e.store.snaps, older)
	for k, v := range e.store.files {
		if strings.HasPrefix(k, "lauf1|") {
			e.store.files["lauf0|"+strings.TrimPrefix(k, "lauf1|")] = v
		}
	}
	e.store.files["lauf1|/root/.hostdeck/laeufe/20261101-100000/lauf.json"] = "kaputt"
	code := e.job.Run(context.Background())
	if code != ExitBlocked || !strings.Contains(e.out.String(), "Bitte eine Zahl von 1 bis 2") || !strings.Contains(e.out.String(), "Nicht gestartet.") {
		t.Errorf("Exit %d:\n%s", code, e.out)
	}
}

// Beim Probe-Umzug bringt der alte Server das Passwort mit; gefragt wird
// nur noch, ob es losgeht, und das mit Knöpfen.
func TestRestoreMitMitgebrachtemPasswort(t *testing.T) {
	e := newJobEnv(t, "ja")
	e.job.Password = "richtig"
	os.MkdirAll(filepath.Dir(e.store.pwFile), 0o700)
	os.WriteFile(e.store.pwFile, []byte("altes-anderes\n"), 0o600)
	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if len(e.input.questions) != 1 || e.input.questions[0].Secret || len(e.input.questions[0].Choices) != 2 || e.input.questions[0].Note == "" {
		t.Fatalf("Fragen: %+v", e.input.questions)
	}
	if b, _ := os.ReadFile(e.store.pwFile); strings.TrimSpace(string(b)) != "richtig" {
		t.Fatalf("Passwortdatei: %q", b)
	}
	old, _ := filepath.Glob(e.store.pwFile + ".vorher-*")
	if len(old) != 1 {
		t.Fatalf("alte Passwortdatei nicht aufgehoben: %v", old)
	}
	if strings.Contains(e.out.String(), "richtig") {
		t.Fatalf("Passwort in der Ausgabe:\n%s", e.out)
	}
}

func TestRestoreMitgebrachtesPasswortFalsch(t *testing.T) {
	e := newJobEnv(t)
	e.job.Password = "daneben"
	if code := e.job.Run(context.Background()); code != ExitBlocked || !strings.Contains(e.out.String(), "mitgebrachte Backup-Passwort passt nicht") {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if _, err := os.Stat(e.store.pwFile); err == nil {
		t.Fatal("falsches Passwort liegt noch in der Datei")
	}
}

type noDocker struct {
	*fakeDocker
	installed bool
}

func (d *noDocker) Version(ctx context.Context) (string, string, error) {
	if !d.installed {
		return "", "", fmt.Errorf("docker: nicht gefunden")
	}
	return d.fakeDocker.Version(ctx)
}

func TestRestoreInstalliertDockerAufWunsch(t *testing.T) {
	e := newJobEnv(t, "ja", "ja")
	e.job.Password = "richtig"
	nd := &noDocker{fakeDocker: e.docker}
	e.job.Docker = nd
	e.job.InstallDocker = func(context.Context) error { nd.installed = true; return nil }
	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if !strings.Contains(e.input.questions[0].Note, "Docker") || e.input.questions[0].Choices[0].Answer != "ja" {
		t.Fatalf("Frage: %+v", e.input.questions[0])
	}

	// Nein heißt: nichts installieren, nicht starten.
	e2 := newJobEnv(t, "nein")
	e2.job.Password = "richtig"
	nd2 := &noDocker{fakeDocker: e2.docker}
	e2.job.Docker = nd2
	e2.job.InstallDocker = func(context.Context) error { t.Fatal("installiert trotz nein"); return nil }
	if code := e2.job.Run(context.Background()); code != ExitBlocked {
		t.Fatalf("Exit %d:\n%s", code, e2.out)
	}

	// Im Probelauf wird nichts installiert, nur gesagt; geprüft wird
	// trotzdem alles andere (Passwort, Lauf, Plan).
	e3 := newJobEnv(t)
	e3.job.Password, e3.job.PlanOnly = "richtig", true
	e3.job.Docker = &noDocker{fakeDocker: e3.docker}
	e3.job.InstallDocker = func(context.Context) error { t.Fatal("installiert im Probelauf"); return nil }
	if code := e3.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e3.out)
	}
	out := e3.out.String()
	for _, want := range []string{"Beim Probe-Umzug bietet HostDeck an, es zu installieren", "Passwort passt zum Backup", "Probelauf beendet, nichts wurde verändert"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fehlt %q:\n%s", want, out)
		}
	}
	if len(e3.docker.calls) != 0 {
		t.Fatalf("Docker im Probelauf benutzt: %v", e3.docker.calls)
	}

	// Ohne Angebot zum Installieren (Kommandozeile) bleibt es ein Halt.
	e4 := newJobEnv(t)
	e4.job.Password, e4.job.PlanOnly = "richtig", true
	e4.job.Docker = &noDocker{fakeDocker: e4.docker}
	if code := e4.job.Run(context.Background()); code != ExitBlocked || !strings.Contains(e4.out.String(), "get.docker.com") {
		t.Fatalf("Exit %d:\n%s", code, e4.out)
	}
}

// Mit dem containerd-Speicher (frischer Server mit Docker 29) hat ein
// geladenes Image eine andere ID als auf dem alten Server. Den Namen für
// Compose bekommt es dann über die ID, die docker load meldet.
func TestRestoreImageOhneNamenContainerd(t *testing.T) {
	e := newJobEnv(t, "falsch", "richtig", "ja")
	e.docker.loadIDs = []string{"sha256:neu"}
	e.docker.tagOnly = "sha256:neu"
	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if !slices.Contains(e.docker.calls, "tag sha256:neu shop-web:lokal") || slices.Contains(e.docker.calls, "tag sha256:bbb shop-web:lokal") {
		t.Fatalf("Docker: %q", e.docker.calls)
	}

	// Meldet docker load nichts, gilt weiter die alte ID.
	e2 := newJobEnv(t, "falsch", "richtig", "ja")
	e2.docker.tagOnly = "sha256:bbb"
	if code := e2.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e2.out)
	}
}

func TestLoadedIDs(t *testing.T) {
	out := "Loaded image: alpine:latest\nLoaded image ID: sha256:abc\n\nLoaded image ID: sha256:def\n"
	if got := loadedIDs([]byte(out)); !slices.Equal(got, []string{"sha256:abc", "sha256:def"}) {
		t.Fatalf("%q", got)
	}
}

func TestMissingExternal(t *testing.T) {
	g := Group{Volumes: []Volume{{Name: "tipp-v3-data"}, {Service: "web", Dest: "/cache"}}}
	err := errors.New("docker compose --project-name: exit status 1: Image node:24 Pulled\nexternal volume \"tipp-v3-data\" not found\nexternal volume \"fremd\" not found")
	if got := missingExternal(err, g); !slices.Equal(got, []string{"tipp-v3-data"}) {
		t.Fatalf("%q", got)
	}
	if missingExternal(nil, g) != nil {
		t.Fatal("ohne Fehler")
	}
}

// Zweiter Lauf auf denselben Server: Die Datenbank hat schon genau den
// Stand aus dem Backup. Das ist kein Fehler, eingespielt wird nichts.
func TestRestoreDatenbankSchonWieImBackup(t *testing.T) {
	c := NewDumpCounter("postgres")
	c.Write([]byte(testDump))
	e := newJobEnv(t, "richtig", "ja")
	e.docker.preData = c.Counts()
	// Der erste Lauf hat genau diesen Dump eingespielt (ältere Datei ohne
	// Herkunft, wie sie HostDeck bis c55ca2d schrieb).
	prev := Record{Run: "20261101-100000", Source: "alt", Groups: []GroupRecord{{Name: "shop", DBs: []DBRecord{{Title: "Volume shop_pg", Service: "db", Replayed: true}}}}}
	b, _ := json.Marshal(prev)
	os.MkdirAll(e.job.StateDir, 0o700)
	os.WriteFile(RecordPath(e.job.StateDir), b, 0o600)
	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if slices.Contains(e.docker.calls, "einspielen shop-db") {
		t.Error("Dump trotz vorhandener Daten eingespielt")
	}
	if !strings.Contains(e.out.String(), "✓ Dieser Dump wurde hier schon bei einem früheren Lauf eingespielt; gleich viele Zeilen je Tabelle (2 Tabellen, 2 Zeilen)") {
		t.Errorf("Ausgabe:\n%s", e.out)
	}
	rec, err := LoadRecord(e.job.StateDir)
	if err != nil || rec.Groups[0].DBs[0].From != "alt/20261101-100000" {
		t.Fatalf("Herkunft nicht vermerkt: %+v %v", rec, err)
	}

	// Ein anderer Lauf mit gleich vielen Zeilen ist ein Fehler.
	e2 := newJobEnv(t, "richtig", "ja")
	e2.docker.preData = c.Counts()
	prev.Run = "20261031-100000"
	b, _ = json.Marshal(prev)
	os.MkdirAll(e2.job.StateDir, 0o700)
	os.WriteFile(RecordPath(e2.job.StateDir), b, 0o600)
	if code := e2.job.Run(context.Background()); code == ExitOK {
		t.Fatalf("anderer Lauf übersprungen:\n%s", e2.out)
	}
	if !strings.Contains(e2.out.String(), "enthält schon Daten (2 Zeilen). HostDeck spielt einen Dump nur in eine leere Datenbank ein") {
		t.Errorf("Ausgabe:\n%s", e2.out)
	}
}

// Steht der Lauf fest (Probe-Umzug aus dem Leitstand), fragt restore nicht.
func TestRestoreLaufVorgegeben(t *testing.T) {
	e := newJobEnv(t, "ja")
	e.job.Password = "richtig"
	lauf := "/root/.hostdeck/laeufe/20261102-100000"
	e.store.snaps = append(e.store.snaps, restic.Snapshot{ID: "lauf2", Short: "lauf2", Time: time.Date(2026, 11, 2, 10, 5, 0, 0, time.UTC), Paths: []string{lauf}, Tags: []string{"sbm-lauf", "sbm-run=20261102-100000"}, Host: "alt"})
	e.job.RunID = "20261101-100000"
	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	for _, q := range e.input.questions {
		if strings.HasPrefix(q.Text, "Welchen Lauf") {
			t.Fatalf("trotz vorgegebenem Lauf gefragt:\n%s", e.out)
		}
	}
	if !strings.Contains(e.out.String(), "Lauf 20261101-100000 vom") {
		t.Errorf("Ausgabe:\n%s", e.out)
	}

	e2 := newJobEnv(t)
	e2.job.Password = "richtig"
	e2.job.RunID = "20261030-100000"
	if code := e2.job.Run(context.Background()); code != ExitBlocked || !strings.Contains(e2.out.String(), "der Lauf 20261030-100000 fehlt im Backup") {
		t.Fatalf("Exit %d:\n%s", code, e2.out)
	}
}

// Ein Skript gab einen Wert beim Start nur über die Shell an Compose (so
// beim mc-router mit MC_ROUTER_MAPPING): Auf dem neuen Server ist er leer.
// HostDeck meldet das als Fehler mit dem Namen, ohne einen Wert zu kennen.
func TestRestoreWertAusDerShellFehlt(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	lauf := "lauf1|/root/.hostdeck/laeufe/20261101-100000/inventory.json"
	var inv inventory.Inventory
	if err := json.Unmarshal([]byte(e.store.files[lauf]), &inv); err != nil {
		t.Fatal(err)
	}
	inv.Containers[0].EnvSet = []string{"MAPPING", "PATH"}
	inv.Containers[1].EnvSet = []string{"PATH", "POSTGRES_DB"}
	b, _ := json.Marshal(inv)
	e.store.files[lauf] = string(b)
	e.docker.env = map[string][]string{"shop-web": {"PATH"}, "shop-db": {"PATH", "POSTGRES_DB"}}
	code := e.job.Run(context.Background())
	if code != ExitFailed {
		t.Fatalf("Exit %d, erwartet %d:\n%s", code, ExitFailed, e.out)
	}
	out := e.out.String()
	if !strings.Contains(out, "web: MAPPING hatte auf dem alten Server einen Wert und ist hier leer") || !strings.Contains(out, ".env") {
		t.Errorf("fehlender Wert nicht gemeldet:\n%s", out)
	}
	if strings.Contains(out, "db: ") && strings.Contains(out, "POSTGRES_DB hatte") {
		t.Errorf("db fälschlich gemeldet:\n%s", out)
	}
}

// Der Name im Container ist nicht der, den man eintragen muss: Die
// Compose-Datei setzt MODUS aus START_MODUS. HostDeck nennt
// START_MODUS und rät nicht, den laufenden Dienst auf dem alten Server
// neu anzulegen.
func TestRestoreWertAusAndererVariable(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	e.store.compose = "services:\n  web:\n    image: shop-web:lokal\n    environment:\n      MODUS: ${START_MODUS:-}\n      ROLLE: fest\n  db:\n    environment:\n      - POSTGRES_DB=shop\n"
	lauf := "lauf1|/root/.hostdeck/laeufe/20261101-100000/inventory.json"
	var inv inventory.Inventory
	if err := json.Unmarshal([]byte(e.store.files[lauf]), &inv); err != nil {
		t.Fatal(err)
	}
	inv.Containers[0].EnvSet = []string{"MODUS", "PATH", "ROLLE"}
	b, _ := json.Marshal(inv)
	e.store.files[lauf] = string(b)
	e.docker.env = map[string][]string{"shop-web": {"PATH", "ROLLE"}, "shop-db": {"PATH"}}
	if code := e.job.Run(context.Background()); code != ExitFailed {
		t.Fatalf("Exit %d, erwartet %d:\n%s", code, ExitFailed, e.out)
	}
	out := e.out.String()
	for _, want := range []string{
		"web: MODUS hatte auf dem alten Server einen Wert und ist hier leer",
		"In der Compose-Datei steht „MODUS: ${START_MODUS:-}“, den Wert liefert also START_MODUS.",
		"Trag START_MODUS=… in " + filepath.Join(e.root, "opt", "shop", ".env") + " ein.",
		"Den laufenden Dienst dort nicht neu anlegen",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fehlt %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Trag MODUS=") {
		t.Errorf("falscher Rat:\n%s", out)
	}
}

func TestEnvSource(t *testing.T) {
	dir := t.TempDir()
	a, o := filepath.Join(dir, "compose.yaml"), filepath.Join(dir, "compose.override.yaml")
	if err := os.WriteFile(a, []byte("services:\n  web:\n    environment:\n      - MAPPING\n      - A=$${NICHT}\n      - B=${X}-$Y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o, []byte("services:\n  web:\n    environment:\n      C:\n      A: ${NEU:?fehlt}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		key, expr string
		names     []string
		found     bool
	}{
		{"MAPPING", "${MAPPING}", []string{"MAPPING"}, true},
		{"A", "${NEU:?fehlt}", []string{"NEU"}, true}, // spätere Datei gewinnt
		{"B", "${X}-$Y", []string{"X", "Y"}, true},
		{"C", "${C}", []string{"C"}, true},
		{"D", "", nil, false},
	} {
		expr, names, found := envSource([]string{a, o}, "web", c.key)
		if expr != c.expr || !slices.Equal(names, c.names) || found != c.found {
			t.Errorf("%s: %q %v %v, erwartet %q %v %v", c.key, expr, names, found, c.expr, c.names, c.found)
		}
	}
	if got := varNames("$${NICHT}"); len(got) != 0 {
		t.Errorf("$$ ist wörtlich: %v", got)
	}
}

func TestParseEnvSet(t *testing.T) {
	got := parseEnvSet("PATH 34\nMAPPING 9\nLEER 5\n\nKAPUTT x\n")
	if !slices.Equal(got, []string{"PATH", "MAPPING"}) {
		t.Errorf("parseEnvSet = %q", got)
	}
}

// Env-Dateien vom alten Server gibt HostDeck Compose wieder mit, aber nur die,
// die nach dem Zurückspielen da sind.
func TestComposeArgsEnvDateien(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, "router.env")
	if err := os.WriteFile(env, []byte("X=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := Group{Name: "mc", Dir: dir, Files: []string{filepath.Join(dir, "compose.yaml")}, EnvFiles: []string{env, filepath.Join(dir, "fehlt.env")}}
	got := strings.Join(composeArgs(g, "create"), " ")
	want := "compose --project-name mc --project-directory " + dir + " --file " + filepath.Join(dir, "compose.yaml") + " --env-file " + env + " create"
	if got != want {
		t.Errorf("composeArgs =\n%s\nerwartet\n%s", got, want)
	}
}
