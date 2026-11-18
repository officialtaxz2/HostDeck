package caddy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const mainFile = `{
	email admin@example.de
}

(logging) {
	log {
		output file /var/log/caddy/access.log
	}
}

import /etc/caddy/mcserver.caddy
import kula-monitor.caddy # relativ

a.example.de {
	import logging
	reverse_proxy localhost:3001
}
b.example.de {
	reverse_proxy localhost:3005
}`

// fake spielt caddy und systemctl. validate lehnt jede Datei im Ordner ab,
// in der KAPUTT steht; adapt liefert hosts.
type fake struct {
	mu        sync.Mutex
	dir       string
	hosts     []string
	reloads   int
	reloadErr error
	calls     []string
	modes     []os.FileMode // Rechte der Datei aus --config bei validate
}

func (f *fake) run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		// Wie exec.CommandContext: Mit erledigtem ctx startet nichts.
		return nil, ctx.Err()
	}
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name == "systemctl" && len(args) > 0 && args[0] == "reload" {
		f.reloads++
		if f.reloadErr != nil {
			return []byte("Job for caddy.service failed."), f.reloadErr
		}
		return nil, nil
	}
	if len(args) == 0 {
		return nil, errors.New("unbekannt")
	}
	// validate und adapt laufen in einem eigenen leeren Ordner.
	if !strings.Contains(strings.Join(env, " "), "HOME=") {
		return nil, errors.New("ohne eigenes HOME")
	}
	switch args[0] {
	case "validate":
		// Liest die Datei aus --config und alles, was sie im selben Ordner
		// einbindet, wie caddy.
		cfg := args[2]
		if fi, err := os.Stat(cfg); err == nil {
			f.modes = append(f.modes, fi.Mode().Perm())
		}
		for _, name := range f.included(cfg) {
			b, _ := os.ReadFile(name)
			if bytes.Contains(b, []byte("KAPUTT")) {
				return []byte(`{"level":"info","msg":"using config"}` + "\n" + `Error: adapting config using caddyfile: ` + filepath.Base(name) + `:2: unrecognized directive: KAPUTT`), errors.New("exit status 1")
			}
		}
		return []byte("Valid configuration"), nil
	case "adapt":
		var routes []string
		for _, h := range f.hosts {
			routes = append(routes, fmt.Sprintf(`{"match":[{"host":[%q]}],"handle":[{"handler":"subroute","routes":[]}]}`, h))
		}
		return []byte(`{"level":"warn","msg":"Caddyfile input is not formatted"}` + "\n" +
			`{"apps":{"http":{"servers":{"srv0":{"routes":[` + strings.Join(routes, ",") + `]}}}}}`), nil
	}
	return nil, errors.New("unbekannt: " + args[0])
}

// included sind cfg und die Dateien, die cfg mit import einbindet.
func (f *fake) included(cfg string) []string {
	out := []string{cfg}
	b, _ := os.ReadFile(cfg)
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "import ") {
			continue
		}
		target := strings.Fields(l)[1]
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cfg), target)
		}
		ms, _ := filepath.Glob(target)
		out = append(out, ms...)
	}
	return out
}

func setup(t *testing.T) (*fake, *Caddy, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(cfg, []byte(mainFile), 0o640); err != nil {
		t.Fatal(err)
	}
	f := &fake{dir: dir, hosts: []string{"a.example.de", "b.example.de"}}
	c := &Caddy{Bin: "caddy", Config: cfg, Service: Unit, Run: f.run, TempDir: t.TempDir()}
	return f, c, dir
}

type probes struct {
	mu    sync.Mutex
	f     *fake
	after map[string][]Answer // Antworten nach dem ersten Neuladen, der Reihe nach
	n     map[string]int
}

func (p *probes) probe(_ context.Context, h string) Answer {
	p.f.mu.Lock()
	reloaded := p.f.reloads > 0
	p.f.mu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if reloaded {
		if as := p.after[h]; len(as) > 0 {
			i := p.n[h]
			p.n[h]++
			if i >= len(as) {
				i = len(as) - 1
			}
			return as[i]
		}
	}
	return Answer{Status: 200}
}

func change(dir string, site string) Change {
	cfg, _ := os.ReadFile(filepath.Join(dir, "Caddyfile"))
	own := filepath.Join(dir, "hostdeck.caddy")
	return Change{
		Files: []File{{Path: filepath.Join(dir, "Caddyfile"), Data: AddImport(cfg, own)}, {Path: own, Data: []byte(site)}},
		Skip:  []string{"hostdeck.example.de"},
	}
}

func TestApplyKlappt(t *testing.T) {
	f, c, dir := setup(t)
	p := &probes{f: f, n: map[string]int{}}
	var events []string
	a := &Applier{Caddy: c, Probe: p.probe, Event: func(e Event) { events = append(events, e.Step+": "+e.State) }}
	ch := change(dir, "hostdeck.example.de {\n\treverse_proxy unix//run/hostdeck-caddy/web.sock\n}\n")
	checked := false
	ch.Check = func(context.Context) error { checked = true; return nil }
	if err := a.Apply(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "Caddyfile"))
	if !bytes.HasPrefix(got, []byte(mainFile)) || !bytes.Contains(got, []byte("import "+filepath.Join(dir, "hostdeck.caddy"))) {
		t.Fatalf("Caddyfile:\n%s", got)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "hostdeck.caddy")); fi == nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("hostdeck.caddy: %v", fi)
	}
	if !checked || f.reloads != 1 {
		t.Fatalf("geprüft %v, neu geladen %d", checked, f.reloads)
	}
	if !strings.Contains(strings.Join(events, "\n"), StepCompare+": ok") {
		t.Fatalf("Schritte: %v", events)
	}
}

// unchanged prüft, dass alles wie vorher ist.
func unchanged(t *testing.T, dir string) {
	t.Helper()
	got, _ := os.ReadFile(filepath.Join(dir, "Caddyfile"))
	if string(got) != mainFile {
		t.Fatalf("Caddyfile nicht zurückgelegt:\n%s", got)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "Caddyfile")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("Rechte: %v", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(dir, "hostdeck.caddy")); err == nil {
		t.Fatal("hostdeck.caddy liegt noch da")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("Reste im Ordner: %v", ents)
	}
}

func TestApplyKaputteDateiWirdZurueckgelegt(t *testing.T) {
	f, c, dir := setup(t)
	p := &probes{f: f, n: map[string]int{}}
	a := &Applier{Caddy: c, Probe: p.probe}
	err := a.Apply(context.Background(), change(dir, "hostdeck.example.de {\n\tKAPUTT\n}\n"))
	var undone *ErrUndone
	if !errors.As(err, &undone) || undone.Step != StepValidate || !strings.Contains(err.Error(), "unrecognized directive: KAPUTT") {
		t.Fatalf("Ergebnis: %v", err)
	}
	if f.reloads != 0 {
		t.Fatal("trotz roter Prüfung neu geladen")
	}
	unchanged(t, dir)
}

func TestApplyNeuladenSchlaegtFehl(t *testing.T) {
	f, c, dir := setup(t)
	f.reloadErr = errors.New("exit status 1")
	p := &probes{f: f, n: map[string]int{}}
	a := &Applier{Caddy: c, Probe: p.probe}
	err := a.Apply(context.Background(), change(dir, "hostdeck.example.de {\n}\n"))
	var undone *ErrUndone
	if errors.As(err, &undone) {
		t.Fatalf("Neuladen auch beim Zurücklegen rot, trotzdem „alles wie vorher“: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "Dateien sind zurückgelegt") {
		t.Fatalf("Ergebnis: %v", err)
	}
	unchanged(t, dir)
	if f.reloads != 2 {
		t.Fatalf("Neuladen: %d", f.reloads)
	}
}

func TestApplyNeueSeiteAntwortetNicht(t *testing.T) {
	f, c, dir := setup(t)
	p := &probes{f: f, n: map[string]int{}}
	a := &Applier{Caddy: c, Probe: p.probe}
	ch := change(dir, "hostdeck.example.de {\n}\n")
	ch.Check = func(context.Context) error { return errors.New("kein Zertifikat") }
	err := a.Apply(context.Background(), ch)
	var undone *ErrUndone
	if !errors.As(err, &undone) || undone.Step != StepCheck {
		t.Fatalf("Ergebnis: %v", err)
	}
	unchanged(t, dir)
	if f.reloads != 2 {
		t.Fatalf("Neuladen: %d", f.reloads)
	}
}

func TestApplyAndereSeiteWeichtAb(t *testing.T) {
	f, c, dir := setup(t)
	p := &probes{f: f, n: map[string]int{}, after: map[string][]Answer{"b.example.de": {{Err: "connection refused"}}}}
	a := &Applier{Caddy: c, Probe: p.probe, Retries: 2}
	err := a.Apply(context.Background(), change(dir, "hostdeck.example.de {\n}\n"))
	var undone *ErrUndone
	if !errors.As(err, &undone) || undone.Step != StepCompare || !strings.Contains(err.Error(), "b.example.de (vorher Status 200, jetzt Fehler: connection refused)") {
		t.Fatalf("Ergebnis: %v", err)
	}
	unchanged(t, dir)
}

func TestApplyKurzerAussetzerNimmtNichtsZurueck(t *testing.T) {
	f, c, dir := setup(t)
	p := &probes{f: f, n: map[string]int{}, after: map[string][]Answer{"a.example.de": {{Status: 502}, {Status: 200}}}}
	a := &Applier{Caddy: c, Probe: p.probe, Retries: 2}
	if err := a.Apply(context.Background(), change(dir, "hostdeck.example.de {\n}\n")); err != nil {
		t.Fatal(err)
	}
}

func TestRehearse(t *testing.T) {
	f, c, dir := setup(t)
	a := &Applier{Caddy: c, Probe: (&probes{f: f}).probe}
	if err := a.Rehearse(context.Background(), []byte("hostdeck.example.de {\n\tKAPUTT\n}\n")); err != nil {
		t.Fatal(err)
	}
	unchanged(t, dir)
	if _, err := os.Stat(ProbePath(c.Config)); err == nil {
		t.Fatal("Probe-Datei liegt noch")
	}
	// Die Probe-Kopie hat die Rechte des Caddyfiles, nie mehr.
	for _, m := range f.modes {
		if m != 0o640 {
			t.Fatalf("Rechte bei validate: %v", f.modes)
		}
	}
	// Nimmt validate auch Kaputtes an, taugt die Prüfung nichts.
	if err := a.Rehearse(context.Background(), []byte("hostdeck.example.de {\n}\n")); err == nil || !strings.Contains(err.Error(), "angenommen") {
		t.Fatalf("Probe mit heiler Datei: %v", err)
	}
	unchanged(t, dir)
	// Ist schon die jetzige Konfiguration ungültig, geht nichts weiter.
	os.WriteFile(filepath.Join(dir, "kula-monitor.caddy"), []byte("KAPUTT\n"), 0o644)
	if err := a.Rehearse(context.Background(), []byte("x {\n\tKAPUTT\n}\n")); err == nil || !strings.Contains(err.Error(), "jetzige Konfiguration") {
		t.Fatalf("ungültige Konfiguration: %v", err)
	}
}

func TestImporte(t *testing.T) {
	dir := "/etc/caddy"
	target := "/etc/caddy/hostdeck.caddy"
	if Covered([]byte(mainFile), dir, target) {
		t.Fatal("feste Importe decken hostdeck.caddy nicht ab")
	}
	for _, line := range []string{"import *.caddy", "import /etc/caddy/*.caddy", `import "hostdeck.caddy"`, "import /etc/caddy/hostdeck.caddy # schon da"} {
		if !Covered([]byte(mainFile+"\n"+line+"\n"), dir, target) {
			t.Errorf("%q deckt hostdeck.caddy ab", line)
		}
	}
	// In einem Block zählt ein import nicht.
	if Covered([]byte("x.example {\n\timport *.caddy\n}\n"), dir, target) {
		t.Error("import im Block")
	}
	// Hin und zurück: byte-gleich.
	for _, orig := range []string{mainFile + "\n", "", mainFile} {
		added := AddImport([]byte(orig), target)
		if !Covered(added, dir, target) {
			t.Fatalf("nach AddImport nicht eingebunden:\n%s", added)
		}
		back, ok := RemoveImport(added, target)
		want := orig
		if orig == mainFile {
			want += "\n" // Zeilenende am Schluss bleibt
		}
		if !ok || string(back) != want {
			t.Fatalf("RemoveImport:\n%q\nstatt\n%q", back, want)
		}
	}
	if _, ok := RemoveImport([]byte(mainFile), target); ok {
		t.Fatal("RemoveImport ohne Zeile")
	}
}

func TestExecStartUndHosts(t *testing.T) {
	bin, cfg := parseExecStart("{ path=/usr/bin/caddy ; argv[]=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile ; ignore_errors=no ; start_time=[n/a] }")
	if bin != "/usr/bin/caddy" || cfg != "/etc/caddy/Caddyfile" {
		t.Fatalf("%q %q", bin, cfg)
	}
	f := &fake{hosts: []string{"B.example.de", "a.example.de", "*.wild.example"}}
	out, _ := f.run(context.Background(), []string{"HOME=/x"}, "caddy", "adapt")
	hosts, err := HostsFromJSON(out)
	if err != nil || strings.Join(hosts, ",") != "a.example.de,b.example.de" {
		t.Fatalf("%v %v", hosts, err)
	}
	if got := short([]byte("{\"level\":\"info\",\"msg\":\"x\"}\n{\"level\":\"error\",\"error\":\"loading config: bad\"}\n")); got != "loading config: bad" {
		t.Fatalf("short: %q", got)
	}
}

// Abbruch (Strg+C, Dienst hält an) während des Wartens aufs Zertifikat:
// Die Dateien kommen zurück, und Caddy lädt sie trotzdem neu.
func TestApplyAbbruchNachDemNeuladen(t *testing.T) {
	f, c, dir := setup(t)
	p := &probes{f: f, n: map[string]int{}}
	a := &Applier{Caddy: c, Probe: p.probe}
	ctx, cancel := context.WithCancel(context.Background())
	ch := change(dir, "hostdeck.example.de {\n}\n")
	ch.Check = func(ctx context.Context) error { cancel(); return ctx.Err() }
	err := a.Apply(ctx, ch)
	var undone *ErrUndone
	if !errors.As(err, &undone) {
		t.Fatalf("Ergebnis: %v", err)
	}
	unchanged(t, dir)
	if f.reloads != 2 {
		t.Fatalf("Neuladen nach dem Abbruch: %d", f.reloads)
	}
}

// Stürzt HostDeck mitten in der Änderung ab, legt Recover beim nächsten Start
// alles aus dem Journal zurück.
func TestJournalNachAbsturz(t *testing.T) {
	f, c, dir := setup(t)
	journalDir := t.TempDir()
	j := filepath.Join(journalDir, "caddy-rueckweg.json")
	p := &probes{f: f, n: map[string]int{}}
	a := &Applier{Caddy: c, Probe: p.probe, Journal: j, Note: "freischalten"}
	ch := change(dir, "hostdeck.example.de {\n}\n")
	// „Absturz“ nach dem Neuladen: Check kehrt nie zurück; hier läuft
	// Recover, während die geänderten Dateien liegen.
	var note string
	var found bool
	var rerr error
	ch.Check = func(context.Context) error {
		if fi, err := os.Stat(j); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("Journal: %v %v", fi, err)
		}
		note, found, rerr = Recover(context.Background(), c, j)
		return errors.New("Ende der Probe")
	}
	a.Apply(context.Background(), ch)
	if rerr != nil || !found || note != "freischalten" {
		t.Fatalf("Recover: %q %v %v", note, found, rerr)
	}
	unchanged(t, dir)
	if _, err := os.Stat(j); err != nil {
		t.Fatal("Apply hat das Journal gelöscht; das macht der Aufrufer")
	}
	if err := ClearJournal(j); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := Recover(context.Background(), c, j); found {
		t.Fatal("Journal nach ClearJournal")
	}
}

// Ein Symlink an der Stelle von hostdeck.caddy: HostDeck schreibt nicht hindurch.
func TestSymlinkStattHostdeckCaddy(t *testing.T) {
	f, c, dir := setup(t)
	target := filepath.Join(t.TempDir(), "passwd")
	os.WriteFile(target, []byte("root:x:0:0\n"), 0o644)
	os.Symlink(target, filepath.Join(dir, "hostdeck.caddy"))
	a := &Applier{Caddy: c, Probe: (&probes{f: f, n: map[string]int{}}).probe}
	err := a.Apply(context.Background(), change(dir, "hostdeck.example.de {\n}\n"))
	var undone *ErrUndone
	if !errors.As(err, &undone) || !strings.Contains(err.Error(), "Symlink") {
		t.Fatalf("Ergebnis: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "root:x:0:0\n" {
		t.Fatal("durch den Symlink geschrieben")
	}
}

// Hat jemand das Caddyfile seit dem Plan geändert, überschreibt Apply es nicht.
func TestApplyCaddyfileGeradeGeaendert(t *testing.T) {
	f, c, dir := setup(t)
	a := &Applier{Caddy: c, Probe: (&probes{f: f, n: map[string]int{}}).probe}
	ch := change(dir, "hostdeck.example.de {\n}\n")
	ch.Files[0].Was = []byte(mainFile + "\n# Stand beim Planen\n")
	err := a.Apply(context.Background(), ch)
	var undone *ErrUndone
	if !errors.As(err, &undone) || !strings.Contains(err.Error(), "gerade geändert") {
		t.Fatalf("Ergebnis: %v", err)
	}
	unchanged(t, dir)
	if f.reloads != 0 {
		t.Fatal("neu geladen")
	}
}
