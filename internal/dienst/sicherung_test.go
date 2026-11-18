package dienst

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/archiv"
	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

// fakeBackups spielt einen Lauf: Er schreibt Zeilen, stellt eine Frage
// und wartet dann auf finish oder Abbruch.
type fakeBackups struct {
	mu      sync.Mutex
	ready   bool
	dir     string
	finish  chan int
	answer  string
	plans   int
	removed int
}

func (f *fakeBackups) Run(ctx context.Context, plan bool, in backup.Input, out io.Writer) int {
	f.mu.Lock()
	if plan {
		f.plans++
	}
	f.mu.Unlock()
	fmt.Fprint(out, "Schritt 1\nFortschritt 10 %\rFortschritt 50 %\r")
	if _, err := in.Ask(ctx, backup.Question{Text: "Geheim?", Secret: true}); err == nil {
		fmt.Fprintln(out, "Geheimfrage im Browser gestellt")
	}
	a, err := in.Ask(ctx, backup.Question{Text: "Enter = jetzt prüfen", Note: "Spielserver stoppen", Choices: []backup.Choice{{Label: "Jetzt prüfen"}, {Label: "Lauf abbrechen", Answer: "a"}}})
	if err != nil {
		fmt.Fprintln(out, "abgebrochen:", err)
		return backup.ExitFailed
	}
	f.mu.Lock()
	f.answer = a
	f.mu.Unlock()
	fmt.Fprintln(out, "Antwort:", a)
	select {
	case code := <-f.finish:
		return code
	case <-ctx.Done():
		fmt.Fprintln(out, "abgebrochen")
		return backup.ExitFailed
	}
}

func (f *fakeBackups) Stored(context.Context) StoredBackups {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := StoredBackups{Ready: true, Keep: 2}
	if !f.ready {
		st = StoredBackups{Problem: "Das Backup-Passwort fehlt. Lege es am Server an: sudo hostdeck backup-passwort", Keep: 2}
	}
	if _, err := os.Stat(filepath.Join(f.dir, "config")); err == nil {
		st.Runs = 1
	}
	return st
}

func (f *fakeBackups) Archive() (*archiv.Archive, string, error) {
	if _, err := os.Stat(filepath.Join(f.dir, "config")); err != nil {
		return nil, "", errors.New("auf dem Server liegt kein Backup")
	}
	a, err := archiv.Build(f.dir, "hostdeck-backup", []string{"locks"})
	return a, "hostdeck-backup-test.tar", err
}

func (f *fakeBackups) Remove(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed++
	var n int64
	es, err := os.ReadDir(f.dir)
	if err != nil {
		return 0, err
	}
	for _, e := range es {
		p := filepath.Join(f.dir, e.Name())
		_ = filepath.Walk(p, func(_ string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode().IsRegular() {
				n += fi.Size()
			}
			return nil
		})
		if err := os.RemoveAll(p); err != nil {
			return n, err
		}
	}
	return n, nil
}

func backupRig(t *testing.T) (*rig, *clock, *fakeBackups, string) {
	t.Helper()
	fb := &fakeBackups{finish: make(chan int, 1), dir: t.TempDir()}
	for name, body := range map[string]string{"config": "k", "data/ab/abc": strings.Repeat("d", 5000), "locks/x": "l"} {
		p := filepath.Join(fb.dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r, c := doorRigWith(t, nil, func(d *Dienst) { d.Backups = fb })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	return r, c, fb, sid
}

func backupState(t *testing.T, r *rig, sid string) BackupReply {
	t.Helper()
	code, b := r.api(t, "GET", "/api/backup", sid, "")
	if code != 200 {
		t.Fatalf("Stand: %d %s", code, b)
	}
	var rep BackupReply
	if err := json.Unmarshal([]byte(b), &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

// waitBackup wartet, bis cond für den Auftrag gilt.
func waitBackup(t *testing.T, r *rig, sid string, cond func(*BackupJob) bool) *BackupJob {
	t.Helper()
	for i := 0; i < 300; i++ {
		if rep := backupState(t, r, sid); rep.Job != nil && cond(rep.Job) {
			return rep.Job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Auftrag kommt nicht an: %+v", backupState(t, r, sid).Job)
	return nil
}

func TestBackupImLeitstand(t *testing.T) {
	r, _, fb, sid := backupRig(t)
	if code, _ := r.api(t, "GET", "/api/backup", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	if rep := backupState(t, r, sid); rep.Job != nil || rep.Stored.Ready || !strings.Contains(rep.Stored.Problem, "backup-passwort") {
		t.Fatalf("Anfang: %+v", rep)
	}

	// Ein Backup braucht Bestätigung und ein Passwort am Server.
	if code, b := r.api(t, "POST", "/api/backup/starten", sid, ""); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b := r.api(t, "POST", "/api/backup/starten", sid, ""); code != http.StatusConflict || !strings.Contains(b, "backup-passwort") {
		t.Fatalf("ohne Backup-Passwort: %d %s", code, b)
	}

	// Der Probelauf geht ohne Bestätigung und ohne Passwort.
	code, b := r.api(t, "POST", "/api/backup/probelauf", sid, "")
	if code != http.StatusAccepted || !strings.Contains(b, `"probelauf":true`) {
		t.Fatalf("Probelauf: %d %s", code, b)
	}
	j := waitBackup(t, r, sid, func(j *BackupJob) bool { return j.Question != nil })
	if j.Question.Text != "Spielserver stoppen" || len(j.Question.Choices) != 2 || j.Question.Choices[1].Answer != "a" {
		t.Fatalf("Frage: %+v", j.Question)
	}
	// Die geheime Frage geht nicht an den Browser.
	if got := strings.Join(j.Lines, "|"); got != "Schritt 1|Fortschritt 10 %|Fortschritt 50 %" {
		t.Fatalf("Zeilen: %q", got)
	}
	// Während des Laufs gilt die Laufsperre.
	if code, b := r.api(t, "POST", "/api/backup/probelauf", sid, ""); code != http.StatusConflict || !strings.Contains(b, "Probelauf") {
		t.Fatalf("zweiter Lauf: %d %s", code, b)
	}
	// Antwort auf eine falsche Frage zählt nicht.
	if code, _ := r.api(t, "POST", "/api/backup/antwort", sid, fmt.Sprintf(`{"id":%d,"antwort":"x"}`, j.Question.ID+1)); code != http.StatusConflict {
		t.Fatalf("falsche Frage: %d", code)
	}
	if code, b := r.api(t, "POST", "/api/backup/antwort", sid, fmt.Sprintf(`{"id":%d,"antwort":""}`, j.Question.ID)); code != http.StatusNoContent {
		t.Fatalf("Antwort: %d %s", code, b)
	}
	waitBackup(t, r, sid, func(j *BackupJob) bool {
		return j.Question == nil && strings.Contains(strings.Join(j.Lines, "|"), "Antwort:")
	})
	fb.finish <- backup.ExitOK
	j = waitBackup(t, r, sid, func(j *BackupJob) bool { return !j.Running })
	if j.Code != 0 || j.Done == nil || j.Cancelled {
		t.Fatalf("Ende: %+v", j)
	}
	// Ab Zeile N kommt nur der Rest.
	if code, b := r.api(t, "GET", fmt.Sprintf("/api/backup?ab=%d", len(j.Lines)-1), sid, ""); code != 200 || strings.Contains(b, "Schritt 1") {
		t.Fatalf("ab: %d %s", code, b)
	}

	// Jetzt ein echtes Backup, abgebrochen.
	fb.mu.Lock()
	fb.ready = true
	fb.mu.Unlock()
	if code, b := r.api(t, "POST", "/api/backup/starten", sid, ""); code != http.StatusAccepted || !strings.Contains(b, `"probelauf":false`) {
		t.Fatalf("Backup: %d %s", code, b)
	}
	waitBackup(t, r, sid, func(j *BackupJob) bool { return j.Question != nil })
	if code, _ := r.api(t, "POST", "/api/backup/abbrechen", sid, ""); code != http.StatusNoContent {
		t.Fatalf("Abbrechen: %d", code)
	}
	j = waitBackup(t, r, sid, func(j *BackupJob) bool { return !j.Running })
	if !j.Cancelled || j.Code != backup.ExitFailed {
		t.Fatalf("abgebrochen: %+v", j)
	}
	if code, _ := r.api(t, "POST", "/api/backup/abbrechen", sid, ""); code != http.StatusConflict {
		t.Fatalf("zweimal abbrechen: %d", code)
	}
	// Die Sperre ist wieder frei.
	if l, err := lauf.TakeIn(r.d.RunDir, "Test"); err != nil {
		t.Fatalf("Sperre noch belegt: %v", err)
	} else {
		l.Release()
	}
	jr := journal(t, r)
	for _, want := range []string{
		"lauf|Probelauf gestartet|203.0.113.9",
		"lauf|Probelauf fertig: alles bereit für ein Backup|203.0.113.9",
		"lauf|Backup gestartet|203.0.113.9",
		"lauf|Abbrechen angefordert|203.0.113.9",
		"lauf|Backup abgebrochen; gestoppte Container laufen wieder|203.0.113.9",
	} {
		if !strings.Contains(jr, want) {
			t.Errorf("Protokoll ohne %q:\n%s", want, jr)
		}
	}
}

func TestBackupAusgabeBegrenzt(t *testing.T) {
	d := &Dienst{}
	run := &backupRun{job: &BackupJob{Lines: []string{}}}
	out := jobOutput{d: d, run: run}
	for i := 0; i < maxJobLines+10; i++ {
		fmt.Fprintf(out, "Zeile %d\n", i)
	}
	if len(run.job.Lines) != maxJobLines || run.job.First != 10 || run.job.Lines[0] != "Zeile 10" {
		t.Fatalf("%d Zeilen ab %d: %q", len(run.job.Lines), run.job.First, run.job.Lines[0])
	}
	d.brun = run
	j := d.backupSnapshot(maxJobLines + 8)
	if j.First != maxJobLines+8 || len(j.Lines) != 2 || j.Lines[1] != fmt.Sprintf("Zeile %d", maxJobLines+9) {
		t.Fatalf("Ausschnitt: %d %q", j.First, j.Lines)
	}
	// \r\n zählt als ein Zeilenende.
	run2 := &backupRun{job: &BackupJob{Lines: []string{}}}
	fmt.Fprint(jobOutput{d: d, run: run2}, "a\r\nb\r\n")
	if got := strings.Join(run2.job.Lines, "|"); got != "a|b" {
		t.Fatalf("CRLF: %q", got)
	}
}

func download(t *testing.T, r *rig, path, sid string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://hostdeck-dienst"+path, nil)
	req.Header.Set(web.SessionHeader, sid)
	req.Header.Set(web.AddrHeader, "203.0.113.9")
	req.Header.Set(web.GateHeader, "Test")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := r.c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestBackupHerunterladen(t *testing.T) {
	r, c, fb, sid := backupRig(t)
	if code, b := r.api(t, "POST", "/api/backup/herunterladen", sid, ""); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	link := func() string {
		code, b := r.api(t, "POST", "/api/backup/herunterladen", sid, "")
		var v struct {
			Pfad string `json:"pfad"`
		}
		if err := json.Unmarshal([]byte(b), &v); code != 200 || err != nil || !strings.HasPrefix(v.Pfad, web.DownloadPath) {
			t.Fatalf("Link: %d %s", code, b)
		}
		return v.Pfad
	}
	p := link()

	// Eine andere Anmeldung kann den Link nicht benutzen.
	other, _ := r.c.Door().Login(context.Background(), testPW, web.Visitor{Gate: "Test", Addr: "198.51.100.1"})
	if resp, _ := download(t, r, p, other, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("fremde Anmeldung: %d", resp.StatusCode)
	}
	resp, full := download(t, r, p, sid, nil)
	if resp.StatusCode != 200 || resp.Header.Get("ETag") == "" || !strings.Contains(resp.Header.Get("Content-Disposition"), "hostdeck-backup-test.tar") {
		t.Fatalf("Download: %d %v", resp.StatusCode, resp.Header)
	}
	tr := tar.NewReader(bytes.NewReader(full))
	var names []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	if got := strings.Join(names, " "); !strings.Contains(got, "hostdeck-backup/data/ab/abc") || strings.Contains(got, "locks/x") {
		t.Fatalf("Inhalt: %s", got)
	}
	etag := resp.Header.Get("ETag")

	// Fortsetzen geht auch nach Ablauf der Startzeit.
	c.add(10 * time.Minute)
	resp, part := download(t, r, p, sid, map[string]string{"Range": "bytes=1000-", "If-Range": etag})
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(part, full[1000:]) {
		t.Fatalf("Fortsetzen: %d, %d Bytes", resp.StatusCode, len(part))
	}
	// Hat sich das Backup geändert, kommt die ganze Datei neu.
	if err := os.WriteFile(filepath.Join(fb.dir, "config"), []byte("neu"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, again := download(t, r, p, sid, map[string]string{"Range": "bytes=1000-", "If-Range": etag})
	if resp.StatusCode != 200 || resp.Header.Get("ETag") == etag || len(again) != len(full) {
		t.Fatalf("geändert: %d %s", resp.StatusCode, resp.Header.Get("ETag"))
	}

	// Ein neuer Link verfällt, wenn ihn niemand anfängt.
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	p2 := link()
	c.add(DownloadTTL + time.Second)
	if resp, _ := download(t, r, p2, sid, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("abgelaufener Link: %d", resp.StatusCode)
	}

	// Während eines anderen Laufs geht kein Download.
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	p3 := link()
	l, err := lauf.TakeIn(r.d.RunDir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	resp, b := download(t, r, p3, sid, nil)
	l.Release()
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(b), "Backup") {
		t.Fatalf("mit Sperre: %d %s", resp.StatusCode, b)
	}
	// N7: Hält etwas anderes die Sperre (etwa ein Container-Neustart),
	// geht der Download trotzdem, und er hält selbst keine Sperre.
	l, err = lauf.TakeIn(r.d.RunDir, "Projekt pensum: Neu starten")
	if err != nil {
		t.Fatal(err)
	}
	resp, _ = download(t, r, p3, sid, nil)
	l.Release()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("neben einer Container-Aktion: %d", resp.StatusCode)
	}
	// Der Handler zählt erst nach dem letzten Byte herunter; der Client kann
	// dann schon fertig gelesen haben. Kurz warten, aber nicht ewig.
	for end := time.Now().Add(2 * time.Second); r.d.Downloading() && time.Now().Before(end); {
		time.Sleep(10 * time.Millisecond)
	}
	if r.d.Downloading() {
		t.Error("Download zählt nach dem Ende noch")
	}
	jr := journal(t, r)
	if !strings.Contains(jr, "download|Herunterladen gestartet: hostdeck-backup-test.tar") || !strings.Contains(jr, "download|Herunterladen fortgesetzt: hostdeck-backup-test.tar") {
		t.Errorf("Protokoll:\n%s", jr)
	}
}
