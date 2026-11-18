package dienst

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/aktualisieren"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

type fakeUpdates struct {
	mu      sync.Mutex
	done    []string
	release chan struct{}
}

func (f *fakeUpdates) LastReport() aktualisieren.Report {
	return aktualisieren.Report{Projects: []aktualisieren.ProjectCheck{{Project: "shop", Services: []aktualisieren.ServiceCheck{{Service: "app", State: aktualisieren.Newer}}}}}
}

func (f *fakeUpdates) LastFallback(p string) *aktualisieren.Fallback {
	if p == "shop" {
		return &aktualisieren.Fallback{Project: "shop"}
	}
	return nil
}

func (f *fakeUpdates) Check(_ context.Context, p string) (aktualisieren.Report, error) {
	if p == "gibtsnicht" {
		return aktualisieren.Report{}, fmt.Errorf("%w: gibtsnicht", aktualisieren.ErrNotFound)
	}
	return f.LastReport(), nil
}

func (f *fakeUpdates) Plan(_ context.Context, p string, want []string) (aktualisieren.Plan, error) {
	if p != "shop" {
		return aktualisieren.Plan{}, aktualisieren.ErrNotFound
	}
	return aktualisieren.Plan{Project: p, Services: []string{"app"}}, nil
}

func (f *fakeUpdates) Do(ctx context.Context, p string, want []string, out io.Writer) (aktualisieren.Result, error) {
	fmt.Fprintln(out, "Images holen: app")
	if f.release != nil {
		<-f.release
	}
	f.mu.Lock()
	f.done = append(f.done, "do "+p+" "+strings.Join(want, ","))
	f.mu.Unlock()
	return aktualisieren.Result{Project: p, OK: true, Changed: []aktualisieren.Change{{Service: "app"}}}, nil
}

func (f *fakeUpdates) RevertPlan(p string) (aktualisieren.Plan, error) {
	return aktualisieren.Plan{Project: p, Services: []string{"app"}}, nil
}

func (f *fakeUpdates) Revert(_ context.Context, p string, out io.Writer) (aktualisieren.Result, error) {
	f.mu.Lock()
	f.done = append(f.done, "zurueck "+p)
	f.mu.Unlock()
	return aktualisieren.Result{Project: p, OK: true, RolledBack: true}, nil
}

func TestAktualisierenImLeitstand(t *testing.T) {
	fu := &fakeUpdates{release: make(chan struct{})}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Updates = fu })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/aktualisieren", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	code, b := r.api(t, "GET", "/api/aktualisieren", sid, "")
	if code != 200 || !strings.Contains(b, `"stand":"neu"`) || !strings.Contains(b, `"rueckfall":{"shop"`) {
		t.Fatalf("Stand: %d %s", code, b)
	}
	if code, _ = r.api(t, "POST", "/api/aktualisieren/nachsehen", sid, `{"projekt":"gibtsnicht"}`); code != http.StatusNotFound {
		t.Errorf("unbekannt: %d", code)
	}
	if code, _ = r.api(t, "POST", "/api/aktualisieren/nachsehen", sid, `{"projekt":"../etc"}`); code != http.StatusBadRequest {
		t.Errorf("Unsinn: %d", code)
	}
	if code, b = r.api(t, "POST", "/api/aktualisieren/vorschau", sid, `{"projekt":"shop"}`); code != 200 || !strings.Contains(b, `"dienste":["app"]`) {
		t.Errorf("Vorschau: %d %s", code, b)
	}
	start := `{"projekt":"shop","dienste":["app"]}`
	if code, b = r.api(t, "POST", "/api/aktualisieren/starten", sid, start); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, _ = r.api(t, "POST", "/api/aktualisieren/starten", sid, `{"projekt":"shop"}`); code != http.StatusBadRequest {
		t.Errorf("ohne Vorschau: %d", code)
	}
	if code, _ = r.api(t, "POST", "/api/aktualisieren/starten", sid, `{"projekt":"shop","dienste":["a b"]}`); code != http.StatusBadRequest {
		t.Errorf("Unsinn als Dienst: %d", code)
	}
	code, b = r.api(t, "POST", "/api/aktualisieren/starten", sid, start)
	if code != http.StatusAccepted || !strings.Contains(b, `"laeuft":true`) {
		t.Fatalf("Start: %d %s", code, b)
	}
	// Läuft: Sperre ist belegt, ein zweiter Lauf wartet.
	if i, _ := lauf.CurrentIn(r.d.RunDir); i == nil || !strings.Contains(i.What, "shop aktualisieren") {
		t.Errorf("Sperre: %+v", i)
	}
	if code, _ = r.api(t, "POST", "/api/aktualisieren/zurueck", sid, `{"projekt":"shop"}`); code != http.StatusConflict {
		t.Errorf("zweiter Lauf: %d", code)
	}
	close(fu.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, b = r.api(t, "GET", "/api/aktualisieren", sid, "")
		if strings.Contains(b, `"ergebnis"`) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(b, `"ok":true`) || !strings.Contains(b, "Images holen: app") {
		t.Fatalf("Ergebnis: %s", b)
	}
	// Erst die Sperre frei, dann weiter.
	for i := 0; i < 100; i++ {
		if c, _ := lauf.CurrentIn(r.d.RunDir); c == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ = r.api(t, "POST", "/api/aktualisieren/zurueck/vorschau", sid, `{"projekt":"shop"}`); code != 200 {
		t.Errorf("Vorschau zurück: %d", code)
	}
	if code, b = r.api(t, "POST", "/api/aktualisieren/zurueck", sid, `{"projekt":"shop"}`); code != http.StatusAccepted {
		t.Fatalf("zurück: %d %s", code, b)
	}
	r.d.wg.Wait()
	jr := journal(t, r)
	for _, want := range []string{"projekt|Projekt shop aktualisieren gestartet: app", "projekt|Projekt shop aktualisiert: app", "zurückgestellt", "203.0.113.9"} {
		if !strings.Contains(jr, want) {
			t.Errorf("Protokoll ohne %q: %s", want, jr)
		}
	}
	fu.mu.Lock()
	defer fu.mu.Unlock()
	if strings.Join(fu.done, ";") != "do shop app;zurueck shop" {
		t.Errorf("Aufträge: %v", fu.done)
	}
}
