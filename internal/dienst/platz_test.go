package dienst

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/platz"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

func TestSpeicherplatzImLeitstand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Löschen nur unter Linux")
	}
	root := t.TempDir()
	for p, n := range map[string]int{"opt/app/backups/a.tar.gz": 2 << 20, "opt/app/backups/b.tar.gz": 1 << 20, "etc/hosts": 10} {
		f := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(f), 0o755)
		if err := os.WriteFile(f, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sp := &platz.Service{Root: root, File: filepath.Join(t.TempDir(), "baum.json.gz"), Mounts: func() []string { return nil }, Open: func() []platz.OpenFile { return nil }}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Space = sp })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/platz/ordner", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	code, b := r.api(t, "GET", "/api/platz/ordner", sid, "")
	if code != 200 || !strings.Contains(b, `"nie_gemessen":true`) {
		t.Fatalf("vor der Messung: %d %s", code, b)
	}
	if code, b = r.api(t, "POST", "/api/platz/messen", sid, ""); code != http.StatusAccepted {
		t.Fatalf("messen: %d %s", code, b)
	}
	deadline := time.Now().Add(5 * time.Second)
	for sp.Measuring().Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	code, b = r.api(t, "GET", "/api/platz/ordner?pfad="+url.QueryEscape("/opt/app/backups"), sid, "")
	if code != 200 || !strings.Contains(b, `"a.tar.gz"`) || !strings.Contains(b, `"stand":"`) {
		t.Fatalf("Liste: %d %s", code, b)
	}
	if code, _ = r.api(t, "GET", "/api/platz/ordner?pfad=/gibtsnicht", sid, ""); code != http.StatusNotFound {
		t.Errorf("fehlt: %d", code)
	}
	if code, _ = r.api(t, "POST", "/api/platz/vorschau", sid, `{"auswahl":[]}`); code != http.StatusBadRequest {
		t.Errorf("leer: %d", code)
	}
	code, b = r.api(t, "POST", "/api/platz/vorschau", sid, `{"auswahl":["/opt/app/backups/a.tar.gz","/etc/hosts"]}`)
	var p platz.Plan
	if code != 200 || json.Unmarshal([]byte(b), &p) != nil || p.Stand == "" || len(p.Blocked) != 1 || len(p.Items) != 1 {
		t.Fatalf("Vorschau: %d %s", code, b)
	}
	body := `{"stand":"` + p.Stand + `","papierkorb":false}`
	if code, b = r.api(t, "POST", "/api/platz/loeschen", sid, body); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	// Hält ein anderer Lauf die Sperre, löscht HostDeck nichts.
	l, err := lauf.TakeIn(r.d.RunDir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ = r.api(t, "POST", "/api/platz/loeschen", sid, body); code != http.StatusConflict {
		t.Errorf("trotz Sperre: %d", code)
	}
	l.Release()
	if code, b = r.api(t, "POST", "/api/platz/loeschen", sid, body); code != http.StatusAccepted {
		t.Fatalf("löschen: %d %s", code, b)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		_, b = r.api(t, "GET", "/api/platz/ordner?pfad=/opt/app/backups", sid, "")
		if strings.Contains(b, `"laeuft":false`) && strings.Contains(b, `"ende"`) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(b, `"ergebnis":"Gelöscht: 1 Eintrag`) || strings.Contains(b, `"a.tar.gz"`) {
		t.Fatalf("Ergebnis: %s", b)
	}
	if _, err := os.Stat(filepath.Join(root, "opt/app/backups/a.tar.gz")); !os.IsNotExist(err) {
		t.Error("nicht gelöscht")
	}
	es, _ := r.d.Journal.Last(20)
	var started, done bool
	for _, e := range es {
		if e.Kind != protokoll.Speicher {
			continue
		}
		if strings.Contains(e.Text, "gestartet") && strings.Contains(strings.Join(e.Details, " "), "/opt/app/backups/a.tar.gz") && strings.Contains(strings.Join(e.Details, " "), "Endgültig") {
			started = true
		}
		if strings.HasPrefix(e.Text, "Gelöscht") {
			done = true
		}
	}
	if !started || !done {
		t.Errorf("Protokoll: %+v", es)
	}
	// Ein Stand gilt nur einmal.
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	for i := 0; i < 100; i++ {
		if c, _ := lauf.CurrentIn(r.d.RunDir); c == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ = r.api(t, "POST", "/api/platz/loeschen", sid, body); code != http.StatusAccepted {
		t.Fatalf("zweiter Versuch: %d", code)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		_, b = r.api(t, "GET", "/api/platz/ordner?pfad=/opt", sid, "")
		if strings.Contains(b, `"laeuft":false`) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(b, "abgelaufen oder schon benutzt") {
		t.Errorf("zweimal derselbe Stand: %s", b)
	}
}
