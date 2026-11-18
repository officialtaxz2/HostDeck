package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/ort"
)

const testCaddyfile = `{
	email admin@example.de
}

import /etc/caddy/mcserver.caddy

work.example.de {
	reverse_proxy localhost:3005
}
neko.example.de {
	reverse_proxy localhost:8082
}
`

// fakeCaddy spielt caddy: validate lehnt jede Datei im Ordner ab, die
// reject enthält; adapt liefert die Hosts aus allen Dateien des Ordners.
type fakeCaddy struct {
	mu      sync.Mutex
	dir     string
	reject  []string
	reloads int
}

func (f *fakeCaddy) run(ctx context.Context, _ []string, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if name == "systemctl" {
		f.reloads++
		return nil, nil
	}
	ents, _ := os.ReadDir(f.dir)
	var all []byte
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(f.dir, e.Name()))
		all = append(all, b...)
	}
	switch args[0] {
	case "validate":
		for _, r := range f.reject {
			if bytes.Contains(all, []byte(r)) {
				return []byte("Error: unrecognized directive: " + r), errors.New("exit status 1")
			}
		}
		return nil, nil
	case "adapt":
		var routes []string
		for _, line := range strings.Split(string(all), "\n") {
			if h, ok := strings.CutSuffix(strings.TrimSpace(line), " {"); ok && strings.Contains(h, ".") {
				var hs []string
				for _, n := range strings.Split(h, ",") {
					hs = append(hs, fmt.Sprintf("%q", strings.TrimSpace(n)))
				}
				routes = append(routes, `{"match":[{"host":[`+strings.Join(hs, ",")+`]}]}`)
			}
		}
		return []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[` + strings.Join(routes, ",") + `]}}}}}`), nil
	}
	return nil, errors.New("unbekannt")
}

type pubRig struct {
	p     *publisher
	in    *installer
	sys   *fakeSystem
	caddy *fakeCaddy
	dir   string // Ordner des Caddyfiles
	steps []dienst.Step
	site  int // so oft antwortet die neue Seite noch nicht
}

func newPubRig(t *testing.T) *pubRig {
	t.Helper()
	in, sys, _ := testInstaller(t, "")
	installedOld(t, in)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "Caddyfile")
	os.WriteFile(cfg, []byte(testCaddyfile), 0o644)
	fc := &fakeCaddy{dir: dir, reject: []string{"hostdeck_probe_absichtlich_kaputt"}}
	r := &pubRig{in: in, sys: sys, caddy: fc, dir: dir}
	dns := map[string][]net.IP{
		"work.example.de":     {net.ParseIP("203.0.113.10")},
		"neko.example.de":     {net.ParseIP("203.0.113.10")},
		"hostdeck.example.de": {net.ParseIP("203.0.113.10")},
		"alt.example.de":      {net.ParseIP("198.51.100.7")},
		"halb.example.de":     {net.ParseIP("203.0.113.10"), net.ParseIP("2001:db8::99")},
	}
	r.p = &publisher{
		In: in,
		Find: func(context.Context) (*caddy.Caddy, error) {
			return &caddy.Caddy{Bin: "caddy", Config: cfg, Service: caddy.Unit, User: "caddy", Group: "caddy", Run: fc.run, TempDir: t.TempDir()}, nil
		},
		Resolve: func(_ context.Context, h string) ([]net.IP, error) {
			if ips, ok := dns[h]; ok {
				return ips, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: h, IsNotFound: true}
		},
		Local: func() []net.IP { return []net.IP{net.ParseIP("10.0.0.5")} },
		Probe: func(context.Context, string) caddy.Answer { return caddy.Answer{Status: 200} },
		Site: func(context.Context, string) error {
			if r.site > 0 {
				r.site--
				return errors.New("tls: internal error")
			}
			return nil
		},
		Web:   func(context.Context, string) error { return nil },
		Wait:  50 * time.Millisecond,
		Every: time.Millisecond,
	}
	return r
}

func (r *pubRig) step(s dienst.Step) { r.steps = append(r.steps, s) }

func (r *pubRig) read(name string) string {
	b, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		return "<fehlt>"
	}
	return string(b)
}

func (r *pubRig) unit(name string) string {
	b, err := os.ReadFile(filepath.Join(r.in.UnitDir, name))
	if err != nil {
		return "<fehlt>"
	}
	return string(b)
}

// nothingChanged: Caddy wie vorher, kein Eingang für Caddy, nichts gemerkt.
func (r *pubRig) nothingChanged(t *testing.T) {
	t.Helper()
	if got := r.read("Caddyfile"); got != testCaddyfile {
		t.Fatalf("Caddyfile:\n%s", got)
	}
	if got := r.read(ort.CaddySite); got != "<fehlt>" {
		t.Fatalf("hostdeck.caddy liegt da:\n%s", got)
	}
	if got := r.unit(ort.UnitSocket); got != "<fehlt>" {
		t.Fatalf("Socket-Unit liegt da:\n%s", got)
	}
	if strings.Contains(r.unit(ort.UnitWeb), "--adresse") {
		t.Fatal("Website hat noch eine Adresse")
	}
	if st, _ := loadAccess(r.in.DataDir); st != nil {
		t.Fatalf("Freischaltung gemerkt: %+v", st)
	}
}

func TestFreischaltenPruefen(t *testing.T) {
	r := newPubRig(t)
	ctx := context.Background()
	for raw, want := range map[string]string{
		"":                "Bitte eine Adresse eingeben",
		"1.2.3.4":         "statt einer IP-Adresse",
		"hostdeck":        "mit Punkt",
		"hostdeck_x.de":   "keine gültige Adresse",
		"neko.example.de": "antwortet schon eine andere Seite",
		"neu.example.de":  "noch keinen DNS-Eintrag. Lege bei deinem DNS-Anbieter einen A-Eintrag an, der auf 203.0.113.10 zeigt",
		"alt.example.de":  "zeigt auf 198.51.100.7, dieser Server hat aber 203.0.113.10",
		"halb.example.de": "zeigt auch auf 2001:db8::99, und das ist nicht dieser Server",
	} {
		if _, err := r.p.Check(ctx, raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", raw, err)
		}
	}
	plan, err := r.p.Check(ctx, " https://HOSTDECK.example.de/ ")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Domain != "hostdeck.example.de" || plan.Pages != 2 || len(plan.Files) != 2 {
		t.Fatalf("Plan: %+v", plan)
	}
	if !strings.Contains(plan.Files[0].Text, "reverse_proxy unix/"+ort.CaddySocket) || !strings.Contains(plan.Files[1].Text, "import "+filepath.Join(r.dir, ort.CaddySite)) {
		t.Fatalf("Dateien: %+v", plan.Files)
	}
	if st := r.p.State(ctx); !st.Caddy || st.Suggest != "hostdeck.example.de" || st.Domain != "" {
		t.Fatalf("Stand: %+v", st)
	}
	r.nothingChanged(t)
}

func TestFreischaltenUndZuruecknehmen(t *testing.T) {
	r := newPubRig(t)
	r.site = 2 // Caddy braucht kurz für das Zertifikat
	ctx := context.Background()
	if err := r.p.Publish(ctx, "hostdeck.example.de", r.step); err != nil {
		t.Fatalf("%v\n%+v", err, r.steps)
	}
	cf := r.read("Caddyfile")
	if !strings.HasPrefix(cf, testCaddyfile) || !strings.HasSuffix(cf, "import "+filepath.Join(r.dir, ort.CaddySite)+"\n") {
		t.Fatalf("Caddyfile:\n%s", cf)
	}
	if !strings.Contains(r.read(ort.CaddySite), "hostdeck.example.de {\n") || !strings.Contains(r.read(ort.CaddySite), "\n\theader Alt-Svc clear\n\treverse_proxy unix/"+ort.CaddySocket+"\n}") {
		t.Fatalf("hostdeck.caddy:\n%s", r.read(ort.CaddySite))
	}
	sock := r.unit(ort.UnitSocket)
	for _, want := range []string{"ListenStream=" + ort.CaddySocket, "SocketGroup=caddy", "SocketMode=0660", "FileDescriptorName=caddy"} {
		if !strings.Contains(sock, want) {
			t.Errorf("Socket-Unit ohne %q", want)
		}
	}
	if !strings.Contains(r.unit(ort.UnitWeb), "web --adresse hostdeck.example.de\n") {
		t.Fatalf("Website-Unit:\n%s", r.unit(ort.UnitWeb))
	}
	calls := strings.Join(r.sys.calls, "\n")
	if !strings.Contains(calls, "systemctl stop hostdeck-web.service\nsystemctl restart hostdeck-web.socket\nsystemctl start hostdeck-web.service") {
		t.Fatalf("Aufrufe:\n%s", calls)
	}
	if strings.Contains(calls, "restart hostdeck-dienst") {
		t.Fatal("Freischalten startet den Dienst neu, der den Auftrag gerade ausführt")
	}
	var names []string
	for _, s := range r.steps {
		if s.State != caddy.StateRunning {
			names = append(names, s.Name+"="+s.State)
		}
	}
	want := "Adresse prüfen=ok,Prüfung mit kaputter Datei proben=ok,Eingang für Caddy anlegen=ok,Deine Seiten vorher abfragen=ok,Caddy-Dateien sichern und ändern=ok,Caddy prüft die Dateien=ok,Caddy neu laden=ok,Neue Seite prüfen=ok,Deine Seiten mit vorher vergleichen=ok,Freischaltung merken=ok"
	if strings.Join(names, ",") != want {
		t.Fatalf("Schritte:\n%s", strings.Join(names, "\n"))
	}
	st := r.p.State(ctx)
	if st.Domain != "hostdeck.example.de" || st.Since == nil {
		t.Fatalf("Stand: %+v", st)
	}
	if _, err := r.p.Check(ctx, "hostdeck.example.de"); err == nil || !strings.Contains(err.Error(), "schon unter hostdeck.example.de freigeschaltet") {
		t.Fatalf("zweimal: %v", err)
	}

	// Ein Update schreibt die Units neu und behält die Freischaltung.
	r.sys.calls = nil
	if err := r.in.install(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.unit(ort.UnitWeb), "--adresse hostdeck.example.de") || !strings.Contains(r.unit(ort.UnitSocket), "SocketGroup=caddy") {
		t.Fatal("Update hat die Freischaltung verloren")
	}
	if !strings.Contains(strings.Join(r.sys.calls, "\n"), "systemctl enable hostdeck-dienst.service hostdeck-web.service hostdeck-web.socket") {
		t.Fatalf("Aufrufe: %v", r.sys.calls)
	}

	// Zurücknehmen: alles wie vor dem Freischalten.
	r.steps = nil
	if err := r.p.Unpublish(ctx, r.step); err != nil {
		t.Fatalf("%v\n%+v", err, r.steps)
	}
	r.nothingChanged(t)
	if !strings.Contains(strings.Join(r.sys.calls, "\n"), "systemctl disable --now hostdeck-web.socket") {
		t.Fatalf("Socket nicht gestoppt: %v", r.sys.calls)
	}
	if err := r.p.Unpublish(ctx, r.step); err == nil {
		t.Fatal("zweimal zurücknehmen")
	}
}

// Nach dem Zurückspielen eines Backups auf einen neu aufgesetzten Server
// stehen hostdeck.caddy und die import-Zeile schon da, eine Freischaltung kennt
// HostDeck aber nicht (Labor S11). Die Adresse ist dann keine fremde Seite:
// HostDeck übernimmt die Datei, und Zurücknehmen räumt alles weg wie sonst.
func TestFreischaltenUebernimmtZurueckgespielteDatei(t *testing.T) {
	r := newPubRig(t)
	ctx := context.Background()
	site := filepath.Join(r.dir, ort.CaddySite)
	if err := os.WriteFile(site, siteBlock("hostdeck.example.de"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(r.dir, "Caddyfile")
	if err := os.WriteFile(cfg, caddy.AddImport([]byte(testCaddyfile), site), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := r.p.Check(ctx, "hostdeck.example.de")
	if err != nil {
		t.Fatalf("zurückgespielte eigene Datei gilt als fremde Seite: %v", err)
	}
	if plan.Pages != 2 || plan.Files[0].New || !strings.Contains(plan.Files[0].Note, "liegt schon da") {
		t.Fatalf("Plan: %+v", plan)
	}
	// Eine fremde Datei mit derselben ersten Zeile, aber anderer Adresse,
	// macht keine fremde Adresse frei.
	if _, err := r.p.Check(ctx, "neko.example.de"); err == nil || !strings.Contains(err.Error(), "antwortet schon eine andere Seite") {
		t.Fatalf("fremde Seite: %v", err)
	}
	if err := r.p.Publish(ctx, "hostdeck.example.de", r.step); err != nil {
		t.Fatalf("%v\n%+v", err, r.steps)
	}
	if st := r.p.State(ctx); st.Domain != "hostdeck.example.de" {
		t.Fatalf("Stand: %+v", st)
	}
	if err := r.p.Unpublish(ctx, r.step); err != nil {
		t.Fatalf("%v\n%+v", err, r.steps)
	}
	r.nothingChanged(t)
}

func TestFreischaltenKaputtWirdZurueckgelegt(t *testing.T) {
	r := newPubRig(t)
	// caddy validate lehnt auch den echten Eintrag ab.
	r.caddy.reject = append(r.caddy.reject, "reverse_proxy unix/")
	err := r.p.Publish(context.Background(), "hostdeck.example.de", r.step)
	var undone *caddy.ErrUndone
	if !errors.As(err, &undone) || undone.Step != caddy.StepValidate {
		t.Fatalf("Ergebnis: %v", err)
	}
	r.nothingChanged(t)
	if r.caddy.reloads != 0 {
		t.Fatal("trotz roter Prüfung neu geladen")
	}
	last := r.steps[len(r.steps)-1]
	if last.Name != stepGateClose || last.State != caddy.StateUndone {
		t.Fatalf("letzter Schritt: %+v", last)
	}
}

func TestFreischaltenOhneZertifikatWirdZurueckgelegt(t *testing.T) {
	r := newPubRig(t)
	r.site = 1 << 30
	err := r.p.Publish(context.Background(), "hostdeck.example.de", r.step)
	var undone *caddy.ErrUndone
	if !errors.As(err, &undone) || undone.Step != caddy.StepCheck || !strings.Contains(err.Error(), "Port 80 oder 443") {
		t.Fatalf("Ergebnis: %v", err)
	}
	r.nothingChanged(t)
	if r.caddy.reloads != 2 {
		t.Fatalf("Neuladen: %d", r.caddy.reloads)
	}
}

func TestFreischaltenOhneProbeDesRueckwegs(t *testing.T) {
	r := newPubRig(t)
	r.caddy.reject = nil // validate nimmt alles an: ein Fehler wäre nicht zu erkennen
	err := r.p.Publish(context.Background(), "hostdeck.example.de", r.step)
	if err == nil || !strings.Contains(err.Error(), "Probe vor dem Freischalten") || !strings.Contains(err.Error(), "nichts geändert") {
		t.Fatalf("Ergebnis: %v", err)
	}
	r.nothingChanged(t)
	if len(r.sys.calls) != 0 {
		t.Fatalf("Units angefasst: %v", r.sys.calls)
	}
}

func TestFreischaltenBefehl(t *testing.T) {
	r := newPubRig(t)
	var out, errOut bytes.Buffer
	code := runFreischalten(context.Background(), r.p, "hostdeck.example.de", false, false, strings.NewReader("nein\n"), &out, &errOut)
	if code != 1 || !strings.Contains(out.String(), "Nichts geändert.") || !strings.Contains(out.String(), "import "+filepath.Join(r.dir, ort.CaddySite)) {
		t.Fatalf("%d\n%s\n%s", code, out.String(), errOut.String())
	}
	r.nothingChanged(t)
	out.Reset()
	if code := runFreischalten(context.Background(), r.p, "hostdeck.example.de", false, false, strings.NewReader("ja\n"), &out, &errOut); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "erreichbar unter https://hostdeck.example.de") || !strings.Contains(out.String(), "✓ Caddy prüft die Dateien") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
}

// Strg+C oder Neustart des Dienstes, während HostDeck aufs Zertifikat wartet:
// Caddy lädt die zurückgelegten Dateien, der Eingang geht zu, und der
// Leitstand sagt „wie vorher“.
func TestFreischaltenAbgebrochen(t *testing.T) {
	r := newPubRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	r.p.Site = func(context.Context, string) error { cancel(); return context.Canceled }
	err := r.p.Publish(ctx, "hostdeck.example.de", r.step)
	var u *dienst.UnchangedError
	if !errors.As(err, &u) {
		t.Fatalf("Ergebnis: %v", err)
	}
	r.nothingChanged(t)
	if r.caddy.reloads != 2 {
		t.Fatalf("Neuladen: %d", r.caddy.reloads)
	}
	if _, err := os.Stat(r.p.journalPath()); err == nil {
		t.Fatal("Journal liegt noch")
	}
}

// Absturz mitten im Freischalten: Beim nächsten Start legt HostDeck alles
// zurück und schließt den Eingang.
func TestFreischaltenNachAbsturz(t *testing.T) {
	r := newPubRig(t)
	ctx := context.Background()
	crash := map[string][]byte{}
	r.p.Site = func(context.Context, string) error {
		for _, p := range []string{r.p.journalPath(), filepath.Join(r.dir, "Caddyfile"), filepath.Join(r.dir, ort.CaddySite)} {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Errorf("%s: %v", p, err)
			}
			crash[p] = b
		}
		return errors.New("Strom weg")
	}
	r.p.Publish(ctx, "hostdeck.example.de", r.step)
	// So sah es beim Absturz aus: Dateien geändert, Eingang offen.
	for p, b := range crash {
		os.WriteFile(p, b, 0o600)
	}
	if err := r.in.applyUnits(ctx, &accessState{Domain: "hostdeck.example.de", Group: "caddy"}, false); err != nil {
		t.Fatal(err)
	}
	reloads := r.caddy.reloads
	msg, err := r.p.recoverChange(ctx)
	if err != nil || !strings.Contains(msg, "unterbrochenes Freischalten") {
		t.Fatalf("Aufräumen: %q %v", msg, err)
	}
	r.nothingChanged(t)
	if r.caddy.reloads != reloads+1 {
		t.Fatal("Caddy nicht neu geladen")
	}
	if msg, err := r.p.recoverChange(ctx); msg != "" || err != nil {
		t.Fatalf("zweites Aufräumen: %q %v", msg, err)
	}
}

// Caddy läuft nicht: Zurücknehmen nimmt die Dateien trotzdem heraus.
// Ein Caddyfile ohne Zeilenende am Schluss ist danach byte-gleich.
func TestZuruecknehmenOhneCaddy(t *testing.T) {
	r := newPubRig(t)
	ctx := context.Background()
	cfg := filepath.Join(r.dir, "Caddyfile")
	orig := strings.TrimSuffix(testCaddyfile, "\n")
	os.WriteFile(cfg, []byte(orig), 0o644)
	if err := r.p.Publish(ctx, "hostdeck.example.de", r.step); err != nil {
		t.Fatalf("%v\n%+v", err, r.steps)
	}
	find := r.p.Find
	r.p.Find = func(context.Context) (*caddy.Caddy, error) { return nil, caddy.ErrNotFound }
	if err := r.p.Unpublish(ctx, r.step); err != nil {
		t.Fatal(err)
	}
	if got := r.read("Caddyfile"); got != orig {
		t.Fatalf("Caddyfile nicht byte-gleich:\n%q", got)
	}
	if r.read(ort.CaddySite) != "<fehlt>" || r.unit(ort.UnitSocket) != "<fehlt>" {
		t.Fatal("hostdeck.caddy oder Socket-Unit liegt noch")
	}
	r.p.Find = find
}

// Eine Datei aus einer älteren Version (ohne „header Alt-Svc clear“)
// frischt das nächste Einrichten oder Update auf, mit derselben Prüfung.
func TestLeitstandDateiAuffrischen(t *testing.T) {
	r := newPubRig(t)
	ctx := context.Background()
	if err := r.p.Publish(ctx, "hostdeck.example.de", r.step); err != nil {
		t.Fatalf("%v\n%+v", err, r.steps)
	}
	site := filepath.Join(r.dir, ort.CaddySite)
	old := "# Leitstand von HostDeck.\nhostdeck.example.de {\n\treverse_proxy unix/" + ort.CaddySocket + "\n}\n"
	if err := os.WriteFile(site, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := r.p.Refresh(ctx)
	if err != nil || !strings.Contains(msg, "aufgefrischt") || !strings.Contains(msg, "HTTP/3") {
		t.Fatalf("Refresh: %q, %v", msg, err)
	}
	if r.read(ort.CaddySite) != string(siteBlock("hostdeck.example.de")) {
		t.Fatalf("Datei:\n%s", r.read(ort.CaddySite))
	}
	if _, err := os.Stat(r.p.journalPath()); !os.IsNotExist(err) {
		t.Fatalf("Journal liegt noch: %v", err)
	}
	// Schon aktuell: nichts zu tun, Caddy wird nicht angefasst.
	before := r.caddy.reloads
	if msg, err := r.p.Refresh(ctx); msg != "" || err != nil {
		t.Fatalf("zweites Mal: %q, %v", msg, err)
	}
	if r.caddy.reloads != before {
		t.Fatal("Caddy ohne Grund neu geladen")
	}

	// Lehnt Caddy die neue Datei ab, liegt die alte wieder da.
	os.WriteFile(site, []byte(old), 0o644)
	r.caddy.reject = append(r.caddy.reject, "Alt-Svc")
	msg, err = r.p.Refresh(ctx)
	if err == nil {
		t.Fatalf("abgelehnte Datei ging durch: %q", msg)
	}
	if r.read(ort.CaddySite) != old {
		t.Fatalf("alte Datei nicht zurück:\n%s", r.read(ort.CaddySite))
	}
	if _, err := os.Stat(r.p.journalPath()); !os.IsNotExist(err) {
		t.Fatalf("Journal nach dem Zurücklegen: %v", err)
	}
}

func TestLeitstandDateiAuffrischenOhneFreischaltung(t *testing.T) {
	r := newPubRig(t)
	if msg, err := r.p.Refresh(context.Background()); msg != "" || err != nil {
		t.Fatalf("%q, %v", msg, err)
	}
}
