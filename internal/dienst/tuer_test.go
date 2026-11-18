package dienst

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/anmeldung"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/update"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

const testPW = "das-richtige-passwort"

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func doorRig(t *testing.T, data http.Handler) (*rig, *clock) {
	t.Helper()
	return doorRigWith(t, data, nil)
}

func doorRigWith(t *testing.T, data http.Handler, set func(d *Dienst)) (*rig, *clock) {
	t.Helper()
	dir := t.TempDir()
	c := &clock{now: time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)}
	d := &Dienst{WebUID: me(), AdminUID: me(), Now: c.Now, Data: data,
		Password:      &anmeldung.Passwort{File: filepath.Join(dir, "anmeldung.json")},
		Lockout:       &anmeldung.Sperre{File: filepath.Join(dir, "sperre.json"), Now: c.Now},
		Journal:       &protokoll.Log{File: filepath.Join(dir, "protokoll.jsonl"), Now: c.Now},
		EmergencyGate: "Notausgang",
	}
	if set != nil {
		set(d)
	}
	return start(t, d), c
}

func (r *rig) api(t *testing.T, method, path, sid, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, "http://hostdeck-dienst"+path, rd)
	if sid != "" {
		req.Header.Set(web.SessionHeader, sid)
	}
	req.Header.Set(web.AddrHeader, "203.0.113.9")
	// Die Website schickt ihren Eingang mit; Anmeldungen gelten nur dort.
	gate := "Test"
	if info, err := r.d.Sessions.Check(sid, false); err == nil {
		gate = info.Gate
	}
	req.Header.Set(web.GateHeader, gate)
	return r.do(t, req)
}

func (r *rig) do(t *testing.T, req *http.Request) (int, string) {
	t.Helper()
	resp, err := r.c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func journal(t *testing.T, r *rig) string {
	t.Helper()
	es, err := r.d.Journal.Last(100)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range es {
		b.WriteString(e.Kind + "|" + e.Text + "|" + e.Addr + "|" + strings.Join(e.Details, ";") + "\n")
	}
	return b.String()
}

func TestAnmeldenMitPasswortUndSperre(t *testing.T) {
	r, c := doorRig(t, nil)
	ctx := context.Background()
	door := r.c.Door()
	v := web.Visitor{Gate: "Caddy", Addr: "203.0.113.9"}
	if _, err := door.Login(ctx, testPW, v); !errors.Is(err, web.ErrNoPassword) {
		t.Fatalf("ohne Passwort: %v", err)
	}
	if err := r.c.SetPassword(ctx, "kurz"); err == nil {
		t.Fatal("kurzes Passwort angenommen")
	}
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	id, err := door.Login(ctx, testPW, v)
	if err != nil || id == "" {
		t.Fatalf("Anmeldung: %v", err)
	}
	if err := door.Check(ctx, id, true); err != nil {
		t.Fatalf("Prüfung: %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := door.Login(ctx, "falsch", v); !errors.Is(err, web.ErrBadPassword) {
			t.Fatalf("Fehlversuch %d: %v", i+1, err)
		}
	}
	_, err = door.Login(ctx, "falsch", v)
	var locked *web.LockedError
	if _, err = door.Login(ctx, testPW, v); !errors.As(err, &locked) || locked.Wait != 15*time.Minute {
		t.Fatalf("nach fünf Fehlversuchen: %v", err)
	}
	// Der Notausgang ist eine andere Adresse und bleibt offen; der Link sowieso.
	if _, err := door.Login(ctx, testPW, web.Visitor{Gate: "Notausgang", Addr: "Notausgang"}); err != nil {
		t.Fatalf("Notausgang gesperrt: %v", err)
	}
	c.add(15 * time.Minute)
	if _, err := door.Login(ctx, testPW, v); err != nil {
		t.Fatalf("nach 15 Minuten: %v", err)
	}
	j := journal(t, r)
	for _, want := range []string{"anmeldung|Angemeldet mit Passwort|203.0.113.9", "fehlversuch|Falsches Passwort (Anmeldung)|203.0.113.9", "gesperrt|Anmelden für 15 Minuten gesperrt", "passwort|Leitstand-Passwort festgelegt"} {
		if !strings.Contains(j, want) {
			t.Errorf("Protokoll ohne %q:\n%s", want, j)
		}
	}
	if strings.Contains(j, testPW) || strings.Contains(j, id) {
		t.Fatal("Passwort oder Anmeldung im Protokoll")
	}
}

func TestNeuesPasswortMeldetAlleAb(t *testing.T) {
	r, _ := doorRig(t, nil)
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	door := r.c.Door()
	id, _ := door.Login(ctx, testPW, web.Visitor{Addr: "a"})
	link := r.session(t)
	if err := r.c.SetPassword(ctx, "ein-neues-passwort"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{id, link} {
		if err := door.Check(ctx, s, false); !errors.Is(err, web.ErrSession) {
			t.Fatalf("nach neuem Passwort noch angemeldet: %v", err)
		}
	}
	if _, err := door.Login(ctx, testPW, web.Visitor{Addr: "a"}); !errors.Is(err, web.ErrBadPassword) {
		t.Fatalf("altes Passwort: %v", err)
	}
}

func TestPasswortNurDurchRoot(t *testing.T) {
	r := start(t, &Dienst{WebUID: me(), AdminUID: me() + 4711, Password: &anmeldung.Passwort{File: filepath.Join(t.TempDir(), "a.json")}})
	if err := r.c.SetPassword(context.Background(), testPW); code(err) != http.StatusForbidden {
		t.Fatalf("Website setzt Passwort: %v", err)
	}
}

func TestSpeichernNurBestaetigt(t *testing.T) {
	data := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"edits"`) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})
	r, c := doorRig(t, data)
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, _ := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	save := `{"version":"x","edits":[{"key":"project:blog","decision":"skip","reason":"alt"}]}`
	if code, b := r.api(t, "POST", "/api/save", sid, save); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	if code, _ := r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"falsch"}`); code != http.StatusUnauthorized {
		t.Fatalf("falsche Bestätigung: %d", code)
	}
	if code, b := r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`); code != 200 {
		t.Fatalf("Bestätigung: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/save", sid, save); code != 200 {
		t.Fatalf("bestätigt speichern: %d %s", code, b)
	}
	c.add(6 * time.Minute)
	if code, _ := r.api(t, "POST", "/api/save", sid, save); code != http.StatusForbidden {
		t.Fatalf("Bestätigung gilt nach 6 Minuten noch: %d", code)
	}
	// Mit Link Angemeldete haben root bewiesen und brauchen keine Bestätigung.
	if code, _ := r.api(t, "POST", "/api/save", r.session(t), save); code != 200 {
		t.Fatalf("Link-Anmeldung: %d", code)
	}
	j := journal(t, r)
	if !strings.Contains(j, "auswahl|Auswahl gespeichert (1 Änderung)|203.0.113.9|project:blog: Entscheidung: bleibt zurück, Grund: „alt“") {
		t.Errorf("Protokoll:\n%s", j)
	}
	if !strings.Contains(j, "fehlversuch|Falsches Passwort (Bestätigung)") || !strings.Contains(j, "bestaetigung|Mit Passwort bestätigt") {
		t.Errorf("Protokoll ohne Bestätigungen:\n%s", j)
	}
	if code, b := r.api(t, "GET", "/api/protokoll", sid, ""); code != 200 || !strings.Contains(b, "Auswahl gespeichert") {
		t.Fatalf("Protokoll abrufen: %d %s", code, b)
	}
	if code, b := r.api(t, "GET", "/api/sitzung", sid, ""); code != 200 || !strings.Contains(b, `"weg":"Passwort"`) || !strings.Contains(b, `"passwort_gesetzt":true`) {
		t.Fatalf("Sitzung: %d %s", code, b)
	}
}

func TestAbmeldenUndUeberall(t *testing.T) {
	r, _ := doorRig(t, nil)
	ctx := context.Background()
	door := r.c.Door()
	a, b := r.session(t), r.session(t)
	if err := door.Logout(ctx, a, false, web.Visitor{Addr: "x"}); err != nil {
		t.Fatal(err)
	}
	if door.Check(ctx, a, false) == nil || door.Check(ctx, b, false) != nil {
		t.Fatal("einzelnes Abmelden")
	}
	c := r.session(t)
	if err := door.Logout(ctx, c, true, web.Visitor{Addr: "x"}); err != nil {
		t.Fatal(err)
	}
	if door.Check(ctx, b, false) == nil {
		t.Fatal("überall abmelden ließ eine Anmeldung übrig")
	}
	if j := journal(t, r); !strings.Contains(j, "Überall abgemeldet (2 Anmeldungen)") {
		t.Errorf("Protokoll:\n%s", j)
	}
}

func TestUpdateNurBestaetigtUndProtokolliert(t *testing.T) {
	var got atomic.Int64
	r, _ := doorRigWith(t, nil, func(d *Dienst) {
		d.Version = "1b-alt"
		d.Update = func(_ context.Context, body io.Reader) (update.Header, error) {
			n, err := io.Copy(io.Discard, body)
			got.Store(n)
			if err != nil {
				return update.Header{}, err
			}
			if n < 10 {
				return update.Header{}, update.ErrNotUpdate
			}
			return update.Header{Version: "1b-neu"}, nil
		}
	})
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, _ := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	big := strings.Repeat("x", 3<<20) // größer als die Grenze für andere Aufträge
	if code, b := r.api(t, "POST", "/api/update", sid, big); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	if got.Load() != 0 {
		t.Fatal("Update ohne Bestätigung angenommen")
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b := r.api(t, "POST", "/api/update", sid, "kurz"); code != http.StatusUnprocessableEntity || !strings.Contains(b, "keine Update-Datei") {
		t.Fatalf("falsche Datei: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/update", sid, big); code != http.StatusAccepted || !strings.Contains(b, `"version":"1b-neu"`) {
		t.Fatalf("Update: %d %s", code, b)
	}
	if got.Load() != int64(len(big)) {
		t.Fatalf("%d Bytes angekommen, erwartet %d", got.Load(), len(big))
	}
	if code, _ := r.api(t, "POST", "/api/bestaetigen", sid, big); code == http.StatusOK {
		t.Fatal("große Anfrage an anderen Auftrag angenommen")
	}
	j := journal(t, r)
	if !strings.Contains(j, "update|Update abgelehnt: ") || !strings.Contains(j, "update|Update auf Version 1b-neu hochgeladen (läuft: 1b-alt), Signatur geprüft, wird eingespielt|203.0.113.9") {
		t.Errorf("Protokoll:\n%s", j)
	}
}

func TestAbgemeldetText(t *testing.T) {
	for n, want := range map[int]string{0: "Überall abgemeldet (0 Anmeldungen)", 1: "Überall abgemeldet (1 Anmeldung)", 2: "Überall abgemeldet (2 Anmeldungen)"} {
		if got := abgemeldetText(n); got != want {
			t.Errorf("abgemeldetText(%d) = %q, will %q", n, got, want)
		}
	}
}
