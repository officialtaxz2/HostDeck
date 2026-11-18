package ueberwachung

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type ruleRig struct {
	now    time.Time
	sites  []SiteCheck
	conts  []ContainerState
	backup time.Time
	boot   time.Time
	events []Event
}

func (g *ruleRig) rules(file string) *Rules {
	return &Rules{
		File:       file,
		Now:        func() time.Time { return g.now },
		Sites:      func(context.Context) ([]SiteCheck, error) { return append([]SiteCheck(nil), g.sites...), nil },
		Containers: func(context.Context) ([]ContainerState, error) { return g.conts, nil },
		LastBackup: func() (time.Time, bool) { return g.backup, !g.backup.IsZero() },
		Boot:       func() time.Time { return g.boot },
		Event:      func(e Event) { g.events = append(g.events, e) },
		syncSites:  true,
	}
}

func (g *ruleRig) kinds() string {
	var out []string
	for _, e := range g.events {
		out = append(out, e.Kind+" "+e.Key)
	}
	g.events = nil
	return strings.Join(out, ", ")
}

func TestRegeln(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	g := &ruleRig{now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.backup = g.now.Add(-2 * 24 * time.Hour)
	g.sites = []SiteCheck{{Host: "a.example", OK: true}, {Host: "b.example", OK: true}}
	g.conts = []ContainerState{{Name: "web", State: "running", Running: true}, {Name: "alt", State: "exited", ExitCode: 1, StoppedByYou: true}}
	r := g.rules(file)
	ctx := context.Background()
	// /boot mit 1 GB und 41 % belegt ist kein Grund zur Warnung, obwohl
	// weniger als 5 GB frei sind.
	info := Info{RAMTotal: 16e9, Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: 50e9}, {Mount: "/boot", Total: 1e9, Used: 0.41e9}}}
	frame := func(cpu, ram float64) Frame {
		return Frame{At: g.now, Values: map[string]float64{KeyCPU: cpu, KeyRAM: ram}}
	}
	step := func(d time.Duration, f Frame, i Info) {
		t.Helper()
		g.now = g.now.Add(d)
		f.At = g.now
		if err := r.Check(ctx, f, i); err != nil {
			t.Fatal(err)
		}
	}
	step(0, frame(5, 4e9), info)
	if k := g.kinds(); k != "" {
		t.Fatalf("alles gut, aber: %s", k)
	}

	// Platte über 80 %: sofort gelb; über 90 % rot („dringend“).
	full := info
	full.Disks = []DiskInfo{{Mount: "/", Total: 100e9, Used: 85e9}}
	step(10*time.Second, frame(5, 4e9), full)
	if k := g.kinds(); k != "warnung platz:/" {
		t.Errorf("Platte: %s", k)
	}
	full.Disks = []DiskInfo{{Mount: "/", Total: 100e9, Used: 96e9}}
	step(10*time.Second, frame(5, 4e9), full)
	if k := g.kinds(); k != "dringend platz:/" {
		t.Errorf("Platte rot: %s", k)
	}
	// Arbeitsspeicher knapp erst nach 5 Minuten.
	for i := 0; i < 30; i++ {
		step(10*time.Second, frame(5, 15.5e9), full)
	}
	if k := g.kinds(); k != "" {
		t.Errorf("Speicher zu früh: %s", k)
	}
	step(10*time.Second, frame(5, 15.5e9), full)
	if k := g.kinds(); k != "warnung speicher" {
		t.Errorf("Speicher nach 5 Minuten: %s", k)
	}
	// Ein kurzer Ausrutscher nach unten setzt die Wartezeit für den
	// Prozessor nicht zurück; erst 10 Minuten ohne Befund fangen neu an.
	for i := 0; i < 80; i++ {
		step(10*time.Second, frame(95, 15.5e9), full)
	}
	step(10*time.Second, frame(10, 15.5e9), full)
	if k := g.kinds(); k != "" {
		t.Errorf("Prozessor zu früh: %s", k)
	}
	for i := 0; i < 10; i++ {
		step(10*time.Second, frame(95, 15.5e9), full)
	}
	if k := g.kinds(); k != "warnung prozessor" {
		t.Errorf("Prozessor nach 15 Minuten: %s", k)
	}
	// Eine lange Pause lässt die Wartezeit neu beginnen.
	for i := 0; i < 61; i++ {
		step(10*time.Second, frame(10, 15.5e9), full)
	}
	g.events = nil
	for i := 0; i < 80; i++ {
		step(10*time.Second, frame(95, 15.5e9), full)
	}
	if k := g.kinds(); k != "" {
		t.Errorf("Prozessor nach langer Pause zu früh: %s", k)
	}
	for i := 0; i < 11; i++ {
		step(10*time.Second, frame(95, 15.5e9), full)
	}
	if k := g.kinds(); k != "warnung prozessor" {
		t.Errorf("Prozessor nach Pause und 15 Minuten: %s", k)
	}

	// Alles wieder gut: Entwarnung erst nach 10 Minuten.
	for i := 0; i < 60; i++ {
		step(10*time.Second, frame(5, 4e9), info)
	}
	if k := g.kinds(); k != "" {
		t.Errorf("Entwarnung zu früh: %s", k)
	}
	if v := r.View(10); len(v.Active) != 3 || v.Active[0].Key != "platz:/" || v.Active[0].GoodFrom == nil {
		t.Errorf("laufend: %+v", v.Active)
	}
	step(10*time.Second, frame(5, 4e9), info)
	if k := g.kinds(); k != "entwarnung platz:/, entwarnung prozessor, entwarnung speicher" {
		t.Errorf("Entwarnung: %s", k)
	}

	// Seite antwortet nicht: rot nach der zweiten Prüfung; pausierte nie.
	g.sites = []SiteCheck{{Host: "a.example", Answer: "Status 502"}, {Host: "b.example", Answer: "Status 502", Paused: "alt"}}
	step(SiteEvery, frame(5, 4e9), info)
	if k := g.kinds(); k != "" {
		t.Errorf("Seite nach einer Prüfung: %s", k)
	}
	step(SiteEvery, frame(5, 4e9), info)
	if k := g.kinds(); k != "warnung seite:a.example" {
		t.Errorf("Seite nach zwei Prüfungen: %s", k)
	}
	v := r.View(10)
	if len(v.Sites) != 2 || v.Sites[0].Fails != 2 || v.Sites[0].Outages != 1 || v.Sites[0].LastDown == nil {
		t.Errorf("Seiten: %+v", v.Sites)
	}

	// Container unerwartet aus und ständige Neustarts; Spielserver und
	// was du gestoppt hast, nie.
	g.conts = []ContainerState{{Name: "web", State: "exited", ExitCode: 137}, {Name: "alt", State: "exited", ExitCode: 1, StoppedByYou: true},
		{Name: "spiel", State: "exited", ExitCode: 1, Managed: true}, {Name: "job", State: "exited", Job: true}}
	step(time.Minute, frame(5, 4e9), info)
	if k := g.kinds(); k != "warnung aus:web" {
		t.Errorf("Container aus: %s", k)
	}
	if v := r.View(1); !strings.Contains(v.Events[0].Text, "137") || !strings.Contains(v.Events[0].Text, "Arbeitsspeicher") {
		t.Errorf("Text: %q", v.Events[0].Text)
	}
	g.conts = []ContainerState{{Name: "web", State: "running", Running: true, Restarts: 1}}
	step(time.Minute, frame(5, 4e9), info)
	g.conts = []ContainerState{{Name: "web", State: "running", Running: true, Restarts: 4}}
	step(time.Minute, frame(5, 4e9), info)
	if k := g.kinds(); k != "warnung neustarts:web" {
		t.Errorf("Neustarts: %s", k)
	}

	// Erinnerung nach 24 Stunden für die Seite, die noch nicht antwortet.
	g.conts = []ContainerState{{Name: "web", State: "running", Running: true, Restarts: 4}}
	for i := 0; i < 24*12; i++ {
		g.now = g.now.Add(SiteEvery)
		r.Check(ctx, frame(5, 4e9), info)
	}
	k := g.kinds()
	if !strings.Contains(k, "erinnerung seite:a.example") || !strings.Contains(k, "entwarnung neustarts:web") || !strings.Contains(k, "entwarnung aus:web") {
		t.Errorf("nach einem Tag: %s", k)
	}

	// Backup alt: gelb nach 14 Tagen.
	g.backup = g.now.Add(-15 * 24 * time.Hour)
	step(10*time.Second, frame(5, 4e9), info)
	if k := g.kinds(); k != "warnung backup" {
		t.Errorf("Backup: %s", k)
	}

	// Neu laden (Update von HostDeck): keine neue Warnung für Laufendes, aber
	// ein Hinweis, wenn der Server neu gestartet ist.
	g.boot = g.now.Add(-time.Minute)
	r2 := g.rules(file)
	r = r2
	step(10*time.Second, frame(5, 4e9), info)
	if k := g.kinds(); k != "hinweis neustart" {
		t.Errorf("nach dem Neuladen: %s", k)
	}
	if v := r.View(100); len(v.Active) != 2 || len(v.Events) < 10 {
		t.Errorf("geladen: %d laufend, %d Ereignisse", len(v.Active), len(v.Events))
	}
}

func TestPlatteKleinUndGross(t *testing.T) {
	r := &Rules{}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		d    DiskInfo
		want string
	}{
		{DiskInfo{Mount: "/boot", Total: 1e9, Used: 0.41e9}, ""},
		{DiskInfo{Mount: "/boot", Total: 1e9, Used: 0.85e9}, LevelWarn},
		{DiskInfo{Mount: "/boot", Total: 1e9, Used: 0.95e9}, LevelErr},
		{DiskInfo{Mount: "/", Total: 19e9, Used: 15e9}, ""},          // unter 20 GB nur nach Anteil
		{DiskInfo{Mount: "/", Total: 40e9, Used: 31.5e9}, ""},        // 79 %, 8,5 GB frei
		{DiskInfo{Mount: "/", Total: 25e9, Used: 20.5e9}, LevelErr},  // 82 %, aber nur 4,5 GB frei
		{DiskInfo{Mount: "/", Total: 400e9, Used: 330e9}, LevelWarn}, // 83 %
	} {
		got := ""
		for _, x := range r.findings(Frame{At: now}, Info{Disks: []DiskInfo{c.d}}, now) {
			got = x.Level
		}
		if got != c.want {
			t.Errorf("%s %.0f/%.0f GB: %q, erwartet %q", c.d.Mount, float64(c.d.Used)/1e9, float64(c.d.Total)/1e9, got, c.want)
		}
	}
}

func TestGrenzenAendern(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	r := &Rules{File: file}
	b, _ := json.Marshal(r.View(0).Limits)
	if !strings.Contains(string(b), `"prozessor_minuten":15`) || !strings.Contains(string(b), `"platte_gelb":0.8`) {
		t.Fatalf("JSON: %s", b)
	}
	var l Limits
	if err := json.Unmarshal([]byte(`{"platte_gelb":0.85,"platte_rot":0.95,"prozessor_minuten":30}`), &l); err != nil {
		t.Fatal(err)
	}
	if l.DiskWarn != 0.85 || l.CPUHold != 30*time.Minute || l.MemHold != DefaultLimits.MemHold || l.ClearAfter != DefaultLimits.ClearAfter {
		t.Fatalf("gelesen: %+v", l)
	}
	ch, err := r.SetLimits(l)
	if err != nil || strings.Join(ch, "; ") != "Platte gelb: 80 % belegt → 85 % belegt; Platte rot: 90 % belegt → 95 % belegt; Prozessor Dauer: 15 Minuten → 30 Minuten" {
		t.Errorf("Änderungen: %q %v", ch, err)
	}
	// Bleibt nach Neustart.
	r2 := &Rules{File: file}
	if g := r2.View(0).Limits; g.DiskWarn != 0.85 || g.CPUHold != 30*time.Minute {
		t.Errorf("nach Neustart: %+v", g)
	}
	// Unsinn lehnt HostDeck ab, und nichts ändert sich.
	bad := l
	bad.DiskErr = 0.7
	bad.CertErrDays = 20
	if _, err := r2.SetLimits(bad); err == nil || !strings.Contains(err.Error(), "Platte rot") || !strings.Contains(err.Error(), "Zertifikat rot") {
		t.Errorf("Unsinn: %v", err)
	}
	if g := r2.View(0).Limits; g.DiskErr != 0.95 {
		t.Errorf("trotz Fehler geändert: %+v", g)
	}
	// Zurück auf Voreinstellungen löscht die Datei.
	if ch, err := r2.SetLimits(DefaultLimits); err != nil || len(ch) != 3 {
		t.Errorf("Voreinstellungen: %q %v", ch, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(file), "grenzen.json")); !os.IsNotExist(err) {
		t.Errorf("Datei noch da: %v", err)
	}
	// Die neue Grenze wirkt: 85 % belegt ist bei gelb ab 90 % kein Befund.
	r3 := &Rules{}
	l90 := DefaultLimits
	l90.DiskWarn, l90.DiskErr = 0.9, 0.95
	if _, err := r3.SetLimits(l90); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if f := r3.findings(Frame{At: now}, Info{Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: 85e9}}}, now); len(f) != 0 {
		t.Errorf("Befund trotz Grenze: %+v", f)
	}
}

// Gibt es noch gar kein gültiges Backup, etwa auf einem frisch
// eingerichteten oder gerade umgezogenen Server, zählt die Zeit ab dem ersten
// Blick der Überwachung, auch über einen Neustart des Dienstes hinweg.
func TestOhneBackupWarntNachGrenze(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	g := &ruleRig{now: time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC)}
	ctx := context.Background()
	check := func(r *Rules) {
		t.Helper()
		if err := r.Check(ctx, Frame{At: g.now}, Info{}); err != nil {
			t.Fatal(err)
		}
	}
	check(g.rules(file))
	if k := g.kinds(); k != "" {
		t.Fatalf("gleich nach dem Einrichten schon: %s", k)
	}
	g.now = g.now.Add(13 * 24 * time.Hour)
	r := g.rules(file) // Dienst neu gestartet
	check(r)
	if k := g.kinds(); k != "" {
		t.Fatalf("nach 13 Tagen schon: %s", k)
	}
	g.now = g.now.Add(24 * time.Hour)
	check(r)
	if k := g.kinds(); k != "warnung backup" {
		t.Fatalf("nach 14 Tagen ohne Backup: %q", k)
	}
	if v := r.View(5); len(v.Active) != 1 || !strings.Contains(v.Active[0].Title, "kein gültiges Backup") {
		t.Fatalf("Titel: %+v", v.Active)
	}
	g.now = g.now.Add(16 * 24 * time.Hour)
	check(r)
	if k := g.kinds(); k != "dringend backup" {
		t.Fatalf("nach 30 Tagen: %q", k)
	}
	// Erstes Backup: Entwarnung, und ein späteres Fehlen zählt neu.
	g.backup = g.now
	check(r)
	g.now = g.now.Add(DefaultLimits.ClearAfter)
	check(r)
	if k := g.kinds(); k != "entwarnung backup" {
		t.Fatalf("nach dem ersten Backup: %q", k)
	}
	g.backup = time.Time{}
	g.now = g.now.Add(time.Hour)
	check(r)
	if k := g.kinds(); k != "" {
		t.Fatalf("Backups weg, aber gerade erst: %q", k)
	}
}

func TestAusfallTage(t *testing.T) {
	loc := time.FixedZone("Ort", 3600)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, loc)
	key := "seite:a.example"
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, loc) }
	ev := []Event{
		// gestern 23:30 bis heute 00:30 (Entwarnung 10 Minuten nach der Rückkehr)
		{At: at(4, 23, 30), Kind: EvStart, Key: key},
		{At: at(4, 23, 50), Kind: EvWorse, Key: key},
		{At: at(5, 0, 40), Kind: EvClear, Key: key},
		// fremde Seite zählt nicht
		{At: at(3, 10, 0), Kind: EvStart, Key: "seite:b.example"},
		// Entwarnung ohne Beginn im Verlauf: übergehen
		{At: at(1, 10, 0), Kind: EvClear, Key: key},
	}
	got := perDay(outageSpans(ev, &Alert{Key: key, Since: at(5, 11, 0)}, key, 10*time.Minute, now), now)
	if len(got) != DownDayCount {
		t.Fatalf("Länge %d", len(got))
	}
	if got[DownDayCount-2] != 30 || got[DownDayCount-1] != 30+60 {
		t.Errorf("gestern %d, heute %d", got[DownDayCount-2], got[DownDayCount-1])
	}
	for i, v := range got[:DownDayCount-2] {
		if v != 0 {
			t.Errorf("Tag %d: %d", i, v)
		}
	}
	// Wieder gut, Entwarnung steht noch aus: bis zur Rückkehr.
	g := at(5, 11, 20)
	got = perDay(outageSpans(nil, &Alert{Key: key, Since: at(5, 11, 0), GoodFrom: &g}, key, 10*time.Minute, now), now)
	if got[DownDayCount-1] != 20 {
		t.Errorf("mit gut_seit: %d", got[DownDayCount-1])
	}
}

// Seit wann eine Domain geprüft wird, überdauert einen Neustart des Dienstes:
// Sonst fangen die Tagesbalken nach jedem
// Update neu an.
func TestSeitenSeitUeberdauertNeustart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	g := &ruleRig{now: start, sites: []SiteCheck{{Host: "a.example", OK: true}}}
	g.backup = start
	ctx := context.Background()
	if err := g.rules(file).Check(ctx, Frame{At: g.now}, Info{}); err != nil {
		t.Fatal(err)
	}
	g.now = start.Add(3 * 24 * time.Hour)
	g.backup = g.now
	g.sites = append(g.sites, SiteCheck{Host: "b.example", OK: true})
	r := g.rules(file) // neu gestartet, etwa nach einem Update
	if err := r.Check(ctx, Frame{At: g.now}, Info{}); err != nil {
		t.Fatal(err)
	}
	v := r.View(0)
	if len(v.Sites) != 2 || v.Sites[0].Since == nil || !v.Sites[0].Since.Equal(start) {
		t.Fatalf("a.example: %+v", v.Sites)
	}
	if v.Sites[1].Since == nil || !v.Sites[1].Since.Equal(g.now) {
		t.Fatalf("b.example: %+v", v.Sites[1])
	}
}

// K12: In der Pause zählt keine Prüfung als Fehlversuch. Nach dem Starten
// löst eine einzelne gescheiterte Prüfung beim Hochfahren nichts aus, und die
// Tagesbalken zeigen die Pause nicht als Ausfall.
func TestSeitePauseOhneAltlast(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	g := &ruleRig{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.backup = g.now.Add(-time.Hour)
	r := g.rules(file)
	ctx := context.Background()
	info := Info{RAMTotal: 16e9, Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: 10e9}}}
	step := func() {
		t.Helper()
		g.now = g.now.Add(SiteEvery)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	g.sites = []SiteCheck{{Host: "blog.example", Answer: "Status 502", Paused: "du hast blog-web-1 gestoppt"}}
	for i := 0; i < 6; i++ {
		step()
	}
	if k := g.kinds(); k != "" {
		t.Fatalf("pausiert, aber: %s", k)
	}
	if v := r.View(0); len(v.Sites) != 1 || v.Sites[0].Fails != 0 || v.Sites[0].Paused == "" {
		t.Fatalf("pausiert: %+v", v.Sites)
	}
	// Wieder gestartet, die erste Prüfung fällt noch ins Hochfahren.
	g.sites = []SiteCheck{{Host: "blog.example", Answer: "Status 502"}}
	step()
	if k := g.kinds(); k != "" {
		t.Errorf("eine Prüfung nach dem Start: %s", k)
	}
	g.sites = []SiteCheck{{Host: "blog.example", OK: true, Answer: "Status 200"}}
	step()
	if k := g.kinds(); k != "" {
		t.Errorf("nach dem Start: %s", k)
	}
	v := r.View(0)
	for _, n := range v.Sites[0].DownDays {
		if n != 0 {
			t.Errorf("Tagesbalken zeigen die Pause als Ausfall: %v", v.Sites[0].DownDays)
		}
	}
}

// M7: Ist nur ein anderer Container des Projekts von dir gestoppt, bleibt
// eine Seite, die nicht antwortet, eine Warnung, aber gelb und mit Hinweis.
func TestSeiteMitGestopptemNachbarGelb(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	g := &ruleRig{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.backup = g.now.Add(-time.Hour)
	r := g.rules(file)
	info := Info{RAMTotal: 16e9, Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: 10e9}}}
	g.sites = []SiteCheck{{Host: "app.example", Answer: "Status 502", Hint: "Du hast app-db-1 gestoppt; liegt es daran?"}}
	for i := 0; i < 2; i++ {
		g.now = g.now.Add(SiteEvery)
		if err := r.Check(context.Background(), Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	if k := g.kinds(); k != "warnung seite:app.example" {
		t.Fatalf("Warnung fehlt: %s", k)
	}
	v := r.View(0)
	if len(v.Active) != 1 || v.Active[0].Level != LevelWarn || !strings.Contains(v.Active[0].Text, "app-db-1") {
		t.Errorf("gelb mit Hinweis erwartet: %+v", v.Active)
	}
}

// Labor S4: Nach Neustarts des Dienstes (Updates) fing der Fehlerzähler einer
// kaputten Seite bei 0 an. Die laufende Warnung löste sich dann in ein
// „Wieder in Ordnung“ auf, obwohl die Seite nie wieder geantwortet hatte.
func TestSeiteKeineEntwarnungNachNeustart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	g := &ruleRig{now: time.Date(2026, 11, 14, 7, 30, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.backup = g.now.Add(-time.Hour)
	ctx := context.Background()
	info := Info{RAMTotal: 16e9, Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: 10e9}}}
	g.sites = []SiteCheck{{Host: "leer.example", Answer: "Status 502"}}
	r := g.rules(file)
	step := func() {
		t.Helper()
		g.now = g.now.Add(SiteEvery)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	step()
	step()
	if k := g.kinds(); k != "warnung seite:leer.example" {
		t.Fatalf("Warnung: %q", k)
	}
	// Viermal neu gestartet, jeweils nach einer Prüfung: Der Zähler kommt nie
	// über 1, die Seite antwortet aber auch nie.
	// Gespeichert wird bei jedem anderen Ereignis; hier von Hand.
	for i := 0; i < 4; i++ {
		r = g.rules(file)
		step()
		r.mu.Lock()
		_ = r.save()
		r.mu.Unlock()
	}
	if k := g.kinds(); k != "" {
		t.Fatalf("falsche Entwarnung: %q", k)
	}
	if v := r.View(0); len(v.Active) != 1 {
		t.Fatalf("Warnung muss stehen bleiben: %+v", v.Active)
	}
	// Antwortet sie wirklich wieder, kommt die Entwarnung wie gewohnt.
	g.sites = []SiteCheck{{Host: "leer.example", OK: true, Answer: "Status 200"}}
	for i := 0; i < 4; i++ {
		step()
	}
	if k := g.kinds(); k != "entwarnung seite:leer.example" {
		t.Fatalf("Entwarnung: %q", k)
	}
	// Eine Domain, die ganz aus Caddy verschwindet, hält keine Warnung fest;
	// sie endet mit einem Hinweis, nicht mit „Wieder in Ordnung“.
	g.sites = []SiteCheck{{Host: "weg.example", Answer: "Status 502"}}
	step()
	step()
	g.kinds()
	g.sites = nil
	for i := 0; i < 4; i++ {
		step()
	}
	if k := g.kinds(); k != "hinweis seite:weg.example" {
		t.Fatalf("entfernte Domain: %q", k)
	}
}

// Labor „Domain aus“: Eine Domain, die rot warnte und dann abgeschaltet
// wird, ist nicht „wieder in Ordnung“. Die Warnung endet mit der nächsten
// Prüfung, und die kommt nach der Änderung gleich (RecheckSites), nicht
// erst nach 5 Minuten.
func TestAbgeschalteteDomainEndetOhneEntwarnung(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	g := &ruleRig{now: time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.backup = g.now.Add(-time.Hour)
	g.sites = []SiteCheck{{Host: "a.example", OK: true}, {Host: "leer.example", Answer: "Status 502"}}
	r := g.rules(file)
	ctx := context.Background()
	info := Info{RAMTotal: 16e9, Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: 10e9}}}
	step := func(d time.Duration) {
		t.Helper()
		g.now = g.now.Add(d)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		step(SiteEvery)
	}
	if k := g.kinds(); k != "warnung seite:leer.example" {
		t.Fatalf("Vorbedingung: %q", k)
	}
	// Abgeschaltet: Caddy prüft sie nicht mehr.
	g.sites = []SiteCheck{{Host: "a.example", OK: true}}
	step(10 * time.Second)
	if k := g.kinds(); k != "" {
		t.Fatalf("vor der neuen Prüfung schon: %q", k)
	}
	r.RecheckSites()
	step(10 * time.Second)
	if k := g.kinds(); k != "hinweis seite:leer.example" {
		t.Fatalf("nach dem Abschalten: %q", k)
	}
	if v := r.View(5); len(v.Active) != 0 || len(v.Sites) != 1 {
		t.Errorf("Stand danach: %d aktiv, %d Seiten", len(v.Active), len(v.Sites))
	}
	for i := 0; i < 6; i++ {
		step(SiteEvery)
	}
	if k := g.kinds(); k != "" {
		t.Errorf("später noch: %q", k)
	}
}

// N21: Kommt die Bitte um eine neue Seitenprüfung, während eine läuft (die
// Caddy vielleicht schon vor der Änderung gelesen hat), folgt gleich danach
// noch eine, statt erst nach fünf Minuten.
func TestRecheckSitesWaehrendPruefung(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	calls := 0
	r := &Rules{Now: func() time.Time { return now }, Sites: func(context.Context) ([]SiteCheck, error) {
		calls++
		return []SiteCheck{{Host: "a.example.org", OK: true}}, nil
	}}
	r.mu.Lock()
	r.sitesBusy = true
	r.mu.Unlock()
	r.RecheckSites()
	r.checkSites(context.Background())
	r.mu.Lock()
	again := r.sitesAt.IsZero() && !r.sitesNext && !r.sitesBusy
	r.mu.Unlock()
	if !again {
		t.Fatalf("nach der laufenden Prüfung keine neue bestellt (sitesAt %v)", r.sitesAt)
	}
	// Ohne Bitte bleibt es beim Takt von fünf Minuten.
	r.checkSites(context.Background())
	if r.sitesAt.IsZero() {
		t.Fatal("ohne Bitte gleich wieder geprüft")
	}
	if calls != 2 {
		t.Fatalf("%d Prüfungen, erwartet 2", calls)
	}
}
