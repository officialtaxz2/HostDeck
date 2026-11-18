package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/ort"
)

// sbmRig legt eine Installation von sbm an, wie sie bis 1.0 aussah, und
// den Installer von HostDeck daneben.
type sbmRig struct {
	in    *installer
	fs    *fakeSystem
	u     *umstieg
	root  string
	old   legacyPaths
	home  string
	group map[string]bool
}

func newSbmRig(t *testing.T) *sbmRig {
	t.Helper()
	in, fs, _ := testInstaller(t, "")
	root := filepath.Dir(filepath.Dir(in.Exe))
	p := func(s string) string { return filepath.Join(root, s) }
	old := legacy
	old.ProgDir, old.BinLink, old.DataDir = p("usr/local/lib/sbm"), p("usr/local/bin/sbm"), p("var/lib/sbm")
	old.RunDir, old.Socket = p("run/sbm"), p("run/sbm/dienst.sock")
	old.Files = [][2]string{{p("etc/sysctl.d/90-sbm-umzug.conf"), p("etc/sysctl.d/90-hostdeck-umzug.conf")}}
	home := p("root")
	files := map[string]string{
		"usr/local/lib/sbm/sbm":                  "\x7fELF sbm",
		"var/lib/sbm/protokoll.jsonl":            "",
		"var/lib/sbm/ergebnis/inventory.json":    "{}",
		"var/lib/sbm/anmeldung.json":             "{\"passwort\":\"x\"}",
		"var/lib/sbm/update/sbm":                 "\x7fELF HostDeck",
		"var/lib/sbm/update/sbm.kopf":            `{"version":"1.0.0"}`,
		"root/.sbm/passwort":                     "geheim",
		"etc/systemd/system/sbm-dienst.service":  "[Unit]\n",
		"etc/systemd/system/sbm-web.service":     "[Unit]\n",
		"etc/systemd/system/sbm-web.socket":      "[Unit]\n",
		"etc/sysctl.d/90-sbm-umzug.conf":         "vm.swappiness=10\n",
		"etc/systemd/system/sbm-schutz.service":  "[Unit]\n",
		"var/lib/sbm/schutz.json":                `{"geschlossen":[{"proto":"tcp","von":8080,"bis":8080,"seit":"2026-11-01T10:00:00Z"}]}`,
		"var/lib/sbm/schutz.nft":                 "table inet sbm_schutz {}\n",
		"var/lib/sbm/ueberwachung/verlauf.jsonl": "",
	}
	for name, text := range files {
		os.MkdirAll(filepath.Dir(p(name)), 0o755)
		if err := os.WriteFile(p(name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	os.Symlink(p("usr/local/lib/sbm/sbm"), old.BinLink)
	in.Exe = p("var/lib/sbm/update/sbm")
	fs.users["sbm-web"] = true
	r := &sbmRig{in: in, fs: fs, root: root, old: old, home: home, group: map[string]bool{"sbm-web": true}}
	base := in.Run
	in.Run = func(ctx context.Context, name string, args ...string) error {
		if name == "usermod" || name == "groupmod" {
			fs.mu.Lock()
			m := fs.users
			if name == "groupmod" {
				m = r.group
			}
			delete(m, args[2])
			m[args[1]] = true
			fs.mu.Unlock()
		}
		return base(ctx, name, args...)
	}
	r.u = findUmstiegWith(in, old, home)
	if r.u == nil {
		t.Fatal("sbm nicht erkannt")
	}
	r.u.GroupExists = func(n string) bool { fs.mu.Lock(); defer fs.mu.Unlock(); return r.group[n] }
	r.u.FindCaddy = func(context.Context) (*caddy.Caddy, error) { return nil, caddy.ErrNotFound }
	r.u.Nft = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		fs.mu.Lock()
		fs.calls = append(fs.calls, name+" "+strings.Join(args, " "))
		fs.mu.Unlock()
		return nil, nil
	}
	return r
}

func (r *sbmRig) p(s string) string { return filepath.Join(r.root, s) }

func (r *sbmRig) has(t *testing.T, call string) bool {
	t.Helper()
	for _, c := range r.fs.calls {
		if c == call {
			return true
		}
	}
	return false
}

func TestUmstiegVonSbm(t *testing.T) {
	r := newSbmRig(t)
	running := func(context.Context) (string, error) { return version, nil }
	if err := r.u.run(context.Background(), &publisher{In: r.in}, running, time.Second, "Test"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"var/lib/hostdeck/anmeldung.json", "var/lib/hostdeck/ergebnis/inventory.json", "var/lib/hostdeck/update/sbm.kopf",
		"root/.hostdeck/passwort", "usr/local/lib/hostdeck/hostdeck", "etc/sysctl.d/90-hostdeck-umzug.conf",
		"etc/systemd/system/hostdeck-dienst.service", "etc/systemd/system/hostdeck-web.service",
		"etc/systemd/system/hostdeck-schutz.service",
	} {
		if !exists(r.p(want)) {
			t.Errorf("fehlt: %s", want)
		}
	}
	for _, gone := range []string{
		"var/lib/sbm", "root/.sbm", "usr/local/lib/sbm", "usr/local/bin/sbm", "run/sbm", "etc/sysctl.d/90-sbm-umzug.conf",
		"etc/systemd/system/sbm-dienst.service", "etc/systemd/system/sbm-web.service", "etc/systemd/system/sbm-web.socket",
		"etc/systemd/system/sbm-schutz.service",
	} {
		if exists(r.p(gone)) {
			t.Errorf("noch da: %s", gone)
		}
	}
	if t2, _ := os.Readlink(r.in.BinLink); t2 != r.in.prog() {
		t.Errorf("Verknüpfung: %q", t2)
	}
	if r.in.Exe != r.p("var/lib/hostdeck/update/sbm") {
		t.Errorf("Exe: %s", r.in.Exe)
	}
	if !r.fs.users["hostdeck-web"] || r.fs.users["sbm-web"] || !r.group["hostdeck-web"] {
		t.Errorf("Benutzer: %v %v", r.fs.users, r.group)
	}
	for _, call := range []string{
		"systemctl disable --now sbm-web.socket sbm-web.service sbm-dienst.service",
		"usermod -l hostdeck-web sbm-web", "groupmod -n hostdeck-web sbm-web",
		"nft -f " + r.p("var/lib/hostdeck/schutz.nft"), "nft delete table inet sbm_schutz",
		"systemctl enable hostdeck-schutz.service", "systemctl disable sbm-schutz.service",
	} {
		if !r.has(t, call) {
			t.Errorf("Aufruf fehlt: %s\n%s", call, strings.Join(r.fs.calls, "\n"))
		}
	}
	// Erst die neue Tabelle laden, dann den neuen Dienst starten, zuletzt
	// die alte Tabelle entfernen: Kein Port ist zwischendurch offen, und
	// die Überwachung sieht die Regeln von Anfang an.
	load, start, drop := -1, -1, -1
	for i, c := range r.fs.calls {
		switch {
		case c == "nft -f "+r.p("var/lib/hostdeck/schutz.nft"):
			load = i
		case strings.HasPrefix(c, "systemctl restart hostdeck-dienst"):
			start = i
		case c == "nft delete table inet sbm_schutz":
			drop = i
		}
	}
	if load < 0 || start < load || drop < start {
		t.Errorf("Reihenfolge Port-Schutz: laden %d, Dienst %d, alte weg %d", load, start, drop)
	}
	rules, _ := os.ReadFile(r.p("var/lib/hostdeck/schutz.nft"))
	if !strings.Contains(string(rules), "table inet hostdeck_schutz") || strings.Contains(string(rules), "sbm") {
		t.Errorf("Regeln: %s", rules)
	}
	j, _ := os.ReadFile(r.p("var/lib/hostdeck/protokoll.jsonl"))
	if !strings.Contains(string(j), "Umstieg von sbm") || !strings.Contains(string(j), "Port-Schutz übernommen") {
		t.Errorf("Protokoll: %s", j)
	}
}

func TestUmstiegLegtZurueck(t *testing.T) {
	r := newSbmRig(t)
	running := func(context.Context) (string, error) { return "", errors.New("keine Antwort") }
	err := r.u.run(context.Background(), &publisher{In: r.in}, running, 10*time.Millisecond, "Test")
	if err == nil || !strings.Contains(err.Error(), "sbm") || !strings.Contains(err.Error(), "läuft wieder") {
		t.Fatalf("Fehler: %v", err)
	}
	for _, want := range []string{
		"var/lib/sbm/anmeldung.json", "var/lib/sbm/update/sbm", "root/.sbm/passwort", "usr/local/lib/sbm/sbm", "usr/local/bin/sbm",
		"etc/sysctl.d/90-sbm-umzug.conf", "etc/systemd/system/sbm-dienst.service", "etc/systemd/system/sbm-web.socket",
		"etc/systemd/system/sbm-schutz.service",
	} {
		if !exists(r.p(want)) {
			t.Errorf("nicht zurück: %s", want)
		}
	}
	for _, gone := range []string{
		"var/lib/hostdeck", "root/.hostdeck", "usr/local/lib/hostdeck", "usr/local/bin/hostdeck",
		"etc/systemd/system/hostdeck-dienst.service", "etc/systemd/system/hostdeck-web.service", "etc/sysctl.d/90-hostdeck-umzug.conf",
	} {
		if exists(r.p(gone)) {
			t.Errorf("nicht weggeräumt: %s", gone)
		}
	}
	if r.in.Exe != r.p("var/lib/sbm/update/sbm") {
		t.Errorf("Exe: %s", r.in.Exe)
	}
	if !r.fs.users["sbm-web"] || r.fs.users["hostdeck-web"] || !r.group["sbm-web"] {
		t.Errorf("Benutzer: %v %v", r.fs.users, r.group)
	}
	last := r.fs.calls[len(r.fs.calls)-1]
	if last != "systemctl enable --now sbm-web.socket sbm-web.service sbm-dienst.service" {
		t.Errorf("sbm startet nicht wieder, zuletzt: %s", last)
	}
	// Die neue Tabelle ist wieder weg, die alte nie angefasst.
	if !r.has(t, "nft delete table inet hostdeck_schutz") || r.has(t, "nft delete table inet sbm_schutz") {
		t.Errorf("Port-Schutz:\n%s", strings.Join(r.fs.calls, "\n"))
	}
	if b, _ := os.ReadFile(r.p("var/lib/sbm/schutz.nft")); string(b) != "table inet sbm_schutz {}\n" {
		t.Errorf("Regeln von sbm nicht zurück: %q", b)
	}
	j, _ := os.ReadFile(r.p("var/lib/sbm/protokoll.jsonl"))
	if !strings.Contains(string(j), "fehlgeschlagen") {
		t.Errorf("Protokoll: %s", j)
	}
}

func TestUmstiegNurMitSbm(t *testing.T) {
	in, _, _ := testInstaller(t, "")
	root := filepath.Dir(filepath.Dir(in.Exe))
	old := legacy
	old.DataDir = filepath.Join(root, "var/lib/sbm")
	if findUmstiegWith(in, old, "") != nil {
		t.Error("ohne sbm erkannt")
	}
	// Reste von sbm neben einem eingerichteten HostDeck: kein Umstieg.
	os.MkdirAll(old.DataDir, 0o700)
	os.MkdirAll(filepath.Join(in.DataDir, "ergebnis"), 0o700)
	if findUmstiegWith(in, old, "") != nil {
		t.Error("Reste als sbm erkannt")
	}
	os.WriteFile(filepath.Join(in.UnitDir, old.UnitDienst), []byte("[Unit]\n"), 0o644)
	u := findUmstiegWith(in, old, "")
	if u == nil {
		t.Fatal("Unit von sbm nicht erkannt")
	}
	u.old.RunDir = filepath.Join(root, "run/sbm")
	if err := u.prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "beide") {
		t.Errorf("beide Datenordner: %v", err)
	}
}

func TestUmstiegCaddyfile(t *testing.T) {
	u := &umstieg{old: legacy}
	cf := "# Von sbm geordnet am 01.11.2026: Jede Domain hat ihre eigene Datei in sites/.\n" +
		"# Abgeschaltete Domains liegen in sites/aus/. Die alte Fassung dieser Datei: sbm-sicherung/20261101-100000/.\n" +
		"{\n\temail a@b.example\n}\nimport sites/*.caddy\n\n" + legacy.ImportComment + "\nimport /etc/caddy/sbm.caddy\n\nx.example {\n\trespond sbm\n}\n"
	got, imp := u.caddyfile([]byte(cf), "/etc/caddy/sbm.caddy", "/etc/caddy/hostdeck.caddy", "/etc/caddy", true)
	want := "# Von HostDeck geordnet am 01.11.2026: Jede Domain hat ihre eigene Datei in sites/.\n" +
		"# Abgeschaltete Domains liegen in sites/aus/. Die alte Fassung dieser Datei: hostdeck-sicherung/20261101-100000/.\n" +
		"{\n\temail a@b.example\n}\nimport sites/*.caddy\n\n" + caddy.ImportComment + "\nimport /etc/caddy/hostdeck.caddy\n\nx.example {\n\trespond sbm\n}\n"
	if string(got) != want || !imp {
		t.Errorf("Caddyfile:\n%s\nerwartet:\n%s", got, want)
	}
	// Ohne eigene import-Zeile (von Hand eingebunden): eine neue ans Ende.
	cf = "x.example {\n\trespond hi\n}\nimport sbm.caddy\n"
	got, imp = u.caddyfile([]byte(cf), "/etc/caddy/sbm.caddy", "/etc/caddy/hostdeck.caddy", "/etc/caddy", false)
	if !imp || strings.Contains(string(got), "sbm.caddy") || !strings.Contains(string(got), "import /etc/caddy/hostdeck.caddy") {
		t.Errorf("von Hand:\n%s", got)
	}
	// Bindet ein Muster die neue Datei schon ein, bleibt es dabei.
	cf = "import /etc/caddy/*.caddy\n"
	got, imp = u.caddyfile([]byte(cf), "/etc/caddy/sbm.caddy", "/etc/caddy/hostdeck.caddy", "/etc/caddy", false)
	if imp || string(got) != cf {
		t.Errorf("Muster: %v\n%s", imp, got)
	}
	if filepath.Base(ort.CaddySite) != "hostdeck.caddy" {
		t.Error(ort.CaddySite)
	}
}
