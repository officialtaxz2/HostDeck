package ueberwachung

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
)

// Während ein Lauf von HostDeck arbeitet (Backup, letzter Lauf, Umzug), stoppt
// er selbst Container, und Seiten antworten nicht. Das ist keine Warnung
// wert, und eine laufende Warnung darf sich dadurch auch nicht in eine
// Entwarnung auflösen.
func TestKeineWarnungWaehrendLauf(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.conts = []ContainerState{{Name: "web", State: "running", Running: true}}
	g.sites = []SiteCheck{{Host: "a.example", OK: true}}
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	busy := ""
	r.Busy = func() string { return busy }
	ctx := context.Background()
	info := Info{RAMTotal: 16e9, Containers: []ContainerInfo{{Name: "web", State: "running", Health: "unhealthy"}}}
	step := func(d time.Duration) {
		g.now = g.now.Add(d)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	// „unhealthy“ seit 6 Minuten: Warnung.
	for i := 0; i < 36; i++ {
		step(10 * time.Second)
	}
	if k := g.kinds(); k != "warnung gesundheit:web" {
		t.Fatalf("Vorbedingung: %s", k)
	}

	// Jetzt arbeitet ein Lauf: Container aus, Seite weg.
	busy = "Letzter Lauf"
	g.conts = []ContainerState{{Name: "web", State: "exited", ExitCode: 143}}
	g.sites = []SiteCheck{{Host: "a.example", Fails: 3, Answer: "keine Antwort"}}
	info.Containers = nil
	for i := 0; i < 120; i++ { // 20 Minuten
		step(10 * time.Second)
	}
	if k := g.kinds(); k != "" {
		t.Errorf("Während des Laufs: %s", k)
	}

	// Lauf vorbei, alles wieder da: Entwarnung wie gewohnt.
	busy = ""
	g.conts = []ContainerState{{Name: "web", State: "running", Running: true}}
	g.sites = []SiteCheck{{Host: "a.example", OK: true}}
	for i := 0; i < 66; i++ {
		step(10 * time.Second)
	}
	if k := g.kinds(); k != "entwarnung gesundheit:web" {
		t.Errorf("Nach dem Lauf: %s", k)
	}
}

// Der Leistungstest lastet den Prozessor absichtlich aus: Prozessor und
// Steal warnen dann nicht. Er stoppt aber keine Container, also bleibt
// alles andere wach.
func TestLeistungstestPausiertNurProzessor(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.conts = []ContainerState{{Name: "web", State: "running", Running: true}}
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	r.Busy = func() string { return lauf.Leistungstest }
	ctx := context.Background()
	info := Info{RAMTotal: 16e9}
	step := func(d time.Duration) {
		g.now = g.now.Add(d)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 100, KeySteal: 40, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 240; i++ { // 40 Minuten voll
		step(10 * time.Second)
	}
	if k := g.kinds(); k != "" {
		t.Fatalf("Während des Tests: %s", k)
	}
	// Ein Container fällt während des Tests aus: Das meldet HostDeck.
	g.conts = []ContainerState{{Name: "web", State: "exited", ExitCode: 1}}
	for i := 0; i < 60; i++ {
		step(10 * time.Second)
	}
	if k := g.kinds(); k != "warnung aus:web" {
		t.Fatalf("Container aus während des Tests: %q", k)
	}
	// Ohne Lauf meldet die gleiche Last sich wie gewohnt.
	r.Busy = func() string { return "" }
	for i := 0; i < 100; i++ {
		step(10 * time.Second)
	}
	if k := g.kinds(); !strings.Contains(k, "warnung prozessor") {
		t.Fatalf("Ohne Test: %q", k)
	}
}

// Tuning stoppt nichts und lastet nichts aus: Während es arbeitet, prüft
// die Überwachung alles weiter, auch den Prozessor.
func TestTuningPausiertNichts(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.conts = []ContainerState{{Name: "web", State: "running", Running: true}}
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	r.Busy = func() string { return lauf.Tuning }
	ctx := context.Background()
	info := Info{RAMTotal: 16e9}
	step := func(d time.Duration) {
		g.now = g.now.Add(d)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 100, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 240; i++ {
		step(10 * time.Second)
	}
	if k := g.kinds(); !strings.Contains(k, "warnung prozessor") {
		t.Fatalf("Prozessor während Tuning: %q", k)
	}
	g.conts = []ContainerState{{Name: "web", State: "exited", ExitCode: 1}}
	for i := 0; i < 60; i++ {
		step(10 * time.Second)
	}
	if k := g.kinds(); !strings.Contains(k, "warnung aus:web") {
		t.Fatalf("Container aus während Tuning: %q", k)
	}
}

// Auf einem abgelösten Server ruht nur die Prüfung der Seiten (die Domains
// zeigen auf den neuen Server). Ein Container, der hier trotzdem ständig
// neu startet, und eine volle Platte sind echt: Die Projekte-Seite zeigt
// sie rot, die Glocke muss dasselbe sagen (Labor S6).
func TestAbgeloestRuhtNurSeitenpruefung(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.conts = []ContainerState{{Name: "web", State: "exited", StoppedByYou: true}, {Name: "wackel", State: "restarting", Restarts: 9}}
	g.sites = []SiteCheck{{Host: "a.example", Fails: 5, Answer: "Status 502"}}
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	r.Busy = func() string { return Abgeloest }
	ctx := context.Background()
	info := Info{RAMTotal: 16e9, Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: 99e9}}}
	for i := 0; i < 60; i++ {
		g.now = g.now.Add(10 * time.Second)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	k := g.kinds()
	if strings.Contains(k, "seite:") || strings.Contains(k, "aus:web") {
		t.Errorf("Seite oder umgezogener Container meldet sich: %s", k)
	}
	if !strings.Contains(k, "neustarts:wackel") || !strings.Contains(k, "platz:/") {
		t.Errorf("Schleife oder Platte auf dem abgelösten Server fehlt: %s", k)
	}
}

// Stand eine Seiten-Warnung schon, bevor der Server abgelöst wurde, bliebe
// sie sonst für immer rot stehen, obwohl hier niemand mehr prüft (Labor
// 14.11., leer.beispiel.test auf dem alten Server). Sie endet mit einem
// Hinweis, nicht mit einer Entwarnung.
func TestAbgeloestBeendetAlteSeitenwarnung(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.sites = []SiteCheck{{Host: "a.example", Fails: 5, Answer: "Status 502"}}
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	busy := ""
	r.Busy = func() string { return busy }
	ctx := context.Background()
	info := Info{RAMTotal: 16e9}
	step := func(d time.Duration) {
		g.now = g.now.Add(d)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		step(SiteEvery)
	}
	if k := g.kinds(); k != "warnung seite:a.example" {
		t.Fatalf("Vorbedingung: %q", k)
	}
	busy = Abgeloest
	step(ContainerEvery)
	if k := g.kinds(); k != "hinweis seite:a.example" {
		t.Errorf("abgelöst: %q", k)
	}
	for i := 0; i < 20; i++ {
		step(SiteEvery)
	}
	if k := g.kinds(); k != "" {
		t.Errorf("danach: %q", k)
	}
}

// Hältst du einen Container in der Neustart-Schleife im Leitstand an, endet
// die Warnung, obwohl die Neustarts der letzten Stunde noch zählen. Seit R1
// mit einem stillen Hinweis statt einer Entwarnung aufs Handy: Er ist ja
// nicht wieder in Ordnung, sondern von dir gestoppt.
func TestSchleifeAngehaltenEndetWarnung(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.conts = []ContainerState{{Name: "wackel", State: "running", Running: true, Restarts: 1}}
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	ctx := context.Background()
	info := Info{RAMTotal: 16e9}
	step := func() {
		g.now = g.now.Add(ContainerEvery)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	for i := 2; i < 8; i++ {
		g.conts[0].Restarts = i
		step()
	}
	if k := g.kinds(); k != "warnung neustarts:wackel" {
		t.Fatalf("Vorbedingung: %q", k)
	}
	g.conts = []ContainerState{{Name: "wackel", State: "exited", ExitCode: 3, Restarts: 7, StoppedByYou: true}}
	for i := 0; i < 15; i++ {
		step()
	}
	if k := g.kinds(); k != "hinweis neustarts:wackel" {
		t.Errorf("angehalten: %q", k)
	}
}

// Endet die Schleife von selbst (der Container läuft wieder ruhig), endet
// auch die Warnung: 15 Minuten nach dem letzten Neustart wie looping() im
// Leitstand, dann die übliche Entwarnung. Vorher stand sie bis zu einer
// Stunde, während die Projekte-Seite schon „läuft“ zeigte (Labor S6).
func TestSchleifeVorbeiEndetWarnung(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.conts = []ContainerState{{Name: "wackel", State: "running", Running: true, Restarts: 1}}
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	ctx := context.Background()
	info := Info{RAMTotal: 16e9}
	step := func() {
		g.now = g.now.Add(ContainerEvery)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	for i := 2; i < 8; i++ {
		g.conts[0].Restarts = i
		step()
	}
	if k := g.kinds(); k != "warnung neustarts:wackel" {
		t.Fatalf("Vorbedingung: %q", k)
	}
	start := g.now
	var cleared time.Duration
	for g.now.Sub(start) < time.Hour {
		step()
		if k := g.kinds(); k != "" {
			if k != "entwarnung neustarts:wackel" {
				t.Fatalf("unerwartet: %q", k)
			}
			cleared = g.now.Sub(start)
			break
		}
	}
	if cleared < LoopFresh || cleared > LoopFresh+15*time.Minute {
		t.Errorf("Entwarnung nach %v, erwartet etwa %v plus Wartezeit", cleared, LoopFresh)
	}
}
