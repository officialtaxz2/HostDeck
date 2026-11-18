package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/dnscheck"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/remote"
	"github.com/officialtaxz2/hostdeck/internal/restore"
	"github.com/officialtaxz2/hostdeck/internal/umzugstag"
)

// dayRig ist ein alter Server mit letztem Lauf, gestoppten Containern und
// einer Server-Ebene mit geheimem SSH-Schlüssel.
type dayRig struct {
	m       *serverMoves
	f       *fakeTarget
	target  dienst.MoveTarget
	run     string // neuester Lauf
	off     int    // aus gelassene Container
	finals  int    // letzte Läufe
	finalFn func() int
	started int // Rückweg: gestartet
	now     time.Time
}

func newDayRig(t *testing.T) *dayRig {
	t.Helper()
	m, f, target := moveRig(t)
	r := &dayRig{m: m, f: f, target: target, run: "20261101-205600", now: time.Date(2026, 11, 5, 19, 0, 0, 0, time.UTC)}
	last := time.Date(2026, 11, 1, 20, 56, 0, 0, time.UTC)
	m.Stored = func(context.Context) dienst.StoredBackups {
		return dienst.StoredBackups{Ready: true, Runs: 1, Last: &last, LastRun: r.run}
	}
	m.Now = func() time.Time { return r.now }
	m.Final = func(ctx context.Context, in backup.Input, w io.Writer) int {
		r.finals++
		fmt.Fprintln(w, "hostdeck backup --letzter-lauf: Lauf 20261105-190000")
		if r.finalFn != nil {
			return r.finalFn()
		}
		r.run, r.off = "20261105-190000", 3
		return backup.ExitOK
	}
	m.LeftOff = func() int { return r.off }
	m.Restart = func(ctx context.Context, w io.Writer) []string {
		r.started += r.off
		r.off = 0
		fmt.Fprintln(w, "  starte app-web-1")
		return nil
	}
	root := t.TempDir()
	for p, s := range map[string]string{
		"/etc/passwd":               "root:x:0:0::/root:/bin/bash\n",
		"/root/.ssh/id_ed25519":     "PRIVAT-GEHEIM\n",
		"/root/.ssh/id_ed25519.pub": "ssh-ed25519 AAAA\n",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(s), 0o600)
	}
	m.Root = root
	inv := &inventory.Inventory{}
	m.Inventory = func() (*inventory.Inventory, error) { return inv, nil }
	if err := m.ChooseServer([]string{"zugang:ssh:/root"}); err != nil {
		t.Fatal(err)
	}
	return r
}

// probe macht die Generalprobe (Probe-Umzug ohne Befund).
func (r *dayRig) probe(t *testing.T) {
	t.Helper()
	var out strings.Builder
	if code := r.m.Run(context.Background(), r.target, dienst.MoveRun, &autoInput{}, &out); code != 0 {
		t.Fatalf("Generalprobe: Code %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "✓ Bestanden") || !strings.Contains(out.String(), "Probe-Kopie anhalten") {
		t.Fatalf("Generalprobe ohne Ergebnis:\n%s", out.String())
	}
	r.f.mu.Lock()
	r.f.actions, r.f.server = nil, nil
	r.f.mu.Unlock()
}

func TestUmzugstag(t *testing.T) {
	r := newDayRig(t)
	ctx := context.Background()

	// Ohne Generalprobe startet nichts.
	var out strings.Builder
	if code := r.m.Run(ctx, r.target, dienst.MoveDay, &autoInput{}, &out); code != restore.ExitBlocked || r.finals != 0 {
		t.Fatalf("ohne Generalprobe: Code %d, %d letzte Läufe\n%s", code, r.finals, out.String())
	}
	if !strings.Contains(out.String(), "Zuerst die Generalprobe") {
		t.Errorf("Grund fehlt:\n%s", out.String())
	}
	r.probe(t)
	if st := r.m.DayState(ctx); !st.Ready || st.Probe == nil || !st.Probe.OK || st.Probe.Host != r.target.Host {
		t.Fatalf("nach der Generalprobe: %+v", st)
	}
	// Auf einen anderen Server nicht.
	if why := r.m.DayProblem(ctx, "198.51.100.99"); !strings.Contains(why, "nur auf den Server der Generalprobe") {
		t.Errorf("anderer Server: %q", why)
	}

	out.Reset()
	if code := r.m.Run(ctx, r.target, dienst.MoveDay, &autoInput{}, &out); code != 0 || r.f.err != nil {
		t.Fatalf("Umzugstag: Code %d, %v\n%s", code, r.f.err, out.String())
	}
	r.f.mu.Lock()
	actions, restored, sv := strings.Join(r.f.actions, " "), r.f.restored, r.f.server
	r.f.mu.Unlock()
	if r.finals != 1 || actions != "probe-entfernen restore server-ebene" || restored != "20261105-190000" {
		t.Errorf("Ablauf: %d letzte Läufe, Aktionen %q, Lauf %q", r.finals, actions, restored)
	}
	if sv == nil || sv.Server == nil || !sv.Server.Apply {
		t.Fatalf("Server-Ebene nicht übernommen: %+v", sv)
	}
	b, _ := json.Marshal(sv.Server)
	if !strings.Contains(string(b), base64.StdEncoding.EncodeToString([]byte("PRIVAT-GEHEIM\n"))) {
		t.Error("am Umzugstag fehlen die Inhalte")
	}
	// Inhalte stehen nirgends sonst.
	secret := []string{"PRIVAT", base64.StdEncoding.EncodeToString([]byte("PRIVAT-GEHEIM\n"))}
	for _, s := range secret {
		if strings.Contains(out.String(), s) {
			t.Errorf("Schlüssel in der Ausgabe")
		}
	}
	filepath.Walk(r.m.Dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, s := range secret {
			if strings.Contains(string(b), s) {
				t.Errorf("Schlüssel in %s", p)
			}
		}
		return nil
	})

	st := r.m.DayState(ctx)
	if st.Day == nil || st.Day.Phase != umzugstag.PhaseDNS || st.Day.FinalRun != "20261105-190000" {
		t.Fatalf("Stand: %+v", st.Day)
	}
	for _, s := range st.Day.Steps {
		want := umzugstag.StepOK
		if s.Key == umzugstag.StepDNS {
			want = umzugstag.StepRunning
		}
		if s.State != want {
			t.Errorf("Schritt %s: %s (%s)", s.Key, s.State, s.Note)
		}
	}
	if st.Ready || !strings.Contains(st.Problem, "wartet auf die DNS-Umstellung") {
		t.Errorf("zweiter Umzugstag möglich: %+v", st)
	}
	if dayBusy(r.m.Dir) == "" {
		t.Error("Überwachung warnt während des Umzugs")
	}

	// DNS zeigt noch hierher: nicht abgelöst. Dann umgestellt: abgelöst.
	where := "203.0.113.1"
	r.m.Sites = func(context.Context) ([]string, error) { return []string{"a.example.de"}, nil }
	r.m.LocalIPs = func() []net.IP { return []net.IP{net.ParseIP("203.0.113.1")} }
	r.m.DNSResolver = &dnscheck.Resolver{
		NS:   func(context.Context, string) ([]string, error) { return nil, errors.New("aus") },
		Addr: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP(where)}, nil },
	}
	if msg := r.m.checkSwitched(ctx); msg != "" {
		t.Fatalf("abgelöst, obwohl DNS hierher zeigt: %s", msg)
	}
	where = r.target.Host
	if msg := r.m.checkSwitched(ctx); !strings.Contains(msg, "abgelöst") {
		t.Fatalf("nicht abgelöst: %q", msg)
	}
	st = r.m.DayState(ctx)
	if st.Done == nil || st.Done.IP != r.target.Host || st.Day.Phase != umzugstag.PhaseDone {
		t.Fatalf("nach DNS: %+v %+v", st.Done, st.Day)
	}
	if why := r.m.DayProblem(ctx, r.target.Host); !strings.Contains(why, "abgelöst am") {
		t.Errorf("Umzugstag nach Ablösung möglich: %q", why)
	}
	if err := r.m.Finish(ctx); err == nil || !strings.Contains(err.Error(), "Schon abgeschlossen") {
		t.Errorf("von Hand abschließen nach der Ablösung: %v", err)
	}

	// Rückweg nach der Ablösung fragt nach und nimmt sie zurück.
	out.Reset()
	in := &autoInput{}
	if code := r.m.Run(ctx, dienst.MoveTarget{}, dienst.MoveBack, in, &out); code != 0 || r.started != 3 {
		t.Fatalf("Rückweg: Code %d, %d gestartet\n%s", code, r.started, out.String())
	}
	if len(in.asked) != 1 || !strings.Contains(in.asked[0].Note, "fehlt hier") {
		t.Errorf("Rückweg nach der Ablösung ohne Warnung: %+v", in.asked)
	}
	// Der neue Server erfährt vom Rückweg nichts; Rückfrage
	// und Ergebnis sagen, dass die Projekte dort weiterlaufen.
	if len(in.asked) == 1 && !strings.Contains(in.asked[0].Note, "laufen die Projekte danach weiter") {
		t.Errorf("Rückfrage nennt den weiterlaufenden neuen Server nicht: %q", in.asked[0].Note)
	}
	if !strings.Contains(out.String(), "Auf dem neuen Server") || !strings.Contains(out.String(), "unter Projekte an") {
		t.Errorf("Ergebnis nennt den weiterlaufenden neuen Server nicht:\n%s", out.String())
	}
	st = r.m.DayState(ctx)
	if st.Done != nil || st.Day.Phase != umzugstag.PhaseBack || st.Day.FinalRun != "" || dayBusy(r.m.Dir) != "" {
		t.Errorf("nach dem Rückweg: %+v %+v", st.Done, st.Day)
	}
}

// Fällt der neue Server nach dem letzten Lauf aus, bleibt der Ablauf
// stehen; erneut gestartet, nimmt er den letzten Lauf von vorhin.
func TestUmzugstagStehenGebliebenUndErneut(t *testing.T) {
	r := newDayRig(t)
	ctx := context.Background()
	r.probe(t)
	r.f.mu.Lock()
	r.f.failOn = remote.ActionRestore
	r.f.mu.Unlock()
	var out strings.Builder
	if code := r.m.Run(ctx, r.target, dienst.MoveDay, &autoInput{}, &out); code != restore.ExitFailed {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
	d := r.m.days().Day()
	if d == nil || d.Phase != umzugstag.PhaseFailed || d.Step(umzugstag.StepRestore).State != umzugstag.StepFailed || d.Step(umzugstag.StepCopy).State != umzugstag.StepOpen {
		t.Fatalf("Stand: %+v", d)
	}
	if !strings.Contains(out.String(), "Zurück zum alten Server") || dayBusy(r.m.Dir) == "" {
		t.Errorf("Rückweg nicht angeboten:\n%s", out.String())
	}
	r.f.mu.Lock()
	r.f.failOn, r.f.actions = "", nil
	r.f.mu.Unlock()
	out.Reset()
	if code := r.m.Run(ctx, r.target, dienst.MoveDay, &autoInput{}, &out); code != 0 {
		t.Fatalf("erneut: Code %d\n%s", code, out.String())
	}
	if r.finals != 1 || !strings.Contains(out.String(), "von vorhin gilt weiter") {
		t.Errorf("letzter Lauf wiederholt (%d)\n%s", r.finals, out.String())
	}
}

// Geht schon der letzte Lauf schief, startet das Backup die Projekte
// wieder; dann ist nichts passiert und kein Rückweg nötig.
func TestUmzugstagLetzterLaufGehtNicht(t *testing.T) {
	r := newDayRig(t)
	r.probe(t)
	r.finalFn = func() int { return backup.ExitFailed }
	var out strings.Builder
	if code := r.m.Run(context.Background(), r.target, dienst.MoveDay, &autoInput{}, &out); code != restore.ExitFailed {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
	d := r.m.days().Day()
	if d.Phase != umzugstag.PhaseBack || d.Active() || len(r.f.actions) != 0 {
		t.Errorf("Stand %s, Aktionen %v", d.Phase, r.f.actions)
	}
	if !strings.Contains(out.String(), "Die Projekte hier laufen weiter") {
		t.Errorf("Ausgabe:\n%s", out.String())
	}
}

// Die alte Generalprobe gilt drei Tage.
func TestGeneralprobeVeraltet(t *testing.T) {
	r := newDayRig(t)
	r.probe(t)
	r.now = r.now.Add(73 * time.Hour)
	if why := r.m.DayProblem(context.Background(), r.target.Host); !strings.Contains(why, "älter als 3 Tage") {
		t.Errorf("%q", why)
	}
}

// Nach der Übernahme ist der neue Server keine
// Probe-Kopie mehr; Anhalten und Entfernen verweigern dort.
func TestUebernahmeNachDNS(t *testing.T) {
	data, state := t.TempDir(), t.TempDir()
	mk, _ := json.Marshal(copyMarker{From: "alt", Since: time.Now(), Move: true})
	os.WriteFile(filepath.Join(data, ort.CopyFile), mk, 0o600)
	rec, _ := json.Marshal(restore.Record{Run: "20261105-190000", Source: "alt"})
	os.WriteFile(restore.RecordPath(state), rec, 0o600)

	res := &dnscheck.Resolver{
		NS: func(context.Context, string) ([]string, error) { return nil, errors.New("aus") },
		Addr: func(_ context.Context, h string) ([]net.IP, error) {
			if h == "hostdeck.example.de" {
				return []net.IP{net.ParseIP("198.51.100.20")}, nil
			}
			return []net.IP{net.ParseIP("203.0.113.1")}, nil
		},
	}
	here := []net.IP{net.ParseIP("198.51.100.20")}
	if !arrived(context.Background(), []string{"hostdeck.example.de"}, res, here) || arrived(context.Background(), []string{"hostdeck.example.de", "alt.example.de"}, res, here) {
		t.Fatal("Ankunft falsch erkannt")
	}
	// K13: Ohne Domains übernimmt der neue Leitstand sofort; der alte wird
	// nie von selbst abgelöst (dafür bietet der Lotse „Von Hand abschließen“).
	if !arrived(context.Background(), nil, res, here) || dnsSwitched(dienst.DNSReply{Target: "198.51.100.20"}) {
		t.Fatal("Leitstand ohne Domains")
	}
	if err := promote(data, state, "DNS", time.Now()); err != nil {
		t.Fatal(err)
	}
	if loadCopy(data) != nil {
		t.Error("noch Probe-Kopie")
	}
	if _, err := os.Stat(filepath.Join(data, arrivedFile)); err != nil {
		t.Error(err)
	}
	var out strings.Builder
	p := &restore.Probe{StateDir: state, Out: &out, Confirmed: true, Input: &autoInput{}}
	if code := p.Remove(context.Background()); code != restore.ExitBlocked || !strings.Contains(out.String(), "echte Server") {
		t.Errorf("Entfernen auf dem echten Server: Code %d\n%s", code, out.String())
	}
	if err := promote(data, state, "von Hand", time.Now()); err == nil {
		t.Error("zweimal übernommen")
	}
}

// Domains von Hand kommen in die DNS-Liste.
func TestDNSVonHand(t *testing.T) {
	m := &serverMoves{Dir: t.TempDir(),
		Sites:    func(context.Context) ([]string, error) { return []string{"a.example.de"}, nil },
		LocalIPs: func() []net.IP { return []net.IP{net.ParseIP("203.0.113.1")} },
		DNSResolver: &dnscheck.Resolver{
			NS:   func(context.Context, string) ([]string, error) { return nil, errors.New("aus") },
			Addr: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.1")}, nil },
		},
	}
	if err := m.SetExtraDomains([]string{"keine domain"}); err == nil {
		t.Error("Unsinn angenommen")
	}
	if err := m.SetExtraDomains([]string{"MC.example.de.", "a.example.de", "mc.example.de"}); err != nil {
		t.Fatal(err)
	}
	rep := m.DNS(context.Background())
	var names []string
	for _, d := range rep.Domains {
		names = append(names, fmt.Sprintf("%s:%v", d.Name, d.Manual))
	}
	if strings.Join(names, " ") != "a.example.de:false mc.example.de:true" || strings.Join(rep.Extra, " ") != "a.example.de mc.example.de" {
		t.Errorf("Liste: %v, von Hand %v", names, rep.Extra)
	}
}

// Labor S5: Nach einem Rückweg hat der neue Server schon übernommen. Der
// Umzugstag hält an, bevor der letzte Lauf die Projekte hier stoppt.
func TestUmzugstagNichtAufUebernommenenServer(t *testing.T) {
	r := newDayRig(t)
	ctx := context.Background()
	r.probe(t)
	r.f.mu.Lock()
	r.f.record = `{"run":"20261105-190000","uebernommen":"2026-11-14T09:37:00Z"}`
	r.f.mu.Unlock()
	var out strings.Builder
	code := r.m.Run(ctx, r.target, dienst.MoveDay, &autoInput{}, &out)
	r.f.mu.Lock()
	actions := strings.Join(r.f.actions, " ")
	r.f.mu.Unlock()
	if code == restore.ExitOK || r.finals != 0 || actions != "" {
		t.Fatalf("Code %d, %d letzte Läufe, Aktionen %q\n%s", code, r.finals, actions, out.String())
	}
	if !strings.Contains(out.String(), "schon übernommen") && !strings.Contains(out.String(), "abgeschlossen; dort ist jetzt ein echter Server") {
		t.Errorf("Grund fehlt:\n%s", out.String())
	}
	if st := r.m.DayState(ctx); st.Day == nil || st.Day.Phase != umzugstag.PhaseBack {
		t.Errorf("Stand: %+v", st.Day)
	}
}
