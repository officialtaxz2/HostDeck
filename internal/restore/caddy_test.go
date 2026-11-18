package restore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

type fakeCaddy struct {
	present     bool
	calls       []string
	installErr  error
	validateErr error
	startOK     bool
}

func (c *fakeCaddy) Present(context.Context) bool { return c.present }
func (c *fakeCaddy) Install(context.Context) error {
	c.calls = append(c.calls, "install")
	if c.installErr != nil {
		return c.installErr
	}
	c.present = true
	return nil
}
func (c *fakeCaddy) Stop(context.Context) error { c.calls = append(c.calls, "stop"); return nil }
func (c *fakeCaddy) Validate(_ context.Context, cfg string) error {
	c.calls = append(c.calls, "validate "+cfg)
	return c.validateErr
}
func (c *fakeCaddy) Start(context.Context) (bool, string) {
	c.calls = append(c.calls, "start")
	if !c.startOK {
		return false, "bind: address already in use"
	}
	return true, ""
}
func (c *fakeCaddy) Disable(context.Context) error { c.calls = append(c.calls, "disable"); return nil }
func (c *fakeCaddy) Remove(context.Context) error {
	c.calls = append(c.calls, "remove")
	c.present = false
	return nil
}
func (c *fakeCaddy) Owner(paths []string, uid, gid int) error {
	c.calls = append(c.calls, "owner "+strings.Join(paths, ",")+" "+itoa(uid)+":"+itoa(gid))
	return nil
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

var caddyPaths = []string{"/etc/caddy", "/var/lib/caddy/.local/share/caddy"}

// withCaddy legt Caddy in den Lauf der Testumgebung.
func withCaddy(t *testing.T, e *jobEnv) *fakeCaddy {
	t.Helper()
	lauf := "/root/.hostdeck/laeufe/20261101-100000"
	var rf backup.RunFile
	var inv inventory.Inventory
	if err := json.Unmarshal([]byte(e.store.files["lauf1|"+lauf+"/lauf.json"]), &rf); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(e.store.files["lauf1|"+lauf+"/inventory.json"]), &inv); err != nil {
		t.Fatal(err)
	}
	rf.Snapshots = append(rf.Snapshots, backup.RunSnapshot{Key: CaddyKey, Strategy: backup.StratHost, Snapshot: "c1", Paths: caddyPaths})
	inv.Caddy = &inventory.Caddy{Config: "/etc/caddy/Caddyfile", Found: true, UID: 998, GID: 997}
	rb, _ := json.Marshal(rf)
	ib, _ := json.Marshal(inv)
	e.store.files["lauf1|"+lauf+"/lauf.json"] = string(rb)
	e.store.files["lauf1|"+lauf+"/inventory.json"] = string(ib)
	c := &fakeCaddy{startOK: true}
	e.job.Caddy = c
	return c
}

func TestRestoreCaddy(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	c := withCaddy(t, e)
	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	want := []string{"install", "stop", "owner /etc/caddy,/var/lib/caddy/.local/share/caddy 998:997", "validate /etc/caddy/Caddyfile", "start"}
	if !slices.Equal(c.calls, want) {
		t.Errorf("Caddy:\n%q\nerwartet\n%q", c.calls, want)
	}
	if !slices.Contains(e.store.calls, "restore c1  → /") {
		t.Errorf("Dateien von Caddy nicht zurückgespielt: %q", e.store.calls)
	}
	for _, s := range []string{"Caddy fehlt noch; HostDeck installiert es beim Zurückspielen", "Caddy samt Zertifikaten: /etc/caddy, /var/lib/caddy/.local/share/caddy", "✓ Caddy installiert", "✓ Caddy läuft"} {
		if !strings.Contains(e.out.String(), s) {
			t.Errorf("Ausgabe ohne %q:\n%s", s, e.out)
		}
	}
	rec, err := LoadRecord(e.job.StateDir)
	if err != nil || rec.Caddy == nil || !rec.Caddy.Installed || !rec.Caddy.Running || !slices.Equal(rec.Caddy.Paths, caddyPaths) {
		t.Fatalf("Protokoll Caddy: %v %+v", err, rec)
	}

	// Zweiter Probe-Umzug: Caddy ist da und stammt von HostDeck; kein Konflikt,
	// und Entfernen weiß weiterhin, dass HostDeck es installiert hat.
	e2 := newJobEnv(t, "richtig", "ja")
	c2 := withCaddy(t, e2)
	c2.present = true
	e2.job.StateDir = e.job.StateDir
	e2.store.pwFile = filepath.Join(e.job.StateDir, "passwort")
	e2.job.PasswordFile = e2.store.pwFile
	_ = os.Remove(e2.store.pwFile)
	if code := e2.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("zweiter Lauf Exit %d:\n%s", code, e2.out)
	}
	if strings.Contains(e2.out.String(), "Caddy ist hier schon installiert") || slices.Contains(c2.calls, "install") {
		t.Errorf("zweiter Lauf: %q\n%s", c2.calls, e2.out)
	}
	if rec, _ := LoadRecord(e.job.StateDir); rec == nil || rec.Caddy == nil || !rec.Caddy.Installed {
		t.Errorf("installiert vergessen: %+v", rec)
	}
}

func TestRestoreCaddyFremdUndKaputt(t *testing.T) {
	// Ein Caddy, das nicht von HostDeck kommt, steht in der Liste und braucht
	// „überschreiben“; lehnt Caddy die Dateien ab, bleibt es aus.
	e := newJobEnv(t, "richtig", "überschreiben")
	c := withCaddy(t, e)
	c.present = true
	c.validateErr = errors.New("unrecognized directive: foo")
	if code := e.job.Run(context.Background()); code != ExitFailed {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if !strings.Contains(e.out.String(), "Caddy ist hier schon installiert; HostDeck ersetzt seine Konfiguration") {
		t.Errorf("fremdes Caddy nicht gemeldet:\n%s", e.out)
	}
	if slices.Contains(c.calls, "start") || slices.Contains(c.calls, "install") {
		t.Errorf("Caddy: %q", c.calls)
	}
	if !strings.Contains(e.out.String(), "Caddy lehnt die Konfiguration ab (unrecognized directive: foo); Caddy bleibt aus") {
		t.Errorf("Ausgabe:\n%s", e.out)
	}
	rec, _ := LoadRecord(e.job.StateDir)
	if rec == nil || rec.Caddy == nil || rec.Caddy.Installed || rec.Caddy.Running {
		t.Errorf("Protokoll: %+v", rec)
	}
}

func TestRestoreCaddyProbelauf(t *testing.T) {
	e := newJobEnv(t, "richtig")
	e.job.PlanOnly = true
	c := withCaddy(t, e)
	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	if len(c.calls) != 0 || !strings.Contains(e.out.String(), "HostDeck installiert es beim Probe-Umzug") {
		t.Errorf("Probelauf: %q\n%s", c.calls, e.out)
	}
}

func TestProbeCaddy(t *testing.T) {
	rec := &Record{Run: "r1", Source: "alt", Version: "x", Caddy: &CaddyRecord{Installed: true, Paths: append(caddyPaths, "/opt/x/site.caddy")}}
	p, _, out, _ := probeRig(t, rec, "anhalten")
	c := &fakeCaddy{present: true}
	p.Caddy = c
	if code := p.Halt(context.Background()); code != ExitOK {
		t.Fatalf("Anhalten %d\n%s", code, out)
	}
	if !slices.Equal(c.calls, []string{"disable"}) || !strings.Contains(out.String(), "Caddy angehalten") {
		t.Errorf("Anhalten: %q\n%s", c.calls, out)
	}

	p, _, out, removed := probeRig(t, rec, "entfernen")
	c = &fakeCaddy{present: true}
	p.Caddy = c
	if code := p.Remove(context.Background()); code != ExitOK {
		t.Fatalf("Entfernen %d\n%s", code, out)
	}
	if !slices.Equal(c.calls, []string{"disable", "remove"}) {
		t.Errorf("Entfernen: %q", c.calls)
	}
	for _, d := range caddyPaths {
		if !slices.Contains(*removed, d) {
			t.Errorf("%s nicht entfernt: %v", d, *removed)
		}
	}
	if slices.Contains(*removed, "/opt/x/site.caddy") {
		t.Errorf("fremder Pfad entfernt: %v", *removed)
	}
	if !strings.Contains(out.String(), "Programm, Konfiguration und Zertifikate") {
		t.Errorf("Vorschau:\n%s", out)
	}

	// Caddy war schon vorher da: nur anhalten.
	rec.Caddy.Installed = false
	p, _, out, removed = probeRig(t, rec, "entfernen")
	c = &fakeCaddy{present: true}
	p.Caddy = c
	if code := p.Remove(context.Background()); code != ExitOK {
		t.Fatalf("Entfernen %d\n%s", code, out)
	}
	if !slices.Equal(c.calls, []string{"disable"}) || slices.Contains(*removed, "/etc/caddy") {
		t.Errorf("fremdes Caddy: %q %v", c.calls, *removed)
	}
}

func TestCaddyOwnPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/etc/caddy": true, "/etc/caddy/sites": true, "/var/lib/caddy/.local/share/caddy": true,
		"/etc": false, "/etc/caddyx": false, "/.local/share/caddy": false, "/opt/app": false,
	} {
		if caddyOwnPath(p) != want {
			t.Errorf("%s: %v", p, !want)
		}
	}
}

func TestCheckKey(t *testing.T) {
	armored, err := os.ReadFile("testdata/caddy-stable.gpg.key")
	if err != nil {
		t.Fatal(err)
	}
	bin, err := CheckKey(armored, CaddyKeyFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if len(bin) < 1000 || bin[0]&0x80 == 0 {
		t.Errorf("ohne Hülle: %d Bytes, %x", len(bin), bin[:2])
	}
	// Auch ohne Hülle, und ein anderer Fingerabdruck wird abgelehnt.
	if _, err := CheckKey(bin, CaddyKeyFingerprint); err != nil {
		t.Errorf("binär: %v", err)
	}
	if _, err := CheckKey(armored, "0000000000000000000000000000000000000000"); err == nil || !strings.Contains(err.Error(), "statt") {
		t.Errorf("falscher Schlüssel angenommen: %v", err)
	}
	broken := append([]byte{}, bin...)
	broken[40] ^= 0xff
	if _, err := CheckKey(broken, CaddyKeyFingerprint); err == nil {
		t.Error("veränderter Schlüssel angenommen")
	}
	if _, err := CheckKey([]byte("<html>nope</html>"), CaddyKeyFingerprint); err == nil {
		t.Error("HTML angenommen")
	}
}

func TestDebianLike(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]bool{
		"ID=debian\nVERSION_ID=\"13\"\n":            true,
		"ID=ubuntu\nID_LIKE=debian\n":               true,
		"ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n": true,
		"ID=fedora\n": false,
	} {
		f := filepath.Join(dir, "os-release")
		_ = os.WriteFile(f, []byte(body), 0o644)
		if debianLike(f) != want {
			t.Errorf("%q: %v", body, !want)
		}
	}
}

func TestCaddyPortsInCheckHost(t *testing.T) {
	p := &Plan{Caddy: &CaddyPlan{}}
	h := fakeHost{busy: map[int]bool{443: true}}
	var got []string
	for _, n := range CheckHost(context.Background(), p, h, nil) {
		got = append(got, n.Text)
	}
	if !strings.Contains(strings.Join(got, "\n"), "443 (Caddy)") {
		t.Errorf("443 nicht gemeldet: %v", got)
	}
}

type fakeLeitstand struct {
	copy  bool
	calls []string
}

func (f *fakeLeitstand) Copy() bool { return f.copy }
func (f *fakeLeitstand) Stop(context.Context) error {
	f.calls = append(f.calls, "stop")
	return nil
}
func (f *fakeLeitstand) Remove(context.Context) error {
	f.calls = append(f.calls, "remove")
	return nil
}

func TestProbeLeitstand(t *testing.T) {
	// Nur der Leitstand ist noch da: Anhalten hat trotzdem etwas zu tun.
	rec := &Record{Run: "r1", Source: "alt", Version: "x"}
	p, _, out, _ := probeRig(t, rec, "anhalten")
	ls := &fakeLeitstand{copy: true}
	p.Leitstand = ls
	if code := p.Halt(context.Background()); code != ExitOK {
		t.Fatalf("Anhalten %d\n%s", code, out)
	}
	if !slices.Equal(ls.calls, []string{"stop"}) || !strings.Contains(out.String(), "Leitstand der Kopie angehalten") {
		t.Errorf("Anhalten: %q\n%s", ls.calls, out)
	}

	p, _, out, _ = probeRig(t, rec, "entfernen")
	ls = &fakeLeitstand{copy: true}
	p.Leitstand = ls
	if code := p.Remove(context.Background()); code != ExitOK {
		t.Fatalf("Entfernen %d\n%s", code, out)
	}
	if !slices.Equal(ls.calls, []string{"remove"}) || !strings.Contains(out.String(), "Leitstand der Kopie: Dienste, Programm und Daten") {
		t.Errorf("Entfernen: %q\n%s", ls.calls, out)
	}

	// Ein eigener Leitstand (ohne Markierung) bleibt unberührt.
	p, _, out, _ = probeRig(t, rec, "entfernen")
	ls = &fakeLeitstand{}
	p.Leitstand = ls
	if code := p.Remove(context.Background()); code != ExitOK || len(ls.calls) > 0 || strings.Contains(out.String(), "Leitstand der Kopie") {
		t.Errorf("eigener Leitstand: Code %d, %q\n%s", code, ls.calls, out)
	}
}
