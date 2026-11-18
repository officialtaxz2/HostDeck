package dienst

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/aufraeumen"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

type fakeCleanup struct {
	mu      sync.Mutex
	done    []string
	release chan struct{}
	expired int
}

func (f *fakeCleanup) Overview(context.Context) aufraeumen.Overview {
	return aufraeumen.Overview{
		Items: []aufraeumen.Item{{Key: "cache", Kind: aufraeumen.KindCache, Name: "Build-Cache leeren", Size: 14 << 30, Pick: true}},
		Trash: []aufraeumen.Entry{{ID: "snap1", Kind: aufraeumen.KindVolume, Name: "alt_db"}},
		Days:  30,
	}
}

func (f *fakeCleanup) Plan(_ context.Context, keys []string) (aufraeumen.Plan, error) {
	if len(keys) == 0 {
		return aufraeumen.Plan{}, aufraeumen.ErrNothing
	}
	if keys[0] == "volume:weg" {
		return aufraeumen.Plan{}, aufraeumen.ErrNotFound
	}
	return aufraeumen.Plan{Stand: "s1"}, nil
}

func (f *fakeCleanup) Run(_ context.Context, keys []string, stand string, ev func(aufraeumen.Event)) (string, error) {
	ev(aufraeumen.Event{Step: aufraeumen.StepCache, State: aufraeumen.StateRunning})
	if f.release != nil {
		<-f.release
	}
	ev(aufraeumen.Event{Step: aufraeumen.StepCache, State: aufraeumen.StateOK, Detail: "14 GB frei"})
	if stand != "s1" {
		return "", aufraeumen.ErrChanged
	}
	f.mu.Lock()
	f.done = append(f.done, "run "+strings.Join(keys, ","))
	f.mu.Unlock()
	return "Aufgeräumt: 14 GB frei geworden (vorher 100 GB frei, jetzt 114 GB). 1 Image bleibt, weil Docker es noch braucht.", nil
}

func (f *fakeCleanup) Restore(_ context.Context, id string, ev func(aufraeumen.Event)) (string, error) {
	f.mu.Lock()
	f.done = append(f.done, "restore "+id)
	f.mu.Unlock()
	return "alt_db ist zurück.", nil
}

func (f *fakeCleanup) Expire(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expired++
	return "Papierkorb: 1 Eintrag nach 30 Tagen endgültig gelöscht (alt_db).", nil
}

func TestAufraeumenImLeitstand(t *testing.T) {
	fc := &fakeCleanup{release: make(chan struct{})}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Cleanup = fc })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/aufraeumen", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	code, b := r.api(t, "GET", "/api/aufraeumen", sid, "")
	if code != 200 || !strings.Contains(b, `"vorschlaege":[{`) || !strings.Contains(b, `"papierkorb":[{`) {
		t.Fatalf("Stand: %d %s", code, b)
	}
	if code, _ = r.api(t, "POST", "/api/aufraeumen/vorschau", sid, `{"auswahl":[]}`); code != http.StatusBadRequest {
		t.Errorf("leere Auswahl: %d", code)
	}
	if code, _ = r.api(t, "POST", "/api/aufraeumen/vorschau", sid, `{"auswahl":["volume:weg"]}`); code != http.StatusNotFound {
		t.Errorf("unbekannt: %d", code)
	}
	if code, b = r.api(t, "POST", "/api/aufraeumen/vorschau", sid, `{"auswahl":["cache"]}`); code != 200 || !strings.Contains(b, `"stand":"s1"`) {
		t.Fatalf("Vorschau: %d %s", code, b)
	}
	if code, b = r.api(t, "POST", "/api/aufraeumen/starten", sid, `{"auswahl":["cache"],"stand":"s1"}`); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, _ = r.api(t, "POST", "/api/aufraeumen/starten", sid, `{"auswahl":["cache"]}`); code != http.StatusBadRequest {
		t.Errorf("ohne Vorschau: %d", code)
	}
	code, b = r.api(t, "POST", "/api/aufraeumen/starten", sid, `{"auswahl":["cache"],"stand":"s1"}`)
	if code != http.StatusAccepted || !strings.Contains(b, `"laeuft":true`) {
		t.Fatalf("Start: %d %s", code, b)
	}
	if i, _ := lauf.CurrentIn(r.d.RunDir); i == nil || i.What != "Aufräumen" {
		t.Errorf("Sperre: %+v", i)
	}
	if code, _ = r.api(t, "POST", "/api/aufraeumen/zurueckholen", sid, `{"id":"snap1"}`); code != http.StatusConflict {
		t.Errorf("zweiter Lauf: %d", code)
	}
	close(fc.release)
	wait := func(want string) string {
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, b := r.api(t, "GET", "/api/aufraeumen", sid, "")
			if (strings.Contains(b, `"laeuft":false`) && strings.Contains(b, want)) || time.Now().After(deadline) {
				return b
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	b = wait("Aufgeräumt")
	if !strings.Contains(b, `"ergebnis":"Aufgeräumt: 14 GB`) || !strings.Contains(b, `"14 GB frei"`) {
		t.Fatalf("Ergebnis: %s", b)
	}
	for i := 0; i < 100; i++ {
		if c, _ := lauf.CurrentIn(r.d.RunDir); c == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ = r.api(t, "POST", "/api/aufraeumen/zurueckholen", sid, `{"id":"snap1"}`); code != http.StatusAccepted {
		t.Fatalf("zurückholen: %d", code)
	}
	b = wait("ist zurück")
	if !strings.Contains(b, "Aus dem Papierkorb zurückholen: alt_db") {
		t.Errorf("Auftrag ohne Namen: %s", b)
	}
	r.d.wg.Wait()
	r.d.expireTrash(ctx)
	if fc.expired != 1 {
		t.Error("Papierkorb nicht gepflegt")
	}
	jr := journal(t, r)
	for _, want := range []string{"aufraeumen|Aufräumen gestartet", "aufraeumen|Aufgeräumt: 14 GB frei geworden (vorher 100 GB frei, jetzt 114 GB).", "1 Image bleibt", "Aus dem Papierkorb zurückholen: alt_db gestartet", "alt_db ist zurück.", "Papierkorb: 1 Eintrag nach 30 Tagen", "203.0.113.9"} {
		if !strings.Contains(jr, want) {
			t.Errorf("Protokoll ohne %q: %s", want, jr)
		}
	}
}
