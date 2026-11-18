package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/altserver"
	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/dnscheck"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/remote"
	"github.com/officialtaxz2/hostdeck/internal/restore"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

func writeData(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for n, c := range files {
		p := filepath.Join(dir, filepath.FromSlash(n))
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUebernahmePaket(t *testing.T) {
	src := t.TempDir()
	writeData(t, src, map[string]string{
		ort.PasswordFile:                              `{"hash":"x"}`,
		ort.JournalFile:                               "{}\n",
		ort.AccessFile:                                `{"adresse":"hostdeck.example.de"}`,
		"ergebnis/manifest.yaml":                      "version: 1\n",
		"ergebnis/inventory.json":                     "{}",
		ort.MonitorDir + "/nachrichten.json":          "{}",
		ort.MonitorDir + "/minuten-20261103.jsonl.gz": "gz",
		ort.MonitorDir + "/warnungen.json":            "{}",
		ort.SessionsFile:                              "{}",
		ort.LockoutFile:                               "{}",
		"backup/config":                               "restic",
		"umzug/zuletzt.json":                          "{}",
		"umzug/dns-von-hand.json":                     `{"domains":["spiel.example.de"]}`,
		ort.BenchDir + "/20261109-151738.json":        "{}",
		ort.BenchDir + "/20261109-151738.zip":         "zip",
		ort.BenchDir + "/20261109-151738.claim":       "link",
		ort.BenchDir + "/arbeit/benchmark.sh":         "#!",
		ort.BenchDir + "/notizen.txt":                 "x",
	})
	os.Symlink("/etc/shadow", filepath.Join(src, ort.DomainsFile))
	pkg, names, err := packTakeover(src)
	if err == nil {
		t.Fatalf("Symlink eingepackt: %v", names)
	}
	os.Remove(filepath.Join(src, ort.DomainsFile))
	pkg, names, err = packTakeover(src)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names, " ")
	for _, want := range []string{ort.PasswordFile, ort.JournalFile, ort.AccessFile, "ergebnis/manifest.yaml", "ueberwachung/nachrichten.json", "ueberwachung/minuten-20261103.jsonl.gz",
		"leistungstest/20261109-151738.json", "leistungstest/20261109-151738.zip", "leistungstest/20261109-151738.claim", "umzug/dns-von-hand.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("fehlt %s: %s", want, got)
		}
	}
	for _, not := range []string{"warnungen", "sitzungen", "sperre", "backup", "umzug/zuletzt", "inventory", "arbeit", "notizen"} {
		if strings.Contains(got, not) {
			t.Errorf("%s eingepackt: %s", not, got)
		}
	}
	dst := t.TempDir()
	if _, err := unpackTakeover(pkg, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "ergebnis/manifest.yaml")); string(b) != "version: 1\n" {
		t.Errorf("manifest %q", b)
	}
	if fi, err := os.Stat(filepath.Join(dst, ort.PasswordFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("Rechte %v %v", fi, err)
	}

	// Fremde Namen lehnt das Auspacken ab und schreibt dann gar nichts.
	for _, bad := range []string{ort.SessionsFile, "../x", "ueberwachung/../anmeldung.json", "/etc/passwd", "ueberwachung/warnungen.json", "ergebnis/inventory.json",
		"leistungstest/arbeit/benchmark.sh", "leistungstest/x.json", "leistungstest/../anmeldung.json"} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for _, n := range []string{ort.JournalFile, bad} {
			tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
			tw.Write([]byte("x"))
		}
		tw.Close()
		gz.Close()
		d := t.TempDir()
		if _, err := unpackTakeover(buf.Bytes(), d); err == nil {
			t.Errorf("%s angenommen", bad)
		}
		if _, err := os.Stat(filepath.Join(d, ort.JournalFile)); err == nil {
			t.Errorf("%s: trotzdem geschrieben", bad)
		}
	}
}

type takeoverRig struct {
	t                    *takeover
	out                  *bytes.Buffer
	installed, refreshed bool
	waited               string
	data, units, site    string
}

func newTakeoverRig(t *testing.T) *takeoverRig {
	root := t.TempDir()
	r := &takeoverRig{out: &bytes.Buffer{}, data: filepath.Join(root, "var/lib/hostdeck"), units: filepath.Join(root, "units"),
		site: filepath.Join(root, "etc/caddy/hostdeck.caddy")}
	os.MkdirAll(r.units, 0o755)
	os.MkdirAll(filepath.Dir(r.site), 0o755)
	os.WriteFile(r.site, siteBlock("hostdeck.example.de"), 0o644)
	r.t = &takeover{
		DataDir: r.data, UnitDir: r.units, Out: r.out,
		Now:        func() time.Time { return time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC) },
		Install:    func(context.Context) error { r.installed = true; return nil },
		CaddyGroup: func(context.Context) (string, error) { return "caddy-neu", nil },
		Refresh:    func(context.Context) (string, error) { r.refreshed = true; return "aufgefrischt", nil },
		Site:       func(_ context.Context, d string) error { r.waited = d; return nil },
	}
	return r
}

func (r *takeoverRig) pkg(t *testing.T, extra ...string) []byte {
	src := t.TempDir()
	st := accessState{Domain: "hostdeck.example.de", Group: "caddy", Site: r.site, Caddyfile: "/etc/caddy/Caddyfile"}
	b, _ := json.Marshal(st)
	files := map[string]string{ort.PasswordFile: `{"hash":"x"}`, ort.AccessFile: string(b)}
	for i := 0; i+1 < len(extra); i += 2 {
		files[extra[i]] = extra[i+1]
	}
	writeData(t, src, files)
	pkg, _, err := packTakeover(src)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestUebernahmeProbeKopie(t *testing.T) {
	r := newTakeoverRig(t)
	cfg := remote.Config{Action: remote.ActionTakeover, Probe: true, Package: r.pkg(t), From: "vps1", TestDomain: "hostdeck-neu.example.de"}
	if code := r.t.Run(context.Background(), cfg); code != restore.ExitOK {
		t.Fatalf("Code %d\n%s", code, r.out)
	}
	if !r.installed || !r.refreshed || r.waited != "hostdeck-neu.example.de" {
		t.Errorf("installiert %v, aufgefrischt %v, gewartet auf %q", r.installed, r.refreshed, r.waited)
	}
	st, err := loadAccess(r.data)
	if err != nil || st == nil || st.Group != "caddy-neu" || len(st.Extra) != 1 || st.Extra[0] != "hostdeck-neu.example.de" {
		t.Fatalf("Zugang %+v %v", st, err)
	}
	c := copyInfo(r.data)
	if c == nil || c.From != "vps1" || c.TestDomain != "hostdeck-neu.example.de" {
		t.Fatalf("Markierung %+v", c)
	}
	if !strings.Contains(r.out.String(), "✓ Probe-Kopie des Leitstands: https://hostdeck-neu.example.de") {
		t.Errorf("Ausgabe:\n%s", r.out)
	}
	// Ein zweiter Probe-Umzug darf die Kopie erneuern.
	r.out.Reset()
	if code := r.t.Run(context.Background(), cfg); code != restore.ExitOK {
		t.Fatalf("zweites Mal: Code %d\n%s", code, r.out)
	}
}

// Was HostDeck auf dem alten Server eingestellt hatte, zieht als
// Vorschlag mit; ein späteres Auffrischen ohne nimmt es wieder weg.
func TestUebernahmeEinstellungenVomAltenServer(t *testing.T) {
	r := newTakeoverRig(t)
	cfg := remote.Config{Action: remote.ActionTakeover, Probe: true, From: "vps1",
		Package: r.pkg(t, ort.ShieldFile, `{"geschlossen":[{"proto":"tcp","von":3552,"bis":3552,"wer":"arcane"}]}`,
			ort.PerfDir+"/zustand.json", `{"logs":{"datei":"/etc/docker/daemon.json"}}`)}
	if code := r.t.Run(context.Background(), cfg); code != restore.ExitOK {
		t.Fatalf("Code %d\n%s", code, r.out)
	}
	old := altserver.Load(r.data)
	if old == nil || !old.Has("tcp", 3552, 3552) || !old.Logs || old.Swap != 0 || old.Tuning != nil {
		t.Fatalf("vom alten Server: %+v", old)
	}
	if !strings.Contains(r.out.String(), "Einstellungen vom alten Server: 1 geschlossener Port, Log-Grenze; der Leitstand hier schlägt sie unter Sicherheit und Leistung neu vor") {
		t.Errorf("Ausgabe:\n%s", r.out)
	}

	// Das Tuning von vps-tune zieht als Angebot mit.
	tr := t.TempDir()
	tuneRoot = tr
	t.Cleanup(func() { tuneRoot = "" })
	writeData(t, tr, map[string]string{tuning.AppliedFile: "preset=mixed zram=on\nboot=mitigations=off\n", tuning.PrioList: "docker.service\n",
		tuning.PrioDropIn("docker.service"): "[Service]\nCPUWeight=20\n"})
	cfg.Package = r.pkg(t, ort.ShieldFile, `{"geschlossen":[{"proto":"tcp","von":3552,"bis":3552,"wer":"arcane"}]}`,
		ort.PerfDir+"/zustand.json", `{"logs":{"datei":"/etc/docker/daemon.json"}}`)
	r.out.Reset()
	if code := r.t.Run(context.Background(), cfg); code != restore.ExitOK {
		t.Fatalf("Code %d\n%s", code, r.out)
	}
	if old := altserver.Load(r.data); old == nil || old.Tuning == nil || old.Tuning.Preset != "mixed" || len(old.Prio) != 1 {
		t.Fatalf("Tuning vom alten Server: %+v", old)
	}
	if !strings.Contains(r.out.String(), "1 geschlossener Port, Log-Grenze, Tuning mixed, CPU-Vorrang für 1 Dienst; der Leitstand hier schlägt sie unter Sicherheit, Leistung und Tuning neu vor") {
		t.Errorf("Ausgabe mit Tuning:\n%s", r.out)
	}
	tuneRoot = ""
	// schutz.json selbst kommt nicht mit: Hier ist noch nichts geschlossen.
	if _, err := os.Stat(filepath.Join(r.data, ort.ShieldFile)); err == nil {
		t.Error("schutz.json übernommen")
	}
	cfg.Package = r.pkg(t)
	if code := r.t.Run(context.Background(), cfg); code != restore.ExitOK {
		t.Fatalf("Code %d\n%s", code, r.out)
	}
	if altserver.Load(r.data) != nil {
		t.Error("alter Stand bleibt nach dem Auffrischen liegen")
	}
}

func TestUebernahmeOhneCaddyDateiNurNotausgang(t *testing.T) {
	r := newTakeoverRig(t)
	pkg := r.pkg(t)
	os.Remove(r.site)
	if code := r.t.Run(context.Background(), remote.Config{Probe: true, Package: pkg, TestDomain: "hostdeck-neu.example.de"}); code != restore.ExitOK {
		t.Fatalf("Code %d\n%s", code, r.out)
	}
	if st, _ := loadAccess(r.data); st != nil || r.refreshed || r.waited != "" {
		t.Errorf("Zugang %+v, aufgefrischt %v", st, r.refreshed)
	}
	if !strings.Contains(r.out.String(), "sudo hostdeck notausgang") {
		t.Errorf("Ausgabe:\n%s", r.out)
	}
}

func TestUebernahmeLaesstEigenenLeitstand(t *testing.T) {
	for _, existing := range []string{"anmeldung", "unit"} {
		r := newTakeoverRig(t)
		if existing == "anmeldung" {
			writeData(t, r.data, map[string]string{ort.PasswordFile: "eigen"})
		} else {
			os.WriteFile(filepath.Join(r.units, ort.UnitDienst), []byte("x"), 0o644)
		}
		code := r.t.Run(context.Background(), remote.Config{Probe: true, Package: r.pkg(t)})
		if code != restore.ExitBlocked || r.installed || !strings.Contains(r.out.String(), "schon ein eigener Leitstand") {
			t.Errorf("%s: Code %d\n%s", existing, code, r.out)
		}
		if b, _ := os.ReadFile(filepath.Join(r.data, ort.PasswordFile)); existing == "anmeldung" && string(b) != "eigen" {
			t.Errorf("Passwort überschrieben")
		}
	}
	r := newTakeoverRig(t)
	if code := r.t.Run(context.Background(), remote.Config{Package: r.pkg(t)}); code != restore.ExitBlocked || r.installed {
		t.Errorf("ohne Probe: Code %d", code)
	}
}

func TestKopieLeitstandEntfernen(t *testing.T) {
	in, fs, _ := testInstaller(t, "")
	fs.users["hostdeck-web"] = true
	os.MkdirAll(in.ProgDir, 0o755)
	os.MkdirAll(in.DataDir, 0o700)
	var removed, deleted []string
	c := &copyLeitstand{in: in,
		RemoveAll: func(p string) error { removed = append(removed, p); return nil },
		DelUser:   func(_ context.Context, n string) error { deleted = append(deleted, n); return nil },
	}
	if c.Copy() || c.Remove(context.Background()) == nil {
		t.Fatal("ohne Markierung entfernt")
	}
	writeData(t, in.DataDir, map[string]string{ort.CopyFile: `{"von":"alt"}`})
	if !c.Copy() {
		t.Fatal("Markierung nicht erkannt")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(removed, " ") != in.ProgDir+" "+in.DataDir || strings.Join(deleted, " ") != "hostdeck-web" {
		t.Errorf("gelöscht %v, Benutzer %v", removed, deleted)
	}
	calls := strings.Join(fs.calls, "\n")
	if !strings.Contains(calls, "systemctl disable --now hostdeck-web.service hostdeck-dienst.service") {
		t.Errorf("Aufrufe:\n%s", calls)
	}
}

func TestTestadresse(t *testing.T) {
	for in, want := range map[string]string{"hostdeck.example.net": "hostdeck-neu.example.net", "leitstand.a.b.example": "leitstand-neu.a.b.example", "x": "", "": ""} {
		if got := testDomain(in); got != want {
			t.Errorf("%q: %q statt %q", in, got, want)
		}
	}
}

func TestProbeUmzugUebernimmtLeitstand(t *testing.T) {
	m, f, target := moveRig(t)
	data := t.TempDir()
	writeData(t, data, map[string]string{ort.PasswordFile: "{}", ort.JournalFile: "{}\n"})
	m.Pack = func() ([]byte, []string, error) { return packTakeover(data) }
	m.Own = func() string { return "hostdeck.example.de" }
	m.SiteRetry = time.Millisecond
	m.SiteThere = func(_ context.Context, h string) caddy.Answer { return caddy.Answer{Status: 200} }
	m.DNSResolver = &dnscheck.Resolver{
		NS: func(context.Context, string) ([]string, error) { return nil, errors.New("aus") },
		Addr: func(_ context.Context, h string) ([]net.IP, error) {
			if h == "hostdeck-neu.example.de" {
				return []net.IP{net.ParseIP(target.Host)}, nil
			}
			return nil, errors.New("unbekannt")
		},
	}
	var out strings.Builder
	if code := m.Run(context.Background(), target, dienst.MoveRun, &autoInput{}, &out); code != 0 || f.err != nil {
		t.Fatalf("Code %d, %v\n%s", code, f.err, out.String())
	}
	f.mu.Lock()
	tk := f.takeover
	f.mu.Unlock()
	if tk == nil {
		t.Fatalf("keine Übernahme\n%s", out.String())
	}
	if !tk.Probe || tk.TestDomain != "hostdeck-neu.example.de" || len(tk.Package) == 0 || tk.Password != "" || tk.Key != "" {
		t.Errorf("Anweisung %+v", tk)
	}
	for _, want := range []string{"Leitstand auf den neuen Server", "✓ Testadresse hostdeck-neu.example.de zeigt auf den neuen Server", "HostDeck läuft hier als Dienst", "Übertragen aus dem Backup: ",
		"✓ https://hostdeck-neu.example.de antwortet von außen"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Ausgabe ohne %q:\n%s", want, out.String())
		}
	}

	// Zeigt die Testadresse nicht dorthin, gibt es keine.
	m2, f2, target2 := moveRig(t)
	m2.Pack, m2.Own = m.Pack, m.Own
	m2.DNSResolver = &dnscheck.Resolver{
		NS:   func(context.Context, string) ([]string, error) { return nil, errors.New("aus") },
		Addr: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.9")}, nil },
	}
	out.Reset()
	if code := m2.Run(context.Background(), target2, dienst.MoveRun, &autoInput{}, &out); code != 0 {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
	f2.mu.Lock()
	tk = f2.takeover
	f2.mu.Unlock()
	if tk == nil || tk.TestDomain != "" || !strings.Contains(out.String(), "zeigt auf 203.0.113.9, nicht auf den neuen Server") {
		t.Errorf("Anweisung %+v\n%s", tk, out.String())
	}

	// Anhalten übernimmt nichts.
	m3, f3, target3 := moveRig(t)
	m3.Pack = m.Pack
	m3.Stored = func(context.Context) dienst.StoredBackups { return dienst.StoredBackups{} }
	if code := m3.Run(context.Background(), target3, dienst.MoveHalt, &autoInput{}, &out); code != 0 || f3.takeover != nil {
		t.Errorf("Anhalten: Code %d, Übernahme %v", code, f3.takeover)
	}
}

func TestLeitstandMitTestadresse(t *testing.T) {
	u := unitWeb("/usr/local/lib/hostdeck/hostdeck", "hostdeck-web", "hostdeck.example.de", "hostdeck-neu.example.de")
	if !strings.Contains(u, "ExecStart=/usr/local/lib/hostdeck/hostdeck web --adresse hostdeck.example.de --auch hostdeck-neu.example.de\n") {
		t.Errorf("Unit:\n%s", u)
	}
	if u := unitWeb("/p", "w", "hostdeck.example.de"); strings.Contains(u, "--auch") {
		t.Errorf("ohne Testadresse:\n%s", u)
	}
	if b := string(siteBlock("hostdeck.example.de", "hostdeck-neu.example.de")); !strings.Contains(b, "\nhostdeck.example.de, hostdeck-neu.example.de {\n") {
		t.Errorf("Caddy-Datei:\n%s", b)
	}
	ok := web.ExactHosts("hostdeck.example.de", "HOSTDECK-neu.example.de.")
	for h, want := range map[string]bool{"hostdeck.example.de": true, "hostdeck-neu.example.de": true, "evil.example.de": false, "": false} {
		if ok(h) != want {
			t.Errorf("%q: %v", h, !want)
		}
	}
}
