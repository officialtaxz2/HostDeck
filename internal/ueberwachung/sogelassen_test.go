package ueberwachung

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// „Bewusst so lassen“ (Designvorgabe, Grundregel 4): Eine Warnung, die du so
// lässt, erinnert nicht mehr und endet ohne Mitteilung. Wird es schlimmer,
// warnt HostDeck wieder; der Zustand überdauert einen Neustart des Dienstes.
func TestBewusstSoLassen(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	g := &ruleRig{now: time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC)}
	ctx := context.Background()
	check := func(r *Rules) {
		t.Helper()
		if err := r.Check(ctx, Frame{At: g.now}, Info{}); err != nil {
			t.Fatal(err)
		}
	}
	r := g.rules(file)
	check(r)
	if _, err := r.Accept("backup", true); !errors.Is(err, ErrNoAlert) {
		t.Fatalf("ohne laufende Warnung: %v", err)
	}
	g.now = g.now.Add(14 * 24 * time.Hour)
	check(r)
	if k := g.kinds(); k != "warnung backup" {
		t.Fatalf("nach 14 Tagen: %q", k)
	}
	if v := r.View(5); len(v.Active) != 1 || !v.Active[0].CanAccept || v.Active[0].Accepted != nil {
		t.Fatalf("vorher: %+v", v.Active)
	}
	msg, err := r.Accept("backup", true)
	if err != nil || !strings.HasPrefix(msg, "Bleibt bewusst so: ") {
		t.Fatalf("so lassen: %q %v", msg, err)
	}
	if k := g.kinds(); k != "" {
		t.Fatalf("so lassen schickt keine Mitteilung: %q", k)
	}
	if v := r.View(5); v.Active[0].Accepted == nil || v.Events[0].Kind != EvNote || !v.Events[0].Quiet {
		t.Fatalf("nachher: %+v %+v", v.Active, v.Events)
	}
	// Neustart des Dienstes, dann einen Tag später: keine Erinnerung.
	r = g.rules(file)
	g.now = g.now.Add(DefaultLimits.RemindAfter + time.Hour)
	check(r)
	if k := g.kinds(); k != "" {
		t.Fatalf("Erinnerung trotz so gelassen: %q", k)
	}
	// Rot: wieder eine Warnung wie jede andere.
	g.now = g.now.Add(16 * 24 * time.Hour)
	check(r)
	if k := g.kinds(); k != "dringend backup" {
		t.Fatalf("schlimmer: %q", k)
	}
	if v := r.View(5); v.Active[0].Accepted != nil {
		t.Fatalf("nach dem Schlimmerwerden noch so gelassen: %+v", v.Active[0])
	}
	if _, err := r.Accept("backup", true); err != nil {
		t.Fatal(err)
	}
	// Ende: Entwarnung nur im Verlauf, still.
	g.backup = g.now
	check(r)
	g.now = g.now.Add(DefaultLimits.ClearAfter)
	g.events = nil
	check(r)
	if len(g.events) != 1 || g.events[0].Kind != EvClear || !g.events[0].Quiet || !strings.Contains(g.events[0].Text, "bewusst so gelassen") {
		t.Fatalf("Entwarnung: %+v", g.events)
	}
	if v := r.View(5); len(v.Active) != 0 {
		t.Fatalf("noch aktiv: %+v", v.Active)
	}
}

// Platte und Neustart-Schleifen bleiben immer Warnungen; zurücknehmen geht
// und schickt die Warnung wieder unter „Braucht dich“.
func TestSoLassenNurWoEsPasst(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC), backup: time.Date(2026, 11, 4, 20, 0, 0, 0, time.UTC)}
	g.conts = []ContainerState{{Name: "bot", State: "exited", ExitCode: 1}}
	r := g.rules("")
	full := Info{Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: 85e9}}}
	if err := r.Check(context.Background(), Frame{At: g.now}, full); err != nil {
		t.Fatal(err)
	}
	if k := g.kinds(); k != "warnung aus:bot, warnung platz:/" {
		t.Fatalf("Warnungen: %q", k)
	}
	if _, err := r.Accept("platz:/", true); !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("Platte: %v", err)
	}
	for _, a := range r.View(0).Active {
		if a.CanAccept != (a.Key == "aus:bot") {
			t.Errorf("%s: so_lassen=%v", a.Key, a.CanAccept)
		}
	}
	if _, err := r.Accept("aus:bot", true); err != nil {
		t.Fatal(err)
	}
	msg, err := r.Accept("aus:bot", false)
	if err != nil || !strings.HasPrefix(msg, "Warnt wieder: ") {
		t.Fatalf("zurück: %q %v", msg, err)
	}
	for _, a := range r.View(0).Active {
		if a.Accepted != nil {
			t.Errorf("%s noch so gelassen", a.Key)
		}
	}
}

func TestSoLassenZweimalOhneProtokoll(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC), backup: time.Date(2026, 11, 4, 20, 0, 0, 0, time.UTC)}
	g.conts = []ContainerState{{Name: "bot", State: "exited", ExitCode: 1}}
	r := g.rules("")
	if err := r.Check(context.Background(), Frame{At: g.now}, Info{}); err != nil {
		t.Fatal(err)
	}
	if msg, err := r.Accept("aus:bot", false); msg != "" || err != nil {
		t.Fatalf("nicht so gelassen, zurücknehmen: %q %v", msg, err)
	}
	if msg, _ := r.Accept("aus:bot", true); msg == "" {
		t.Fatal("erstes Mal ohne Satz")
	}
	if msg, err := r.Accept("aus:bot", true); msg != "" || err != nil {
		t.Fatalf("zweites Mal: %q %v", msg, err)
	}
}
