package dienst

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/hinweise"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/leistung"
	"github.com/officialtaxz2/hostdeck/internal/sicherheit"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

type fakeSecurity struct {
	pending  *sicherheit.Pending
	kept     int
	keptOpen bool
}

func (f *fakeSecurity) Overview(context.Context) sicherheit.Overview {
	return sicherheit.Overview{
		Ports:   []sicherheit.Port{{Key: "tcp/3552", Proto: "tcp", From: 3552, To: 3552, Owner: "arcane", Suggest: true}},
		Pending: f.pending,
	}
}

func (f *fakeSecurity) Plan(_ context.Context, ch sicherheit.Change) (sicherheit.Plan, error) {
	if len(ch.Close) == 0 && len(ch.Open) == 0 {
		return sicherheit.Plan{}, sicherheit.ErrNothing
	}
	if len(ch.Close) > 0 && ch.Close[0] == "tcp/22" {
		return sicherheit.Plan{}, sicherheit.ErrLocked
	}
	return sicherheit.Plan{Stand: "s1"}, nil
}

func (f *fakeSecurity) Apply(_ context.Context, ch sicherheit.Change, stand string) (string, error) {
	if stand != "s1" {
		return "", sicherheit.ErrChanged
	}
	f.pending = &sicherheit.Pending{Until: time.Now().Add(90 * time.Second), What: "Schließen: 3552/tcp (arcane)"}
	return "Geschlossen: 3552/tcp (arcane). Warte auf die Bestätigung des Leitstands.", nil
}

func (f *fakeSecurity) Keep(context.Context) (string, error) {
	f.kept++
	f.pending = nil
	return "Schließen: 3552/tcp (arcane) ist aktiv und bleibt es auch nach einem Neustart.", nil
}

func (f *fakeSecurity) Reload(context.Context) (string, error) {
	return "", sicherheit.ErrNothing
}

func (f *fakeSecurity) KeepOpen(_ context.Context, key string, on bool) (string, error) {
	if key != "tcp/3552" {
		return "", sicherheit.ErrNotFound
	}
	f.keptOpen = on
	return "Bleibt bewusst offen: 3552/tcp (arcane).", nil
}

type fakePerf struct {
	applied []string
	hold    chan struct{}
}

func (f *fakePerf) Overview(context.Context) leistung.Overview {
	return leistung.Overview{Suggestions: []leistung.Suggestion{{Key: leistung.KeySwap, Title: "Kleine Swap-Datei als Puffer"}}}
}

func (f *fakePerf) Plan(_ context.Context, key string) (leistung.Plan, error) {
	if key != leistung.KeySwap && key != leistung.KeyLogs {
		return leistung.Plan{}, leistung.ErrNotFound
	}
	return leistung.Plan{Key: key, Stand: "p1"}, nil
}

func (f *fakePerf) Apply(_ context.Context, key, stand string) (string, error) {
	if f.hold != nil {
		<-f.hold
	}
	if stand != "p1" {
		return "", leistung.ErrChanged
	}
	f.applied = append(f.applied, key)
	return "Swap-Puffer angelegt: /swapfile-hostdeck mit 2 GB, eingeschaltet und in /etc/fstab eingetragen.", nil
}

func TestSicherheitUndLeistungImLeitstand(t *testing.T) {
	fs, fp := &fakeSecurity{}, &fakePerf{}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Security, d.Performance = fs, fp })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/sicherheit", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	if code, b := r.api(t, "GET", "/api/sicherheit", sid, ""); code != 200 || !strings.Contains(b, `"schluessel":"tcp/3552"`) {
		t.Fatalf("Stand: %d %s", code, b)
	}
	if code, _ := r.api(t, "POST", "/api/sicherheit/vorschau", sid, `{"schliessen":["tcp/22"]}`); code != http.StatusUnprocessableEntity {
		t.Errorf("SSH: %d", code)
	}
	if code, _ := r.api(t, "POST", "/api/sicherheit/vorschau", sid, `{}`); code != http.StatusBadRequest {
		t.Errorf("leer: %d", code)
	}
	if code, b := r.api(t, "POST", "/api/sicherheit/vorschau", sid, `{"schliessen":["tcp/3552"]}`); code != 200 || !strings.Contains(b, `"stand":"s1"`) {
		t.Fatalf("Vorschau: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/sicherheit/aendern", sid, `{"schliessen":["tcp/3552"],"stand":"s1"}`); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, _ := r.api(t, "POST", "/api/sicherheit/aendern", sid, `{"schliessen":["tcp/3552"],"stand":"alt"}`); code != http.StatusConflict {
		t.Errorf("alter Stand: %d", code)
	}
	code, b := r.api(t, "POST", "/api/sicherheit/aendern", sid, `{"schliessen":["tcp/3552"],"stand":"s1"}`)
	if code != 200 || !strings.Contains(b, `"bestaetigen_bis"`) {
		t.Fatalf("Ändern: %d %s", code, b)
	}
	if c, _ := lauf.CurrentIn(r.d.RunDir); c != nil {
		t.Errorf("Sperre bleibt: %+v", c)
	}
	if code, b = r.api(t, "POST", "/api/sicherheit/behalten", sid, ``); code != 200 || fs.kept != 1 {
		t.Fatalf("Behalten: %d %s", code, b)
	}
	r.d.SecurityExpired("Schließen: 8095/tcp (mc-packs): keine Bestätigung vom Leitstand, darum zurückgenommen.")

	// Leistung
	if code, b = r.api(t, "GET", "/api/leistung", sid, ""); code != 200 || !strings.Contains(b, `"schluessel":"swap"`) {
		t.Fatalf("Leistung: %d %s", code, b)
	}
	if code, _ = r.api(t, "POST", "/api/leistung/vorschau", sid, `{"schluessel":"alles"}`); code != http.StatusNotFound {
		t.Errorf("unbekannt: %d", code)
	}
	if code, _ = r.api(t, "POST", "/api/leistung/aendern", sid, `{"schluessel":"swap"}`); code != http.StatusBadRequest {
		t.Errorf("ohne Vorschau: %d", code)
	}
	// Läuft gerade etwas anderes, wartet die Änderung nicht, sondern sagt es.
	l, err := lauf.TakeIn(r.d.RunDir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ = r.api(t, "POST", "/api/leistung/aendern", sid, `{"schluessel":"swap","stand":"p1"}`); code != http.StatusConflict {
		t.Errorf("gesperrt: %d", code)
	}
	l.Release()
	if code, b = r.api(t, "POST", "/api/leistung/aendern", sid, `{"schluessel":"swap","stand":"p1"}`); code != 200 || !strings.Contains(b, "Swap-Puffer angelegt") {
		t.Fatalf("Swap: %d %s", code, b)
	}
	jr := journal(t, r)
	for _, want := range []string{"sicherheit|Geschlossen: 3552/tcp (arcane).", "ist aktiv und bleibt", "keine Bestätigung vom Leitstand", "leistung|Swap-Puffer angelegt", "203.0.113.9"} {
		if !strings.Contains(jr, want) {
			t.Errorf("Protokoll ohne %q: %s", want, jr)
		}
	}
}

type fakeHints struct{}

func (fakeHints) Hints(context.Context) hinweise.Reply {
	return hinweise.Reply{Hints: []hinweise.Hint{{Key: "platz:/", Level: hinweise.Err, Title: "Platte fast voll", Link: "#/platz"}}}
}

func TestHinweiseImLeitstand(t *testing.T) {
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Hints = fakeHints{} })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/hinweise", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	if code, b := r.api(t, "GET", "/api/hinweise", sid, ""); code != 200 || !strings.Contains(b, `"stufe":"err"`) || !strings.Contains(b, `"link":"#/platz"`) {
		t.Fatalf("Hinweise: %d %s", code, b)
	}
}
