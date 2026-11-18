package dienst

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/leistungstest"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

// fakeBench spielt einen Test: Er schreibt Zeilen und wartet auf finish
// oder Abbruch.
type fakeBench struct {
	mu      sync.Mutex
	dir     string
	finish  chan string
	opts    []leistungstest.Options
	results []leistungstest.Result
	deleted []string
	gbGone  bool
}

func (f *fakeBench) Info() leistungstest.Info {
	return leistungstest.Info{Arch: "arm64", GeekbenchWhy: "nur x86_64", Missing: []string{"sysbench"}, Seconds: 120}
}

func (f *fakeBench) List() ([]leistungstest.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]leistungstest.Result{}, f.results...), nil
}

func (f *fakeBench) Report(id string) (leistungstest.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.results {
		if r.ID == id {
			r.Summary = "CPU-PERFORMANCEMESSUNG"
			return r, nil
		}
	}
	if !leistungstest.ValidID(id) {
		return leistungstest.Result{}, leistungstest.ErrBadID
	}
	return leistungstest.Result{}, leistungstest.ErrNotFound
}

func (f *fakeBench) Run(ctx context.Context, opt leistungstest.Options, out io.Writer) (leistungstest.Result, error) {
	f.mu.Lock()
	f.opts = append(f.opts, opt)
	f.mu.Unlock()
	fmt.Fprint(out, "START Gesamtmessung\n[ 1s ] thds: 4 eps: 4449.49\r")
	res := leistungstest.Result{ID: "20261109-120000", State: leistungstest.StateDone, SysbenchSingle: 1000, SysbenchMulti: 4000, Threads: 4}
	select {
	case st := <-f.finish:
		res.State = st
	case <-ctx.Done():
		fmt.Fprintln(out, "abgebrochen")
		res.State = leistungstest.StateCancelled
	}
	f.mu.Lock()
	f.results = append([]leistungstest.Result{res}, f.results...)
	f.mu.Unlock()
	return res, nil
}

func (f *fakeBench) ZipPath(id string) (string, error) {
	if !leistungstest.ValidID(id) {
		return "", leistungstest.ErrBadID
	}
	p := filepath.Join(f.dir, id+".zip")
	if _, err := os.Stat(p); err != nil {
		return "", leistungstest.ErrNotFound
	}
	return p, nil
}

func (f *fakeBench) Claim(id string) (string, error) {
	if id == "20261109-120000" {
		return "https://browser.geekbench.com/v6/cpu/1/claim?key=x", nil
	}
	return "", leistungstest.ErrNotFound
}

func (f *fakeBench) Delete(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeBench) RemoveGeekbench() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gbGone = true
	return 477_000_000, nil
}

func benchState(t *testing.T, r *rig, sid string) BenchReply {
	t.Helper()
	code, b := r.api(t, "GET", "/api/leistungstest", sid, "")
	if code != 200 {
		t.Fatalf("Stand: %d %s", code, b)
	}
	var rep BenchReply
	if err := json.Unmarshal([]byte(b), &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

func waitBench(t *testing.T, r *rig, sid string, cond func(*BackupJob) bool) BenchReply {
	t.Helper()
	for i := 0; i < 300; i++ {
		if rep := benchState(t, r, sid); rep.Job != nil && cond(rep.Job) {
			return rep
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Auftrag kommt nicht an: %+v", benchState(t, r, sid).Job)
	return BenchReply{}
}

func TestLeistungstestImLeitstand(t *testing.T) {
	fb := &fakeBench{dir: t.TempDir(), finish: make(chan string)}
	os.WriteFile(filepath.Join(fb.dir, "20261109-120000.zip"), []byte("PK-zip"), 0o600)
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Benchmarks = fb })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/leistungstest", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	rep := benchState(t, r, sid)
	if rep.Job != nil || len(rep.Results) != 0 || rep.Info.Missing[0] != "sysbench" {
		t.Fatalf("Anfang: %+v", rep)
	}
	// Starten braucht die Bestätigung mit dem Passwort.
	if code, b := r.api(t, "POST", "/api/leistungstest/starten", sid, `{"geekbench":false}`); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	// Geekbench geht auf diesem Server nicht; eine falsche Dauer auch nicht.
	if code, b := r.api(t, "POST", "/api/leistungstest/starten", sid, `{"geekbench":true}`); code != http.StatusConflict || !strings.Contains(b, "ohne Geekbench") {
		t.Fatalf("Geekbench auf arm64: %d %s", code, b)
	}
	if code, _ := r.api(t, "POST", "/api/leistungstest/starten", sid, `{"sekunden":3}`); code != http.StatusBadRequest {
		t.Fatalf("Dauer 3 s: %d", code)
	}
	if code, b := r.api(t, "POST", "/api/leistungstest/starten", sid, `{"geekbench":false,"sekunden":60}`); code != http.StatusAccepted {
		t.Fatalf("Start: %d %s", code, b)
	}
	waitBench(t, r, sid, func(j *BackupJob) bool { return strings.Contains(strings.Join(j.Lines, "|"), "eps") })
	// Während des Tests gilt die Laufsperre, und Löschen wartet.
	if code, b := r.api(t, "POST", "/api/leistungstest/starten", sid, `{}`); code != http.StatusConflict || !strings.Contains(b, "Leistungstest") {
		t.Fatalf("zweiter Test: %d %s", code, b)
	}
	if code, _ := r.api(t, "POST", "/api/leistungstest/loeschen", sid, `{"id":"20261109-120000"}`); code != http.StatusConflict {
		t.Fatalf("Löschen während des Tests: %d", code)
	}
	if code, _ := r.api(t, "POST", "/api/leistungstest/geekbench-entfernen", sid, ""); code != http.StatusConflict || fb.gbGone {
		t.Fatalf("Geekbench entfernen während des Tests: %d", code)
	}
	fb.finish <- leistungstest.StateDone
	rep = waitBench(t, r, sid, func(j *BackupJob) bool { return !j.Running })
	if rep.Job.Code != 0 || rep.Last != "20261109-120000" || len(rep.Results) != 1 || fb.opts[0].Seconds != 60 || fb.opts[0].Geekbench {
		t.Fatalf("Ende: %+v %+v", rep, fb.opts)
	}
	// Bericht, ZIP und Claim-Link.
	if code, b := r.api(t, "GET", "/api/leistungstest/bericht?id=20261109-120000", sid, ""); code != 200 || !strings.Contains(b, "CPU-PERFORMANCEMESSUNG") {
		t.Fatalf("Bericht: %d %s", code, b)
	}
	if code, _ := r.api(t, "GET", "/api/leistungstest/bericht?id=../x", sid, ""); code != http.StatusBadRequest {
		t.Fatalf("Bericht mit falscher Kennung: %d", code)
	}
	if code, b := r.api(t, "GET", "/api/leistungstest/zip?id=20261109-120000", sid, ""); code != 200 || b != "PK-zip" {
		t.Fatalf("ZIP: %d %q", code, b)
	}
	if code, _ := r.api(t, "GET", "/api/leistungstest/zip?id=20261109-130000", sid, ""); code != http.StatusNotFound {
		t.Fatalf("ZIP fehlt: %d", code)
	}
	if code, b := r.api(t, "GET", "/api/leistungstest/claim?id=20261109-120000", sid, ""); code != 200 || !strings.Contains(b, "claim?key=x") {
		t.Fatalf("Claim: %d %s", code, b)
	}

	// Ein zweiter Test, abgebrochen.
	if code, b := r.api(t, "POST", "/api/leistungstest/starten", sid, `{}`); code != http.StatusAccepted {
		t.Fatalf("zweiter Start: %d %s", code, b)
	}
	waitBench(t, r, sid, func(j *BackupJob) bool { return j.Running && len(j.Lines) > 0 })
	if code, _ := r.api(t, "POST", "/api/leistungstest/abbrechen", sid, ""); code != http.StatusNoContent {
		t.Fatalf("Abbrechen: %d", code)
	}
	rep = waitBench(t, r, sid, func(j *BackupJob) bool { return !j.Running })
	if !rep.Job.Cancelled || rep.Job.Code != 1 || fb.opts[1].Seconds != 0 {
		t.Fatalf("abgebrochen: %+v", rep.Job)
	}
	if code, _ := r.api(t, "POST", "/api/leistungstest/abbrechen", sid, ""); code != http.StatusConflict {
		t.Fatalf("Abbrechen ohne Test: %d", code)
	}
	if code, _ := r.api(t, "POST", "/api/leistungstest/loeschen", sid, `{"id":"20261109-120000"}`); code != 200 || fb.deleted[0] != "20261109-120000" {
		t.Fatalf("Löschen: %d %v", code, fb.deleted)
	}
	// Ein Test aus dem Terminal hält nur die Laufsperre (Nachprüfung NP2).
	term, err := lauf.TakeIn(r.d.RunDir, lauf.Leistungstest)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "POST", "/api/leistungstest/geekbench-entfernen", sid, ""); code != http.StatusConflict || fb.gbGone {
		t.Fatalf("Geekbench entfernen während eines Terminal-Tests: %d", code)
	}
	term.Release()
	if code, _ := r.api(t, "POST", "/api/leistungstest/geekbench-entfernen", sid, ""); code != 200 || !fb.gbGone {
		t.Fatalf("Geekbench entfernen: %d", code)
	}
	es, _ := r.d.Journal.Last(50)
	var texts []string
	for _, e := range es {
		texts = append(texts, e.Text)
	}
	all := strings.Join(texts, "|")
	for _, want := range []string{"Leistungstest gestartet (ohne Geekbench, Sysbench je 60 s)", "Leistungstest fertig: Sysbench 1000,00 Events/s mit 1 Thread, 4000,00 mit 4 Threads", "Leistungstest abgebrochen", "Leistungstest vom 20261109-120000 gelöscht", "Geekbench vom Server entfernt (477 MB frei)"} {
		if !strings.Contains(all, want) {
			t.Errorf("Protokoll ohne %q: %s", want, all)
		}
	}
}
