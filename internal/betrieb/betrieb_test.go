package betrieb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

// fakeDocker spielt einen Server wie vps1 im Kleinen: ein Projekt mit
// App und Datenbank, ein Panel mit Docker-Socket und einem Spielserver, den
// es anlegt, und ein Container ohne Projekt.
type fakeDocker struct {
	mu    sync.Mutex
	list  []dockerapi.OpsContainer
	info  map[string]dockerapi.Container
	calls []string
	fail  map[string]error
	logs  []byte
	gate  chan struct{} // gesetzt: Stats wartet, bis er zu ist
	stats int
}

func compose(project, service string, deps string) map[string]string {
	l := map[string]string{labelProject: project, labelService: service}
	if deps != "" {
		l[labelDependsOn] = deps
	}
	return l
}

func newFake() *fakeDocker {
	f := &fakeDocker{
		list: []dockerapi.OpsContainer{
			{ID: "a1", Names: []string{"/pensum-app"}, Image: "pensum:latest", State: "running", Labels: compose("pensum", "app", "db:service_healthy:false,cache:service_started:false")},
			{ID: "a2", Names: []string{"/pensum-db"}, Image: "postgres:17", State: "running", Labels: compose("pensum", "db", "")},
			{ID: "a3", Names: []string{"/pensum-cache"}, Image: "redis:7", State: "exited", Labels: compose("pensum", "cache", "")},
			{ID: "w1", Names: []string{"/calagopus"}, Image: "calagopus/panel", State: "running", Labels: compose("mcserver", "panel", ""),
				Mounts: []dockerapi.MountPoint{{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"}, {Type: "bind", Source: "/opt/calagopus/wings", Destination: "/opt/calagopus/wings"}}},
			{ID: "g1", Names: []string{"/0f5e8c1a-1111-2222-3333-444455556666"}, Image: "java:21", State: "running",
				Mounts: []dockerapi.MountPoint{{Type: "bind", Source: "/opt/calagopus/wings/volumes/0f5e8c1a-1111-2222-3333-444455556666", Destination: "/home/container"}}},
			{ID: "x1", Names: []string{"/arcane"}, Image: "arcane:1", State: "running"},
		},
		info: map[string]dockerapi.Container{},
		fail: map[string]error{},
	}
	for _, c := range f.list {
		v := dockerapi.Container{ID: c.ID, Name: "/" + c.Name()}
		v.State.Running = c.State == "running"
		v.State.StartedAt = "2026-11-02T08:00:00.123Z"
		v.State.FinishedAt = "2026-11-01T20:00:00Z"
		v.HostConfig.RestartPolicy.Name = "unless-stopped"
		f.info[c.Name()] = v
	}
	v := f.info["arcane"]
	v.HostConfig.RestartPolicy.Name = "always"
	v.RestartCount = 4
	f.info["arcane"] = v
	return f
}

func (f *fakeDocker) find(id string) int {
	for i, c := range f.list {
		if c.ID == id || c.Name() == id {
			return i
		}
	}
	return -1
}

func (f *fakeDocker) List(context.Context) ([]dockerapi.OpsContainer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dockerapi.OpsContainer(nil), f.list...), nil
}

func (f *fakeDocker) Inspect(_ context.Context, id string) (dockerapi.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.find(id)
	if i < 0 {
		return dockerapi.Container{}, dockerapi.ErrNoSuchContainer
	}
	return f.info[f.list[i].Name()], nil
}

func (f *fakeDocker) Stats(ctx context.Context, id string) (dockerapi.Usage, error) {
	f.mu.Lock()
	f.stats++
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return dockerapi.Usage{}, ctx.Err()
		}
	}
	return dockerapi.Usage{CPU: 1.5, Mem: 100 << 20, MemLimit: 8 << 30}, nil
}

func (f *fakeDocker) Logs(_ context.Context, id string, tail int, tty bool, max int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "logs "+id)
	return f.logs, nil
}

func (f *fakeDocker) act(id, verb, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, verb+" "+id)
	if err := f.fail[verb+" "+id]; err != nil {
		return err
	}
	i := f.find(id)
	if i < 0 {
		return dockerapi.ErrNoSuchContainer
	}
	f.list[i].State = state
	return nil
}

func (f *fakeDocker) Start(_ context.Context, id string) error { return f.act(id, "start", "running") }
func (f *fakeDocker) Stop(_ context.Context, id string, _ time.Duration) error {
	return f.act(id, "stop", "exited")
}
func (f *fakeDocker) Restart(_ context.Context, id string, _ time.Duration) error {
	return f.act(id, "restart", "running")
}

func (f *fakeDocker) took() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

func newService(t *testing.T, f *fakeDocker) *Service {
	return &Service{Docker: f, StoppedFile: filepath.Join(t.TempDir(), "gestoppt.json"),
		Now: func() time.Time { return time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC) }}
}

func byName(s Snapshot) map[string]Container {
	m := map[string]Container{}
	for _, c := range s.Containers {
		m[c.Name] = c
	}
	return m
}

func TestListeMitPanelUndReihenfolge(t *testing.T) {
	f := newFake()
	s := newService(t, f)
	snap, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range snap.Containers {
		names = append(names, c.Name)
	}
	// Projekte nach Namen, darin Startreihenfolge; ohne Projekt am Ende.
	want := []string{"calagopus", "pensum-cache", "pensum-db", "pensum-app", "0f5e8c1a-1111-2222-3333-444455556666", "arcane"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("Reihenfolge %v, erwartet %v", names, want)
	}
	m := byName(snap)
	if g := m["0f5e8c1a-1111-2222-3333-444455556666"]; g.ManagedBy != "calagopus" {
		t.Errorf("Spielserver nicht als Panel-Container erkannt: %+v", g)
	}
	if m["calagopus"].ManagedBy != "" || m["arcane"].ManagedBy != "" {
		t.Error("Panel oder Arcane fälschlich als verwaltet erkannt")
	}
	if a := m["pensum-app"]; a.CPU == nil || a.Mem != 100<<20 || a.Since == nil || a.Project != "pensum" || a.Service != "app" {
		t.Errorf("App: %+v", a)
	}
	if c := m["pensum-cache"]; c.CPU != nil || c.Running {
		t.Errorf("gestoppter Cache mit Messwert oder als laufend: %+v", c)
	}
	if a := m["arcane"]; a.Restarts != 4 || a.RestartPolicy != "always" {
		t.Errorf("Arcane: %+v", a)
	}
}

func TestPlanRegeln(t *testing.T) {
	f := newFake()
	s := newService(t, f)
	ctx := context.Background()

	p, err := s.Plan(ctx, "projekt:pensum", Stop)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Containers, []string{"pensum-app", "pensum-db"}) {
		t.Errorf("Stoppen: erst die App, dann die Datenbank; gestoppter Cache fehlt: %v", p.Containers)
	}
	if len(p.Skipped) != 1 || p.Skipped[0].Name != "pensum-cache" {
		t.Errorf("ausgelassen: %+v", p.Skipped)
	}

	p, _ = s.Plan(ctx, "projekt:pensum", Start)
	if !reflect.DeepEqual(p.Containers, []string{"pensum-cache"}) {
		t.Errorf("Starten nur, was nicht läuft: %v", p.Containers)
	}

	p, _ = s.Plan(ctx, "projekt:pensum", Restart)
	if !reflect.DeepEqual(p.Containers, []string{"pensum-db", "pensum-app"}) || len(p.Skipped) != 1 || !strings.Contains(p.Skipped[0].Reason, "bleibt") {
		t.Errorf("Neustart lässt gestoppte aus: %+v", p)
	}

	// Panel-Container: nie steuern, auch nicht im Projekt des Panels.
	p, _ = s.Plan(ctx, "container:0f5e8c1a-1111-2222-3333-444455556666", Stop)
	if p.Problem == "" || len(p.Containers) != 0 || !strings.Contains(p.Problem, "Panel") {
		t.Errorf("Panel-Container ließe sich stoppen: %+v", p)
	}

	p, _ = s.Plan(ctx, "container:arcane", Stop)
	if len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "always") {
		t.Errorf("Hinweis auf Neustart-Regel fehlt: %+v", p.Notes)
	}

	p, _ = s.Plan(ctx, "container:arcane", Start)
	if p.Problem != "Hier läuft schon alles." {
		t.Errorf("Start eines laufenden: %+v", p)
	}

	for _, bad := range []string{"", "projekt:", "pensum", "volume:x", "projekt:../etc", "container:a b"} {
		if _, err := s.Plan(ctx, bad, Stop); !errors.Is(err, ErrBadTarget) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := s.Plan(ctx, "projekt:pensum", "loeschen"); !errors.Is(err, ErrBadAction) {
		t.Errorf("unbekannte Aktion: %v", err)
	}
	if _, err := s.Plan(ctx, "projekt:gibtsnicht", Stop); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekanntes Projekt: %v", err)
	}
	if len(f.took()) != 0 {
		t.Error("Plan hat Docker verändert")
	}
}

func TestGestopptBleibtGestoppt(t *testing.T) {
	f := newFake()
	s := newService(t, f)
	ctx := context.Background()

	// Vorschau veraltet: nichts passiert.
	if _, err := s.Do(ctx, "projekt:pensum", Stop, []string{"pensum-app"}); err == nil || !strings.Contains(err.Error(), "Vorschau") {
		t.Fatalf("veraltete Vorschau: %v", err)
	}
	if c := f.took(); len(c) != 0 {
		t.Fatalf("trotzdem gehandelt: %v", c)
	}

	res, err := s.Do(ctx, "container:pensum-app", Stop, []string{"pensum-app"})
	if err != nil || !res.OK {
		t.Fatalf("Stoppen: %v %+v", err, res)
	}
	if c := f.took(); !reflect.DeepEqual(c, []string{"stop pensum-app"}) {
		t.Fatalf("Aufrufe %v", c)
	}
	if !strings.Contains(res.Text(), "gestoppt") {
		t.Errorf("Text: %s", res.Text())
	}
	b, err := os.ReadFile(s.StoppedFile)
	if err != nil || !strings.Contains(string(b), "pensum-app") {
		t.Fatalf("nicht gemerkt: %v %s", err, b)
	}
	snap, _ := s.List(ctx)
	if byName(snap)["pensum-app"].StoppedByYou == nil {
		t.Error("Liste zeigt nicht, dass du ihn gestoppt hast")
	}

	// Neustart des Projekts lässt ihn aus.
	p, _ := s.Plan(ctx, "projekt:pensum", Restart)
	if !reflect.DeepEqual(p.Containers, []string{"pensum-db"}) {
		t.Fatalf("Neustart nimmt gestoppten mit: %+v", p)
	}
	found := false
	for _, sk := range p.Skipped {
		if sk.Name == "pensum-app" && strings.Contains(sk.Reason, "hast du gestoppt") {
			found = true
		}
	}
	if !found {
		t.Errorf("Grund fehlt: %+v", p.Skipped)
	}

	// Starten vergisst es wieder.
	if _, err := s.Do(ctx, "container:pensum-app", Start, []string{"pensum-app"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(s.StoppedFile)
	if strings.Contains(string(b), "pensum-app") {
		t.Errorf("nach dem Start noch gemerkt: %s", b)
	}

	// Ein Fehler bei einem Container: Ergebnis sagt welcher.
	f.fail["restart pensum-db"] = errors.New("docker: kaputt")
	res, err = s.Do(ctx, "projekt:pensum", Restart, []string{"pensum-db", "pensum-app"})
	if err != nil || res.OK || len(res.Steps) != 2 || res.Steps[0].OK || !res.Steps[1].OK {
		t.Fatalf("Teilfehler: %v %+v", err, res)
	}
	if !strings.Contains(res.Text(), "nicht neu gestartet: pensum-db") {
		t.Errorf("Text: %s", res.Text())
	}
}

func TestLogsSauber(t *testing.T) {
	f := newFake()
	s := newService(t, f)
	f.logs = []byte("2026-11-03T09:00:00Z \x1b[32mINFO\x1b[0m bereit\r\n2026-11-03T09:00:01Z Fortschritt 10%\rFortschritt 100%\n\xff kaputt\n")
	lg, err := s.Logs(context.Background(), "pensum-app", 50)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-11-03T09:00:00Z INFO bereit", "Fortschritt 100%", "� kaputt"}
	if !reflect.DeepEqual(lg.Lines, want) {
		t.Errorf("Zeilen %q", lg.Lines)
	}
	if _, err := s.Logs(context.Background(), "gibtsnicht", 50); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekannt: %v", err)
	}
	if _, err := s.Logs(context.Background(), "../x", 50); !errors.Is(err, ErrBadTarget) {
		t.Errorf("Name: %v", err)
	}
	// Zu viel: Anfang fällt weg, an einer Zeilengrenze.
	f.logs = []byte(strings.Repeat("x", 100) + "\n" + strings.Repeat("y\n", MaxLogBytes/2))
	lg, _ = s.Logs(context.Background(), "pensum-app", 2000)
	if !lg.Cut || lg.Lines[0] != "y" {
		t.Errorf("Kürzen: cut=%v erste=%q", lg.Cut, lg.Lines[0])
	}
}

// Docker braucht für Messwerte gut eine Sekunde. Die Liste wartet nicht
// darauf: Sie kommt sofort, sagt, dass gemessen wird, und hat die Werte beim
// nächsten Mal.
func TestListeWartetNichtAufMesswerte(t *testing.T) {
	f := newFake()
	f.gate = make(chan struct{})
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	s := &Service{Docker: f, Now: func() time.Time { return now }}
	ctx := context.Background()
	start := time.Now()
	snap, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Liste wartete %v", d)
	}
	if !snap.UsagePending {
		t.Error("ohne Hinweis, dass gemessen wird")
	}
	for _, c := range snap.Containers {
		if c.CPU != nil {
			t.Errorf("%s hat schon einen Wert", c.Name)
		}
	}
	// Eine zweite Liste, während gemessen wird, misst nicht noch einmal.
	if _, err := s.List(ctx); err != nil {
		t.Fatal(err)
	}
	close(f.gate)
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, _ = s.List(ctx)
		if !snap.UsagePending || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := 0
	for _, c := range snap.Containers {
		if c.CPU != nil {
			got++
		}
	}
	if snap.UsagePending || got != 5 {
		t.Fatalf("nach der Messung: offen=%v, %d Werte", snap.UsagePending, got)
	}
	f.mu.Lock()
	n := f.stats
	f.mu.Unlock()
	if n != 5 {
		t.Errorf("%d Messungen statt 5 (eine je laufendem Container)", n)
	}
	// Frische Werte: keine neue Messung.
	s.List(ctx)
	f.mu.Lock()
	n = f.stats
	f.mu.Unlock()
	if n != 5 {
		t.Errorf("trotz frischer Werte neu gemessen: %d", n)
	}
	// Vorschau und Aktion brauchen keine Messwerte.
	if _, err := s.Plan(ctx, "projekt:pensum", Restart); err != nil {
		t.Fatal(err)
	}
}

// Ein Aufräum-Job läuft einmal und beendet sich sauber. Er ist dann
// erledigt, nicht gestoppt, und „Projekt starten“ lässt ihn aus.
func TestEinmalJob(t *testing.T) {
	f := newFake()
	f.list = append(f.list, dockerapi.OpsContainer{ID: "j1", Names: []string{"/pensum-cleanup"}, Image: "alpine:latest", State: "exited", Labels: compose("pensum", "cleanup", "")})
	v := dockerapi.Container{ID: "j1"}
	v.HostConfig.RestartPolicy.Name = "no"
	f.info["pensum-cleanup"] = v
	// Mit Code 1 beendet wäre er kein erledigter Job.
	v.State.ExitCode = 1
	f.info["pensum-cache"] = func() dockerapi.Container {
		x := f.info["pensum-cache"]
		x.HostConfig.RestartPolicy.Name = "no"
		x.State.ExitCode = 1
		return x
	}()
	s := &Service{Docker: f}
	ctx := context.Background()
	snap, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]bool{}
	for _, c := range snap.Containers {
		jobs[c.Name] = c.Job
	}
	if !jobs["pensum-cleanup"] || jobs["pensum-cache"] || jobs["pensum-app"] {
		t.Fatalf("Jobs: %v", jobs)
	}
	p, err := s.Plan(ctx, "projekt:pensum", Start)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Containers, []string{"pensum-cache"}) || !strings.Contains(fmt.Sprint(p.Skipped), "einmaliger Job") {
		t.Errorf("Projekt starten: %+v", p)
	}
	p, _ = s.Plan(ctx, "container:pensum-cleanup", Start)
	if !reflect.DeepEqual(p.Containers, []string{"pensum-cleanup"}) || !strings.Contains(strings.Join(p.Notes, " "), "Arbeit noch einmal") || p.Back != "Nicht nötig." {
		t.Errorf("Job einzeln starten: %+v", p)
	}
	// Von dir gestoppt ist kein Job.
	if _, err := s.Do(ctx, "container:pensum-app", Stop, []string{"pensum-app"}); err != nil {
		t.Fatal(err)
	}
	f.info["pensum-app"] = func() dockerapi.Container {
		x := f.info["pensum-app"]
		x.HostConfig.RestartPolicy.Name = "no"
		return x
	}()
	snap, _ = s.List(ctx)
	for _, c := range snap.Containers {
		if c.Name == "pensum-app" && (c.Job || c.StoppedByYou == nil) {
			t.Errorf("pensum-app: %+v", c)
		}
	}
}

// policyDocker kann zusätzlich die Neustart-Regel setzen, wie dockerapi.Ops.
type policyDocker struct{ *fakeDocker }

func (p policyDocker) SetRestartPolicy(_ context.Context, id, policy string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "policy "+id+" "+policy)
	return nil
}

func TestStoppenBleibtNachNeustart(t *testing.T) {
	f := newFake()
	s := newService(t, f)
	s.Docker = policyDocker{f}
	ctx := context.Background()
	p, err := s.Plan(ctx, "container:arcane", Stop)
	if err != nil || len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "unless-stopped") {
		t.Fatalf("Hinweis: %v %+v", err, p.Notes)
	}
	f.took()
	if _, err := s.Do(ctx, "container:arcane", Stop, p.Containers); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.took(), ",")
	if !strings.HasPrefix(got, "policy arcane unless-stopped,") || !strings.Contains(got, "stop arcane") {
		t.Errorf("Ablauf: %s", got)
	}
	// Starten sagt, dass die Regel bleibt.
	f.mu.Lock()
	v := f.info["arcane"]
	v.HostConfig.RestartPolicy.Name = "unless-stopped"
	f.info["arcane"] = v
	f.mu.Unlock()
	if p, _ := s.Plan(ctx, "container:arcane", Start); !strings.Contains(strings.Join(p.Notes, " "), "behält die Neustart-Regel") {
		t.Errorf("Hinweis beim Starten: %+v", p.Notes)
	}
	// Ein Container ohne „always“ behält seine Regel.
	p, _ = s.Plan(ctx, "projekt:pensum", Stop)
	f.took()
	s.Do(ctx, "projekt:pensum", Stop, p.Containers)
	if got := strings.Join(f.took(), ","); strings.Contains(got, "policy") {
		t.Errorf("Regel ohne Grund geändert: %s", got)
	}
}

func (f *fakeDocker) Top(_ context.Context, id string) (dockerapi.Top, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "top "+id)
	return dockerapi.Top{Titles: []string{"PID", "USER", "%CPU", "RSS", "ELAPSED", "COMMAND"}, Processes: [][]string{{"4711", "app", "1.5", "20480", "01:02:03", "node"}}}, nil
}

func TestLogsStderr(t *testing.T) {
	f := newFake()
	s := newService(t, f)
	m := dockerapi.StderrMark
	f.logs = []byte("eins\n" + m + "Fehler\r\n" + "zwei\n" + m + "\x1b[31mnoch einer\x1b[0m\n")
	lg, err := s.Logs(context.Background(), "pensum-app", 50)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"eins", "Fehler", "zwei", "noch einer"}; !reflect.DeepEqual(lg.Lines, want) {
		t.Errorf("Zeilen %q", lg.Lines)
	}
	if !reflect.DeepEqual(lg.Err, []int{1, 3}) {
		t.Errorf("stderr %v", lg.Err)
	}
}

func TestDetailsOhneGeheimnisse(t *testing.T) {
	f := newFake()
	s := newService(t, f)
	v := f.info["pensum-app"]
	v.Config.Image = "pensum:latest"
	v.Config.Env = dockerapi.EnvNames{"POSTGRES_PASSWORD", "TZ"}
	v.Config.Labels = map[string]string{labelProject: "pensum", labelService: "app", "traefik.http.middlewares.auth.basicauth.users": "admin:$apr1$geheim"}
	v.HostConfig.PortBindings = map[string][]dockerapi.PortBinding{"8080/tcp": {{HostIP: "127.0.0.1", HostPort: "18080"}}}
	v.Mounts = []dockerapi.MountPoint{{Type: "volume", Name: "pensum_daten", Source: "/var/lib/docker/volumes/pensum_daten/_data", Destination: "/data", RW: true}}
	v.State.OOMKilled = true
	v.HostConfig.LogConfig.Type = "splunk"
	v.HostConfig.LogConfig.Config = map[string]string{"splunk-token": "geheim-token", "max-size": "10m"}
	f.info["pensum-app"] = v
	d, err := s.Details(context.Background(), "pensum-app")
	if err != nil {
		t.Fatal(err)
	}
	c := d.Config
	if c.Image != "pensum:latest" || len(c.Ports) != 1 || c.Ports[0].HostPort != "18080" || c.Mounts[0].Source != "pensum_daten" || !c.Mounts[0].RW {
		t.Errorf("Konfig %+v", c)
	}
	if !reflect.DeepEqual(c.EnvNames, []string{"POSTGRES_PASSWORD", "TZ"}) || c.Compose["dienst"] != "app" || len(c.Compose) != 2 {
		t.Errorf("Umgebung/Compose %+v %+v", c.EnvNames, c.Compose)
	}
	if !reflect.DeepEqual(c.LogOptions, map[string]string{"max-size": "10m"}) {
		t.Errorf("Log-Optionen %+v", c.LogOptions)
	}
	if d.Processes == nil || d.Processes.Rows[0][5] != "node" {
		t.Errorf("Prozesse %+v", d.Processes)
	}
	if b := fmt.Sprintf("%+v", d); strings.Contains(b, "geheim") {
		t.Errorf("fremdes Label in den Einzelheiten: %s", b)
	}
	// Gestoppt: keine Prozesse, kein Aufruf.
	d, _ = s.Details(context.Background(), "pensum-cache")
	if d.Processes != nil || strings.Contains(strings.Join(f.calls, ","), "top pensum-cache") {
		t.Errorf("gestoppt: %+v %v", d.Processes, f.calls)
	}
	if _, err := s.Details(context.Background(), "gibtsnicht"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekannt: %v", err)
	}
	if _, err := s.Details(context.Background(), "../x"); !errors.Is(err, ErrBadTarget) {
		t.Errorf("Name: %v", err)
	}
	snap, _ := s.List(context.Background())
	for _, c := range snap.Containers {
		if c.Name == "pensum-app" && !c.OOM {
			t.Errorf("OOM fehlt in der Liste")
		}
	}
}

// R2: Von dir gestoppt, dann von außerhalb gestartet (docker compose up):
// Die Merkung gilt nicht mehr. Ein späterer Absturz ist kein Stopp von dir.
func TestGestopptDannExternGestartetVergisstMerkung(t *testing.T) {
	f := newFake()
	s := newService(t, f)
	ctx := context.Background()
	stop := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if err := SaveStopped(s.StoppedFile, map[string]time.Time{"pensum-app": stop, "pensum-cache": stop}); err != nil {
		t.Fatal(err)
	}
	set := func(name, state, started string, running bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.list[f.find(name)].State = state
		v := f.info[name]
		v.State.Running = running
		v.State.StartedAt = started
		f.info[name] = v
	}

	// Liste von kurz vor deinem Stopp: Er läuft noch, sein Start liegt
	// davor. Die Merkung bleibt.
	set("pensum-app", "running", "2026-11-02T08:00:00Z", true)
	if _, err := s.List(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadStopped(s.StoppedFile)["pensum-app"]; !ok {
		t.Fatal("Merkung vergessen, obwohl der Start vor deinem Stopp liegt")
	}

	// Gestoppt, aber der letzte Start ist jünger als die Merkung (nach
	// einem Umzug): bleibt von dir gestoppt.
	set("pensum-cache", "exited", "2026-11-02T15:00:00Z", false)
	snap, _ := s.List(ctx)
	if byName(snap)["pensum-cache"].StoppedByYou == nil {
		t.Error("gestoppter Container gilt nicht mehr als von dir gestoppt")
	}

	// Von außerhalb gestartet: Merkung weg.
	set("pensum-app", "running", "2026-11-02T18:00:00Z", true)
	if _, err := s.List(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadStopped(s.StoppedFile)["pensum-app"]; ok {
		t.Fatal("Merkung bleibt nach externem Start")
	}

	// Danach abgestürzt: unerwartet gestoppt, keine Pause.
	set("pensum-app", "exited", "2026-11-02T18:00:00Z", false)
	f.mu.Lock()
	v := f.info["pensum-app"]
	v.State.ExitCode = 1
	f.info["pensum-app"] = v
	f.mu.Unlock()
	snap, _ = s.List(ctx)
	c := byName(snap)["pensum-app"]
	if c.StoppedByYou != nil {
		t.Fatalf("Absturz gilt als von dir gestoppt: %v", c.StoppedByYou)
	}
	c.classify(s.now())
	if c.Class != KlasseUnerwartet {
		t.Errorf("Klasse %q, erwartet %q", c.Class, KlasseUnerwartet)
	}
}
