package dienst

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/umzugstag"
)

func TestSpansDecken(t *testing.T) {
	var s spans
	s = s.add(1000, 1500)
	if s.covers(1500) {
		t.Fatal("Lücke vorn zählt als vollständig")
	}
	s = s.add(0, 999)
	if s.covers(1500) {
		t.Fatal("Lücke bei 999 zählt als vollständig")
	}
	s = s.add(999, 1000)
	if !s.covers(1500) || len(s) != 1 {
		t.Fatalf("zusammengefügt: %v", s)
	}
	if got := (spans{}).add(5, 9).add(0, 2).add(20, 30); len(got) != 3 || got[0][0] != 0 || got[2][0] != 20 {
		t.Fatalf("sortiert: %v", got)
	}
}

func TestServedFrom(t *testing.T) {
	for _, c := range []struct {
		st   int
		cr   string
		from int64
		ok   bool
	}{{200, "", 0, true}, {206, "bytes 1000-4999/5000", 1000, true}, {206, "", 0, false}, {304, "", 0, false}, {416, "bytes */5000", 0, false}} {
		from, ok := servedFrom(c.st, c.cr)
		if from != c.from || ok != c.ok {
			t.Errorf("%d %q: %d %v", c.st, c.cr, from, ok)
		}
	}
}

func setMode(t *testing.T, r *rig, sid string, planned bool) {
	t.Helper()
	code, b := r.api(t, "POST", "/api/umzug/modus", sid, `{"geplant":`+strconv.FormatBool(planned)+`}`)
	if code != http.StatusOK {
		t.Fatalf("Modus: %d %s", code, b)
	}
	var st Status
	if err := json.Unmarshal([]byte(b), &st); err != nil || st.Alltag == planned {
		t.Fatalf("Alltag nach geplant=%v: %s", planned, b)
	}
}

// alltagRig ist ein Backup-Rig im Alltag, bestätigt, mit Frist grace.
func alltagRig(t *testing.T, grace time.Duration) (*rig, *fakeBackups, string) {
	t.Helper()
	r, _, fb, sid := backupRig(t)
	r.d.ModeFile = filepath.Join(t.TempDir(), "umzug", "modus.json")
	r.d.RemoveGrace = grace
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	setMode(t, r, sid, false)
	return r, fb, sid
}

func dlLink(t *testing.T, r *rig, sid string) string {
	t.Helper()
	code, b := r.api(t, "POST", "/api/backup/herunterladen", sid, "")
	var v struct {
		Pfad string `json:"pfad"`
	}
	if err := json.Unmarshal([]byte(b), &v); code != 200 || err != nil {
		t.Fatalf("Link: %d %s", code, b)
	}
	return v.Pfad
}

func removedCount(fb *fakeBackups) int {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return fb.removed
}

func waitRemoved(t *testing.T, fb *fakeBackups, want int) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if removedCount(fb) >= want {
			return
		}
	}
	t.Fatalf("Backup nicht gelöscht (%d)", want)
}

func waitJournal(t *testing.T, r *rig, text string) string {
	t.Helper()
	var jr string
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if jr = journal(t, r); strings.Contains(jr, text) {
			return jr
		}
	}
	t.Fatalf("%q nicht im Protokoll:\n%s", text, jr)
	return ""
}

func TestAlltagErstNachUmzug(t *testing.T) {
	r, _, _, _ := backupRig(t)
	r.d.ModeFile = filepath.Join(t.TempDir(), "umzug", "modus.json")
	arrived := false
	r.d.Arrived = func() bool { return arrived }
	if r.d.everyday() {
		t.Fatal("ohne Umzug und ohne Eintrag gilt Alltag")
	}
	arrived = true
	if !r.d.everyday() {
		t.Fatal("nach dem Einzug gilt Alltag")
	}
	r.d.Copy = func() *CopyInfo { return &CopyInfo{From: "alt"} }
	if r.d.everyday() {
		t.Fatal("eine Probe-Kopie ist mitten im Umzug")
	}
	r.d.Copy = nil
	r.d.Replaced = func() *umzugstag.Done { return &umzugstag.Done{IP: "198.51.100.20"} }
	if r.d.everyday() {
		t.Fatal("ein abgelöster Server ist mitten im Umzug")
	}
}

func TestBackupNachDownloadVomServer(t *testing.T) {
	r, fb, sid := alltagRig(t, 1500*time.Millisecond)

	// Steht ein Umzug an, bleibt das Backup nach dem Download liegen.
	setMode(t, r, sid, true)
	if resp, _ := download(t, r, dlLink(t, r, sid), sid, nil); resp.StatusCode != 200 {
		t.Fatalf("Download: %d", resp.StatusCode)
	}
	if f := backupState(t, r, sid).Fetched; f == nil || f.RemoveAt != nil {
		t.Fatalf("im Umzug: %+v", f)
	}

	// Im Alltag: erst ein Teil, dann der Rest.
	setMode(t, r, sid, false)
	p := dlLink(t, r, sid)
	resp, part := download(t, r, p, sid, map[string]string{"Range": "bytes=0-999"})
	if resp.StatusCode != http.StatusPartialContent || len(part) != 1000 {
		t.Fatalf("Teil: %d %d", resp.StatusCode, len(part))
	}
	resp, _ = download(t, r, p, sid, map[string]string{"Range": "bytes=1000-", "If-Range": resp.Header.Get("ETag")})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("Rest: %d", resp.StatusCode)
	}
	// Nicht sofort: Was geschrieben ist, muss noch nicht beim Browser sein.
	if removedCount(fb) != 0 {
		t.Fatal("sofort nach dem letzten Byte gelöscht")
	}
	f1 := backupState(t, r, sid).Fetched
	if f1 == nil || f1.RemoveAt == nil {
		t.Fatalf("Löschen nicht vorgemerkt: %+v", f1)
	}
	// Läuft der Download noch einmal an, beginnt die Frist neu.
	time.Sleep(20 * time.Millisecond)
	download(t, r, p, sid, map[string]string{"Range": "bytes=0-9"})
	f2 := backupState(t, r, sid).Fetched
	if f2 == nil || f2.RemoveAt == nil || !f2.RemoveAt.After(*f1.RemoveAt) {
		t.Fatalf("Frist nicht neu: %+v -> %+v", f1.RemoveAt, f2)
	}
	waitRemoved(t, fb, 1)
	if _, err := os.Stat(filepath.Join(fb.dir, "config")); err == nil {
		t.Fatal("Backup-Ordner nicht leer")
	}
	if f := backupState(t, r, sid).Fetched; f != nil {
		t.Errorf("nach dem Löschen noch heruntergeladen: %+v", f)
	}
	// Ein anderes Gerät setzt danach fort: klarer Text statt „packen“.
	if resp, body := download(t, r, p, sid, map[string]string{"Range": "bytes=10-"}); resp.StatusCode != http.StatusGone || !strings.Contains(string(body), "kein Backup (mehr)") {
		t.Errorf("Fortsetzen nach dem Löschen: %d %s", resp.StatusCode, body)
	}
	jr := waitJournal(t, r, "Backup nach dem Download vom Server gelöscht")
	if !strings.Contains(jr, "Backup vollständig heruntergeladen: hostdeck-backup-test.tar. HostDeck löscht es gleich vom Server") {
		t.Errorf("Protokoll:\n%s", jr)
	}
}

// Ändert sich das Backup in der Frist, bleibt es liegen.
func TestBackupNachDownloadGeaendert(t *testing.T) {
	r, fb, sid := alltagRig(t, 200*time.Millisecond)
	download(t, r, dlLink(t, r, sid), sid, nil)
	if err := os.WriteFile(filepath.Join(fb.dir, "data", "ab", "neu"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitJournal(t, r, "Seit dem Download hat sich das Backup geändert")
	if removedCount(fb) != 0 {
		t.Fatal("geändertes Backup gelöscht")
	}
	if f := backupState(t, r, sid).Fetched; f == nil || f.RemoveAt != nil {
		t.Errorf("danach: %+v", f)
	}
}

// Wird in der Frist ein Umzug geplant, bleibt es liegen.
func TestBackupNachDownloadUmzugGeplant(t *testing.T) {
	r, fb, sid := alltagRig(t, 300*time.Millisecond)
	download(t, r, dlLink(t, r, sid), sid, nil)
	setMode(t, r, sid, true)
	if f := backupState(t, r, sid).Fetched; f == nil || f.RemoveAt != nil {
		t.Fatalf("Löschen nicht abgesagt: %+v", f)
	}
	time.Sleep(600 * time.Millisecond)
	if removedCount(fb) != 0 {
		t.Fatal("trotz Umzug gelöscht")
	}
	// Zurück auf Alltag: Das schon heruntergeladene Backup bleibt nicht liegen.
	setMode(t, r, sid, false)
	if f := backupState(t, r, sid).Fetched; f == nil || f.RemoveAt == nil {
		t.Fatalf("Löschen nicht wieder vorgemerkt: %+v", f)
	}
	waitRemoved(t, fb, 1)
	waitJournal(t, r, "Das Backup ist schon vollständig heruntergeladen")
}

// Lädt jemand noch, wenn die Frist abläuft, wartet HostDeck weiter.
func TestBackupNachDownloadNochJemand(t *testing.T) {
	r, fb, sid := alltagRig(t, 100*time.Millisecond)
	download(t, r, dlLink(t, r, sid), sid, nil)
	r.d.mu.Lock()
	r.d.loading++
	r.d.mu.Unlock()
	time.Sleep(400 * time.Millisecond)
	if removedCount(fb) != 0 {
		t.Fatal("während eines Downloads gelöscht")
	}
	r.d.mu.Lock()
	r.d.loading--
	r.d.mu.Unlock()
	waitRemoved(t, fb, 1)
}

func TestBackupVomServerLoeschen(t *testing.T) {
	r, _, fb, sid := backupRig(t)
	if code, b := r.api(t, "POST", "/api/backup/loeschen", sid, ""); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b := r.api(t, "POST", "/api/backup/loeschen", sid, ""); code != http.StatusOK {
		t.Fatalf("Löschen: %d %s", code, b)
	}
	if fb.removed != 1 {
		t.Fatalf("removed=%d", fb.removed)
	}
	if !strings.Contains(journal(t, r), "Backup vom Server gelöscht") {
		t.Error("nicht im Protokoll")
	}
}
