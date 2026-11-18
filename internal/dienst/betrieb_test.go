package dienst

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

type fakeContainers struct {
	mu   sync.Mutex
	done []string
	ctx  context.Context
}

func (f *fakeContainers) List(context.Context) (betrieb.Snapshot, error) {
	return betrieb.Snapshot{Time: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC), Containers: []betrieb.Container{{Name: "pensum-app", Project: "pensum", State: "running", Running: true}}}, nil
}

func (f *fakeContainers) Plan(_ context.Context, target, action string) (betrieb.Plan, error) {
	if _, _, err := betrieb.ParseTarget(target); err != nil {
		return betrieb.Plan{}, err
	}
	if target == "projekt:gibtsnicht" {
		return betrieb.Plan{}, betrieb.ErrNotFound
	}
	return betrieb.Plan{Action: action, Target: target, Label: "Projekt pensum", Containers: []string{"pensum-app"}}, nil
}

func (f *fakeContainers) Do(ctx context.Context, target, action string, want []string) (betrieb.Result, error) {
	p, err := f.Plan(ctx, target, action)
	if err != nil {
		return betrieb.Result{}, err
	}
	f.mu.Lock()
	f.done = append(f.done, action+" "+target)
	f.ctx = ctx
	f.mu.Unlock()
	return betrieb.Result{Plan: p, OK: true, Steps: []betrieb.Step{{Name: "pensum-app", OK: true}}}, nil
}

func (f *fakeContainers) Logs(_ context.Context, name string, lines int) (betrieb.Logs, error) {
	return betrieb.Logs{Name: name, Lines: []string{"hallo", "zeilen " + string(rune('0'+lines/100))}}, nil
}

type fakeDetails struct{ fakeContainers }

func (f *fakeDetails) Details(_ context.Context, name string) (betrieb.Details, error) {
	if name != "pensum-app" {
		return betrieb.Details{}, betrieb.ErrNotFound
	}
	return betrieb.Details{Name: name, Config: betrieb.Config{Image: "pensum:latest", EnvNames: []string{"TZ"}}}, nil
}

func TestContainerEinzelheiten(t *testing.T) {
	for _, withDetails := range []bool{true, false} {
		var c Containers = &fakeContainers{}
		if withDetails {
			c = &fakeDetails{}
		}
		r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Containers = c })
		ctx := context.Background()
		if err := r.c.SetPassword(ctx, testPW); err != nil {
			t.Fatal(err)
		}
		sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
		if err != nil {
			t.Fatal(err)
		}
		if code, _ := r.api(t, "GET", "/api/container/details?name=pensum-app", "", ""); code != http.StatusUnauthorized {
			t.Errorf("ohne Anmeldung: %d", code)
		}
		code, b := r.api(t, "GET", "/api/container/details?name=pensum-app", sid, "")
		if !withDetails {
			if code != http.StatusNotFound {
				t.Errorf("ohne Einzelheiten: %d %s", code, b)
			}
			continue
		}
		if code != 200 || !strings.Contains(b, `"image":"pensum:latest"`) || !strings.Contains(b, `"umgebung":["TZ"]`) {
			t.Errorf("Einzelheiten: %d %s", code, b)
		}
		if code, _ = r.api(t, "GET", "/api/container/details?name=gibtsnicht", sid, ""); code != http.StatusNotFound {
			t.Errorf("unbekannt: %d", code)
		}
	}
}

func TestBetriebImLeitstand(t *testing.T) {
	fc := &fakeContainers{}
	lim := &recheckLimits{}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Containers = fc; d.Limits = lim })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/container", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	code, b := r.api(t, "GET", "/api/container", sid, "")
	if code != 200 || !strings.Contains(b, `"pensum-app"`) || !strings.Contains(b, `"zeit"`) {
		t.Fatalf("Liste: %d %s", code, b)
	}
	code, b = r.api(t, "GET", "/api/container/logs?name=pensum-app&zeilen=200", sid, "")
	if code != 200 || !strings.Contains(b, `"zeilen 2"`) {
		t.Fatalf("Logs: %d %s", code, b)
	}
	// Vorschau braucht keine Bestätigung, ändert nichts.
	code, b = r.api(t, "POST", "/api/container/vorschau", sid, `{"ziel":"projekt:pensum","aktion":"neustarten"}`)
	if code != 200 || !strings.Contains(b, `"container":["pensum-app"]`) {
		t.Fatalf("Vorschau: %d %s", code, b)
	}
	if code, _ = r.api(t, "POST", "/api/container/vorschau", sid, `{"ziel":"projekt:gibtsnicht","aktion":"stoppen"}`); code != http.StatusNotFound {
		t.Errorf("unbekanntes Projekt: %d", code)
	}
	if code, _ = r.api(t, "POST", "/api/container/vorschau", sid, `{"ziel":"rm -rf /","aktion":"stoppen"}`); code != http.StatusBadRequest {
		t.Errorf("Unsinn als Ziel: %d", code)
	}

	act := `{"ziel":"projekt:pensum","aktion":"neustarten","container":["pensum-app"]}`
	if code, b = r.api(t, "POST", "/api/container/aktion", sid, act); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b = r.api(t, "POST", "/api/container/aktion", sid, `{"ziel":"projekt:pensum","aktion":"neustarten"}`); code != http.StatusBadRequest || !strings.Contains(b, "Vorschau") {
		t.Errorf("ohne Vorschau: %d %s", code, b)
	}
	if code, _ = r.api(t, "POST", "/api/container/aktion", sid, `{"ziel":"projekt:pensum","aktion":"loeschen","container":[]}`); code != http.StatusBadRequest {
		t.Errorf("Aktion außerhalb der Liste: %d", code)
	}

	// Ein Lauf zur Zeit: Während eines Backups wartet die Aktion.
	l, err := lauf.TakeIn(r.d.RunDir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	if code, b = r.api(t, "POST", "/api/container/aktion", sid, act); code != http.StatusConflict || !strings.Contains(b, "Backup") {
		t.Errorf("trotz Laufsperre: %d %s", code, b)
	}
	l.Release()
	if len(fc.done) != 0 {
		t.Fatalf("schon gehandelt: %v", fc.done)
	}

	code, b = r.api(t, "POST", "/api/container/aktion", sid, act)
	if code != 200 {
		t.Fatalf("Aktion: %d %s", code, b)
	}
	var res betrieb.Result
	if err := json.Unmarshal([]byte(b), &res); err != nil || !res.OK {
		t.Fatalf("Ergebnis: %v %s", err, b)
	}
	if len(fc.done) != 1 || fc.done[0] != "neustarten projekt:pensum" {
		t.Errorf("Aufträge %v", fc.done)
	}
	if lim.rechecks != 1 {
		t.Errorf("Seitenprüfung nach der Aktion %d-mal neu angestoßen, erwartet 1", lim.rechecks)
	}
	if _, ok := fc.ctx.Deadline(); !ok {
		t.Error("Aktion ohne Zeitlimit")
	}
	// Die Sperre ist danach wieder frei.
	if i, _ := lauf.CurrentIn(r.d.RunDir); i != nil {
		t.Errorf("Sperre hängt: %+v", i)
	}
	jr := journal(t, r)
	if !strings.Contains(jr, "container|") || !strings.Contains(jr, "Projekt pensum neu gestartet") || !strings.Contains(jr, "203.0.113.9") {
		t.Errorf("Protokoll: %s", jr)
	}
}

func TestBetriebOhneDocker(t *testing.T) {
	r, _ := doorRigWith(t, nil, nil)
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, _ := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test"})
	if code, _ := r.api(t, "GET", "/api/container", sid, ""); code != http.StatusNotFound {
		t.Errorf("ohne Betrieb: %d", code)
	}
}

// recheckLimits zählt, wie oft die Seitenprüfung neu angestoßen wird.
type recheckLimits struct{ rechecks int }

func (l *recheckLimits) SetLimits(ueberwachung.Limits) ([]string, error) { return nil, nil }
func (l *recheckLimits) RecheckSites()                                   { l.rechecks++ }
