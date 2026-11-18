package dienst

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/umzugstag"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

// fakeMoves spielt einen Probe-Umzug: Zeilen, eine Frage mit Knöpfen, dann
// Ende oder Abbruch.
type fakeMoves struct {
	mu     sync.Mutex
	ready  bool
	target MoveTarget
	plan   bool
	kind   MoveKind
	finish chan int
}

func (f *fakeMoves) Run(ctx context.Context, t MoveTarget, kind MoveKind, in backup.Input, out io.Writer) int {
	f.mu.Lock()
	f.target, f.plan, f.kind = t, kind == MovePlan, kind
	f.mu.Unlock()
	fmt.Fprintf(out, "Verbinde mit %s\n", t)
	a, err := in.Ask(ctx, backup.Question{Text: "Tippe ja:", Note: "Ist das dein neuer Server?", Choices: []backup.Choice{{Label: "Ja", Answer: "ja"}, {Label: "Abbrechen", Answer: "nein"}}})
	if err != nil {
		fmt.Fprintln(out, "abgebrochen")
		return backup.ExitFailed
	}
	fmt.Fprintln(out, "Antwort:", a)
	select {
	case code := <-f.finish:
		return code
	case <-ctx.Done():
		fmt.Fprintln(out, "abgebrochen")
		return backup.ExitFailed
	}
}

const testBackupPW = "backup-geheim-55"

func (f *fakeMoves) BackupPasswordOK(pw string) (bool, error) { return pw == testBackupPW, nil }

func (f *fakeMoves) State(context.Context) MoveState {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.ready {
		return MoveState{Problem: "Auf diesem Server liegt noch kein gültiges Backup."}
	}
	return MoveState{Ready: true}
}

func moveState(t *testing.T, r *rig, sid string) MoveReply {
	t.Helper()
	code, b := r.api(t, "GET", "/api/umzug", sid, "")
	if code != 200 {
		t.Fatalf("Stand: %d %s", code, b)
	}
	var rep MoveReply
	if err := json.Unmarshal([]byte(b), &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

func waitMove(t *testing.T, r *rig, sid string, cond func(*BackupJob) bool) *BackupJob {
	t.Helper()
	for i := 0; i < 300; i++ {
		if rep := moveState(t, r, sid); rep.Job != nil && cond(rep.Job) {
			return rep.Job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Auftrag kommt nicht an: %+v", moveState(t, r, sid).Job)
	return nil
}

func TestProbeUmzugImLeitstand(t *testing.T) {
	fm := &fakeMoves{finish: make(chan int, 1)}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Moves = fm })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/umzug", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	body := `{"server":"198.51.100.20","benutzer":"root","passwort":"server-geheim-77","backup_passwort":"` + testBackupPW + `"}`
	// Bestätigung nötig, auch für den Probelauf.
	for _, p := range []string{"/api/umzug/probelauf", "/api/umzug/starten"} {
		if code, b := r.api(t, "POST", p, sid, body); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
			t.Fatalf("%s ohne Bestätigung: %d %s", p, code, b)
		}
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	// Ohne gültiges Backup geht nichts.
	if code, b := r.api(t, "POST", "/api/umzug/starten", sid, body); code != http.StatusConflict || !strings.Contains(b, "kein gültiges Backup") {
		t.Fatalf("ohne Backup: %d %s", code, b)
	}
	fm.mu.Lock()
	fm.ready = true
	fm.mu.Unlock()
	for _, bad := range []string{
		`{"server":"","passwort":"x","backup_passwort":"x"}`,
		`{"server":"a b","passwort":"x","backup_passwort":"x"}`,
		`{"server":"198.51.100.20","passwort":"","backup_passwort":"x"}`,
		`{"server":"127.0.0.1","passwort":"x","backup_passwort":"x"}`,
		`{"server":"localhost","passwort":"x","backup_passwort":"x"}`,
		`{"server":"hostdeck.localhost","passwort":"x","backup_passwort":"x"}`,
		`{"server":"198.51.100.20","port":70000,"passwort":"x","backup_passwort":"x"}`,
		`{"server":"198.51.100.20","benutzer":"Root;rm","passwort":"x","backup_passwort":"x"}`,
		`{"server":"198.51.100.20","passwort":"x"}`,
	} {
		if code, b := r.api(t, "POST", "/api/umzug/starten", sid, bad); code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, code, b)
		}
	}
	// Das Leitstand-Passwort allein reicht nicht: Das Backup-Passwort muss
	// stimmen, sonst startet nichts.
	wrong := `{"server":"198.51.100.20","passwort":"server-geheim-77","backup_passwort":"geraten"}`
	if code, b := r.api(t, "POST", "/api/umzug/probelauf", sid, wrong); code != http.StatusForbidden || !strings.Contains(b, "Backup-Passwort stimmt nicht") {
		t.Fatalf("falsches Backup-Passwort: %d %s", code, b)
	}
	if rep := moveState(t, r, sid); rep.Job != nil {
		t.Fatalf("trotz falschem Backup-Passwort gestartet: %+v", rep.Job)
	}
	if code, b := r.api(t, "POST", "/api/umzug/starten", sid, body); code != http.StatusAccepted {
		t.Fatalf("Start: %d %s", code, b)
	}
	job := waitMove(t, r, sid, func(j *BackupJob) bool { return j.Question != nil })
	if job.Question.Text != "Ist das dein neuer Server?" || len(job.Question.Choices) != 2 {
		t.Fatalf("Frage: %+v", job.Question)
	}
	fm.mu.Lock()
	tg := fm.target
	fm.mu.Unlock()
	if tg.Host != "198.51.100.20" || tg.Port != 22 || tg.User != "root" || tg.Password != "server-geheim-77" {
		t.Fatalf("Ziel: %+v", tg)
	}
	// Zweiter Lauf wird abgewiesen.
	if code, _ := r.api(t, "POST", "/api/umzug/starten", sid, body); code != http.StatusConflict {
		t.Fatalf("zweiter Lauf: %d", code)
	}
	if code, _ := r.api(t, "POST", "/api/umzug/antwort", sid, fmt.Sprintf(`{"id":%d,"antwort":"ja"}`, job.Question.ID)); code != http.StatusNoContent {
		t.Fatalf("Antwort: %d", code)
	}
	waitMove(t, r, sid, func(j *BackupJob) bool { return strings.Contains(strings.Join(j.Lines, "\n"), "Antwort: ja") })
	fm.finish <- 0
	job = waitMove(t, r, sid, func(j *BackupJob) bool { return !j.Running })
	if job.Code != 0 {
		t.Fatalf("Ende: %+v", job)
	}
	rep := moveState(t, r, sid)
	if rep.Target != "root@198.51.100.20" {
		t.Fatalf("Ziel im Stand: %q", rep.Target)
	}
	if i, err := lauf.CurrentIn(r.d.RunDir); err == nil && i != nil {
		t.Fatalf("Sperre noch gehalten: %+v", i)
	}
	// Das Passwort des neuen Servers steht nirgends.
	_, journal := r.api(t, "GET", "/api/protokoll", sid, "")
	if !strings.Contains(journal, "Probe-Umzug auf root@198.51.100.20 gestartet") || !strings.Contains(journal, "fertig") || !strings.Contains(journal, "Falsches Backup-Passwort (Probelauf Umzug)") {
		t.Fatalf("Protokoll: %s", journal)
	}
	for _, s := range []string{journal, fmt.Sprintf("%+v", rep)} {
		if strings.Contains(s, "server-geheim-77") || strings.Contains(s, testBackupPW) || strings.Contains(s, "geraten") {
			t.Fatal("Passwort des neuen Servers oder Backup-Passwort taucht auf")
		}
	}
	if b, err := os.ReadFile(r.d.Journal.File); err == nil && (strings.Contains(string(b), "server-geheim-77") || strings.Contains(string(b), testBackupPW)) {
		t.Fatal("Passwort im Protokoll")
	}
	fm.mu.Lock()
	tg = fm.target
	fm.mu.Unlock()
	if strings.Contains(fmt.Sprintf("%+v", tg), testBackupPW) {
		t.Fatal("Backup-Passwort an Moves weitergereicht")
	}
}

func TestProbeUmzugAbbrechenImLeitstand(t *testing.T) {
	fm := &fakeMoves{finish: make(chan int, 1), ready: true}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Moves = fm })
	ctx := context.Background()
	r.c.SetPassword(ctx, testPW)
	sid, _ := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, _ := r.api(t, "POST", "/api/umzug/abbrechen", sid, ""); code != http.StatusConflict {
		t.Fatalf("ohne Lauf: %d", code)
	}
	if code, b := r.api(t, "POST", "/api/umzug/probelauf", sid, `{"server":"neu.example.de","port":2222,"passwort":"x","backup_passwort":"`+testBackupPW+`"}`); code != http.StatusAccepted {
		t.Fatalf("Start: %d %s", code, b)
	}
	waitMove(t, r, sid, func(j *BackupJob) bool { return j.Question != nil })
	if code, _ := r.api(t, "POST", "/api/umzug/abbrechen", sid, ""); code != http.StatusNoContent {
		t.Fatalf("Abbrechen: %d", code)
	}
	job := waitMove(t, r, sid, func(j *BackupJob) bool { return !j.Running })
	if !job.Cancelled || !job.Plan || job.Code != backup.ExitFailed {
		t.Fatalf("Ende: %+v", job)
	}
	if rep := moveState(t, r, sid); rep.Target != "root@neu.example.de:2222" {
		t.Fatalf("Ziel: %q", rep.Target)
	}
}

// Fünf falsche Backup-Passwörter sperren den Probe-Umzug, auch für das
// richtige.
func TestProbeUmzugBackupPasswortSperre(t *testing.T) {
	fm := &fakeMoves{finish: make(chan int, 1), ready: true}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Moves = fm })
	ctx := context.Background()
	r.c.SetPassword(ctx, testPW)
	sid, _ := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	for i := 0; i < 5; i++ {
		if code, b := r.api(t, "POST", "/api/umzug/probelauf", sid, `{"server":"198.51.100.20","passwort":"x","backup_passwort":"falsch"}`); code != http.StatusForbidden {
			t.Fatalf("Versuch %d: %d %s", i, code, b)
		}
	}
	if code, b := r.api(t, "POST", "/api/umzug/probelauf", sid, `{"server":"198.51.100.20","passwort":"x","backup_passwort":"`+testBackupPW+`"}`); code != http.StatusTooManyRequests {
		t.Fatalf("nach fünf Fehlversuchen: %d %s", code, b)
	}
	_, journal := r.api(t, "GET", "/api/protokoll", sid, "")
	if !strings.Contains(journal, "Probe-Umzug für 15 Minuten gesperrt") {
		t.Fatalf("Protokoll: %s", journal)
	}
}

// Probe-Kopie anhalten und entfernen: nur mit Bestätigung, ohne
// Backup-Passwort und auch ohne gültiges Backup hier; die Art kommt bei
// Moves an und steht im Protokoll.
func TestProbeKopieAnhaltenEntfernen(t *testing.T) {
	fm := &fakeMoves{finish: make(chan int, 1)}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Moves = fm })
	ctx := context.Background()
	r.c.SetPassword(ctx, testPW)
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"server":"198.51.100.20","passwort":"server-geheim-77"}`
	for _, p := range []string{"/api/umzug/probe-anhalten", "/api/umzug/probe-entfernen"} {
		if code, b := r.api(t, "POST", p, sid, body); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
			t.Fatalf("%s ohne Bestätigung: %d %s", p, code, b)
		}
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b := r.api(t, "POST", "/api/umzug/probe-entfernen", sid, `{"server":"198.51.100.20"}`); code != http.StatusBadRequest {
		t.Fatalf("ohne Passwort des Servers: %d %s", code, b)
	}
	for i, k := range []MoveKind{MoveHalt, MoveRemove} {
		if code, b := r.api(t, "POST", "/api/umzug/"+string(k), sid, body); code != http.StatusAccepted {
			t.Fatalf("%s: %d %s", k, code, b)
		}
		job := waitMove(t, r, sid, func(j *BackupJob) bool { return j.Question != nil })
		if job.Kind != string(k) || job.Plan {
			t.Fatalf("Auftrag: %+v", job)
		}
		fm.mu.Lock()
		got := fm.kind
		fm.mu.Unlock()
		if got != k {
			t.Fatalf("Art bei Moves: %q", got)
		}
		r.api(t, "POST", "/api/umzug/antwort", sid, fmt.Sprintf(`{"id":%d,"antwort":"ja"}`, job.Question.ID))
		fm.finish <- 0
		waitMove(t, r, sid, func(j *BackupJob) bool { return !j.Running && j.Done != nil })
		_, journal := r.api(t, "GET", "/api/protokoll", sid, "")
		want := []string{"Probe-Kopie auf 198.51.100.20 angehalten", "Probe-Kopie auf 198.51.100.20 entfernt"}[i]
		if !strings.Contains(journal, want) {
			t.Fatalf("Protokoll ohne %q: %s", want, journal)
		}
	}
}

type dnsMoves struct {
	fakeMoves
	extra []string
}

func (m *dnsMoves) DNS(context.Context) DNSReply {
	return DNSReply{Target: "198.51.100.20", Here: []string{"203.0.113.1"}, Extra: m.extra, Domains: []DNSDomain{
		{Name: "a.example", State: DNSStateOf([]DNSRecord{{Type: "A", IP: "198.51.100.20", Where: DNSNew}})}}}
}

func (m *dnsMoves) SetExtraDomains(list []string) error {
	m.extra = list
	return nil
}

func TestUmzugDNS(t *testing.T) {
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Moves = &dnsMoves{} })
	ctx := context.Background()
	r.c.SetPassword(ctx, testPW)
	if code, _ := r.api(t, "GET", "/api/umzug/dns", "", ""); code != http.StatusUnauthorized {
		t.Errorf("ohne Anmeldung: %d", code)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	code, b := r.api(t, "GET", "/api/umzug/dns", sid, "")
	if code != http.StatusOK || !strings.Contains(b, `"stand":"neu"`) || !strings.Contains(b, `"ziel":"198.51.100.20"`) {
		t.Errorf("DNS: %d %s", code, b)
	}
}

func TestDNSStateOf(t *testing.T) {
	r := func(w ...string) []DNSRecord {
		var out []DNSRecord
		for _, x := range w {
			out = append(out, DNSRecord{Where: x})
		}
		return out
	}
	for want, recs := range map[string][]DNSRecord{
		DNSStateNew: r(DNSNew, DNSNew), DNSStateOld: r(DNSHere), DNSStateMixed: r(DNSNew, DNSHere),
		DNSStateOther: r(DNSNew, DNSOther), DNSStateError: nil,
	} {
		if got := DNSStateOf(recs); got != want {
			t.Errorf("%v: %s statt %s", recs, got, want)
		}
	}
}

func TestProbeKopieMachtKeineUmzuegeUndBackups(t *testing.T) {
	fm := &fakeMoves{finish: make(chan int, 1), ready: true}
	since := time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
	r, _ := doorRigWith(t, nil, func(d *Dienst) {
		d.Moves = fm
		d.Backups = &fakeBackups{finish: make(chan int, 1), dir: t.TempDir()}
		d.Copy = func() *CopyInfo { return &CopyInfo{From: "vps1", Since: since, TestDomain: "hostdeck-neu.example.de"} }
	})
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	body := `{"server":"198.51.100.20","benutzer":"root","passwort":"server-geheim-77","backup_passwort":"` + testBackupPW + `"}`
	for _, p := range []string{"/api/umzug/probelauf", "/api/umzug/starten", "/api/umzug/probe-anhalten", "/api/backup/starten", "/api/backup/probelauf"} {
		if code, b := r.api(t, "POST", p, sid, body); code != http.StatusConflict || !strings.Contains(b, "Probe-Kopie von vps1") {
			t.Errorf("%s: %d %s", p, code, b)
		}
	}
	st, err := r.c.Status(ctx)
	if err != nil || st.Kopie == nil || st.Kopie.From != "vps1" || st.Kopie.TestDomain != "hostdeck-neu.example.de" {
		t.Errorf("Status %+v %v", st.Kopie, err)
	}
}

// Ein abgelöster Server sagt es in /api/lauf, damit der Leitstand es auf
// jeder Seite zeigt (alte Tabs halten die Verbindung zum alten Server).
func TestStatusNenntAbloesung(t *testing.T) {
	at := time.Date(2026, 11, 4, 19, 22, 0, 0, time.UTC)
	r, _ := doorRigWith(t, nil, func(d *Dienst) {
		d.Replaced = func() *umzugstag.Done { return &umzugstag.Done{At: at, IP: "198.51.100.20", How: "DNS"} }
	})
	st, err := r.c.Status(context.Background())
	if err != nil || st.Abgeloest == nil || st.Abgeloest.IP != "198.51.100.20" || !st.Abgeloest.At.Equal(at) {
		t.Errorf("Status %+v %v", st.Abgeloest, err)
	}
	r2, _ := doorRigWith(t, nil, nil)
	if st, err := r2.c.Status(context.Background()); err != nil || st.Abgeloest != nil {
		t.Errorf("nicht abgelöst: %+v %v", st.Abgeloest, err)
	}
}
