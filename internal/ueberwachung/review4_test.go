package ueberwachung

// Regressionstests zu früheren Funden
// übernommen: Sie halten die Fixes H1, M1, M2, M3 und N1 fest.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func review4Rig(t *testing.T) (*ruleRig, *Rules) {
	g := &ruleRig{now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	return g, r
}

func countKind(evs []Event, kind string) int {
	n := 0
	for _, e := range evs {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// H1: Pendelt ein Wert um die rote Grenze, kommt bei jedem Übertritt
// sofort wieder „dringend“ (keine Wartezeit, keine Hysterese nach unten).
func TestReview4DringendPendelt(t *testing.T) {
	g, r := review4Rig(t)
	ctx := context.Background()
	var all []Event
	for i := 0; i < 60; i++ { // 10 Minuten, alle 10 s
		used := int64(89.5e9)
		if i%2 == 1 {
			used = 90.5e9
		}
		info := Info{RAMTotal: 16e9, Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: used}}}
		g.now = g.now.Add(10 * time.Second)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
		all = append(all, g.events...)
		g.events = nil
	}
	if n := countKind(all, EvWorse); n > 1 {
		t.Errorf("Platte pendelt 89,5/90,5 %% über 10 Minuten: %d-mal „dringend“ (je eine Mitteilung mit hoher Dringlichkeit), erwartet höchstens 1", n)
	}

	// Dasselbe beim Arbeitsspeicher um 5 % frei (nach der Wartezeit).
	g2, r2 := review4Rig(t)
	all = nil
	for i := 0; i < 120; i++ { // 20 Minuten
		free := 0.049
		if i%2 == 1 && i > 40 {
			free = 0.051
		}
		g2.now = g2.now.Add(10 * time.Second)
		ram := 16e9 * (1 - free)
		if err := r2.Check(ctx, Frame{At: g2.now, Values: map[string]float64{KeyRAM: ram}}, Info{RAMTotal: 16e9}); err != nil {
			t.Fatal(err)
		}
		all = append(all, g2.events...)
		g2.events = nil
	}
	if n := countKind(all, EvWorse); n > 1 {
		t.Errorf("Arbeitsspeicher pendelt 4,9/5,1 %% frei: %d-mal „dringend“, erwartet höchstens 1", n)
	}
}

// M1: Fällt die Messquelle aus (Docker antwortet nicht), verschwinden die
// Befunde, und nach 10 Minuten kommt eine Entwarnung, obwohl nichts
// besser wurde. Eine eigene Warnung „Docker antwortet nicht“ gibt es nicht.
func TestReview4EntwarnungWennMessungFehlt(t *testing.T) {
	g, r := review4Rig(t)
	ctx := context.Background()
	sick := Info{RAMTotal: 16e9, Containers: []ContainerInfo{{Name: "db", State: "running", Health: "unhealthy"}}}
	var all []Event
	for i := 0; i < 36; i++ { // 6 Minuten unhealthy -> Warnung
		g.now = g.now.Add(10 * time.Second)
		_ = r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyRAM: 4e9}}, sick)
	}
	all = append(all, g.events...)
	g.events = nil
	if countKind(all, EvStart) != 1 {
		t.Fatalf("Vorbedingung: erwartet eine Warnung, bekam %v", all)
	}
	// Docker antwortet nicht mehr: Collect liefert nur ein Problem.
	broken := Info{RAMTotal: 16e9, Problems: []string{"Docker: connection refused"}}
	all = nil
	for i := 0; i < 72; i++ { // 12 Minuten
		g.now = g.now.Add(10 * time.Second)
		_ = r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyRAM: 4e9}}, broken)
		all = append(all, g.events...)
		g.events = nil
	}
	var kinds []string
	for _, e := range all {
		kinds = append(kinds, e.Kind+" "+e.Key)
	}
	if countKind(all, EvClear) > 0 {
		t.Errorf("Docker weg, Container weiter unbekannt: trotzdem %s", strings.Join(kinds, ", "))
	}
	if len(all) == 0 || !strings.Contains(strings.Join(kinds, ","), "docker") {
		t.Errorf("Keine Warnung, dass Docker/die Messung ausgefallen ist: %v", kinds)
	}
}

// N: Haltezeit verlangt jeden einzelnen 10-s-Wert über der Grenze. Ein
// Server bei 95 % mit einem kurzen Einbruch alle 5 Minuten meldet nie.
func TestReview4HaltezeitLueckenlos(t *testing.T) {
	g, r := review4Rig(t)
	ctx := context.Background()
	var all []Event
	for i := 0; i < 360; i++ { // 1 Stunde
		cpu := 95.0
		if i%30 == 29 {
			cpu = 85
		}
		g.now = g.now.Add(10 * time.Second)
		_ = r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: cpu, KeyRAM: 4e9}}, Info{RAMTotal: 16e9})
		all = append(all, g.events...)
		g.events = nil
	}
	if countKind(all, EvStart) == 0 {
		t.Errorf("Prozessor 1 Stunde im Mittel 94,7 %% (alle 5 Minuten ein Wert 85 %%): keine Warnung")
	}
}
