package ueberwachung

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func durchsichtRig(t *testing.T) (*ruleRig, *Rules) {
	g := &ruleRig{now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	return g, g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
}

// Zwei einzelne Aussetzer von Docker, Minuten auseinander, sind keine
// Warnung „seit mindestens 2 Minuten“: Die Lücke dazwischen ist länger
// als ein Zehntel der Wartezeit.
func TestEinzelneAussetzerKeineWarnung(t *testing.T) {
	g, r := durchsichtRig(t)
	ctx := context.Background()
	ok := Info{RAMTotal: 16e9}
	broken := Info{RAMTotal: 16e9, Problems: []string{"Docker: Zeitüberschreitung"}}
	step := func(i Info) {
		g.now = g.now.Add(10 * time.Second)
		_ = r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyRAM: 4e9}}, i)
	}
	step(broken)
	for i := 0; i < 48; i++ { // 8 Minuten gut
		step(ok)
	}
	step(broken)
	step(ok)
	if k := g.kinds(); k != "" {
		t.Errorf("zwei Aussetzer: %s", k)
	}
	// Dauerhaft weg: nach 2 Minuten eine Warnung.
	for i := 0; i < 13; i++ {
		step(broken)
	}
	if k := g.kinds(); k != "warnung docker" {
		t.Errorf("Docker 2 Minuten weg: %q", k)
	}
}

// Wird eine rote Warnung wieder gelb, bleibt sie rot, bis es 10 Minuten
// milder war, und mit ihr Titel und Text der roten Stufe.
func TestGehalteneStufeBehaeltWortlaut(t *testing.T) {
	g, r := durchsichtRig(t)
	ctx := context.Background()
	disk := func(used int64) Info {
		return Info{RAMTotal: 16e9, Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: used}}}
	}
	step := func(i Info) {
		g.now = g.now.Add(10 * time.Second)
		_ = r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyRAM: 4e9}}, i)
	}
	step(disk(97e9))
	g.events = nil
	for i := 0; i < 30; i++ { // 5 Minuten bei 85 %
		step(disk(85e9))
	}
	v := r.View(10)
	if len(v.Active) != 1 || v.Active[0].Level != LevelErr || v.Active[0].Title != "Platte fast voll (/)" {
		t.Fatalf("gehalten: %+v", v.Active)
	}
	for i := 0; i < 31; i++ { // weitere 5 Minuten: jetzt 10 Minuten milder
		step(disk(85e9))
	}
	v = r.View(10)
	if len(v.Active) != 1 || v.Active[0].Level != LevelWarn || v.Active[0].Title != "Platte wird knapp (/)" {
		t.Errorf("nach 10 Minuten milder: %+v", v.Active)
	}
	if k := g.kinds(); k != "" {
		t.Errorf("Ereignisse beim Zurückstufen: %s", k)
	}
}

// Eine Seite, die schon vor einem Lauf ausgefallen war und danach noch
// ausgefallen ist, bekommt keine Entwarnung zwischendurch.
func TestSeiteAusVorUndNachLauf(t *testing.T) {
	g, r := durchsichtRig(t)
	busy := ""
	r.Busy = func() string { return busy }
	ctx := context.Background()
	g.sites = []SiteCheck{{Host: "a.example", Answer: "keine Antwort"}}
	step := func() {
		g.now = g.now.Add(10 * time.Second)
		_ = r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyRAM: 4e9}}, Info{RAMTotal: 16e9})
	}
	for i := 0; i < 61; i++ { // 10 Minuten: zwei Fehlversuche
		step()
	}
	if k := g.kinds(); k != "warnung seite:a.example" {
		t.Fatalf("Vorbedingung: %q", k)
	}
	busy = "Backup"
	for i := 0; i < 180; i++ { // 30 Minuten Lauf
		step()
	}
	busy = ""
	for i := 0; i < 120; i++ { // 20 Minuten danach, weiter aus
		step()
	}
	if k := g.kinds(); k != "" {
		t.Errorf("Seite blieb aus, trotzdem: %s", k)
	}
}

// Ein Umzug installiert oder startet Docker neu: Das ist keine Warnung
// „Docker antwortet nicht“.
func TestDockerWegWaehrendLauf(t *testing.T) {
	g, r := durchsichtRig(t)
	r.Busy = func() string { return "Umzug" }
	ctx := context.Background()
	broken := Info{RAMTotal: 16e9, Problems: []string{"Docker: connection refused"}}
	for i := 0; i < 60; i++ {
		g.now = g.now.Add(10 * time.Second)
		_ = r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyRAM: 4e9}}, broken)
	}
	if k := g.kinds(); k != "" {
		t.Errorf("während des Umzugs: %s", k)
	}
}
