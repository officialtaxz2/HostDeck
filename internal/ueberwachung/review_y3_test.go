package ueberwachung

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Update von einer Fassung ohne Ereignisse: Die Datei fehlt, aber die
// Überwachung lief schon. Dann ist das Update das erste Ereignis. Auf einem
// neuen Server (Ordner leer) gibt es keins.
func TestJournalErsteDateiNachUpdate(t *testing.T) {
	now := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "warnungen.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "ereignisse.json")
	j := &Journal{File: file, Now: func() time.Time { return now }}
	if err := j.Started("y3"); err != nil {
		t.Fatal(err)
	}
	got := j.Since(time.Time{})
	if len(got) != 1 || got[0].Kind != HapUpdate || !strings.Contains(got[0].Title, "y3") {
		t.Fatalf("Update nach Fassung ohne Ereignisse: %+v", got)
	}
	if !j.Begun().Equal(now) {
		t.Errorf("sammelt seit %v, erwartet %v", j.Begun(), now)
	}
	// Neustart mit derselben Fassung: nichts Neues, „seit“ bleibt.
	now = now.Add(time.Hour)
	j2 := &Journal{File: file, Now: func() time.Time { return now }}
	_ = j2.Started("y3")
	if n := len(j2.Since(time.Time{})); n != 1 {
		t.Errorf("Neustart: %d Ereignisse", n)
	}
	if !j2.Begun().Equal(now.Add(-time.Hour)) {
		t.Errorf("seit nach Neustart: %v", j2.Begun())
	}

	// Neuer Server: Ordner ohne Überwachung, kein Ereignis.
	fresh := &Journal{File: filepath.Join(t.TempDir(), "ereignisse.json"), Now: func() time.Time { return now }}
	_ = fresh.Started("y3")
	if n := len(fresh.Since(time.Time{})); n != 0 {
		t.Errorf("neuer Server: %d Ereignisse", n)
	}
	if fresh.Begun().IsZero() {
		t.Error("neuer Server: seit fehlt")
	}
}

// Kann die Ereignis-Liste nicht schreiben, laufen die Warnungen weiter.
func TestRegelnTrotzEreignisFehler(t *testing.T) {
	dir := t.TempDir()
	block := filepath.Join(dir, "datei")
	if err := os.WriteFile(block, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	g := &ruleRig{now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.backup = g.now.Add(-24 * time.Hour)
	g.conts = []ContainerState{{Name: "web", State: "running", Running: true}}
	r := g.rules(filepath.Join(dir, "warnungen.json"))
	// Der Ordner der Ereignis-Datei ist eine Datei: Schreiben scheitert immer.
	r.Journal = &Journal{File: filepath.Join(block, "ereignisse.json"), Now: func() time.Time { return g.now }}
	ctx := context.Background()
	info := Info{RAMTotal: 16e9, Disks: []DiskInfo{{Mount: "/", Total: 100e9, Used: 50e9}}}
	f := Frame{At: g.now, Values: map[string]float64{KeyCPU: 5, KeyRAM: 4e9}}
	_ = r.Check(ctx, f, info)
	g.events = nil

	// Jetzt stoppst du web (ein Ereignis) und die Platte ist voll.
	g.now = g.now.Add(time.Minute)
	g.conts = []ContainerState{{Name: "web", State: "exited", StoppedByYou: true}}
	full := info
	full.Disks = []DiskInfo{{Mount: "/", Total: 100e9, Used: 96e9}}
	f.At = g.now
	err := r.Check(ctx, f, full)
	if err == nil || !strings.Contains(err.Error(), "Ereignisse") {
		t.Errorf("Fehler der Ereignis-Liste kommt nicht zurück: %v", err)
	}
	if k := g.kinds(); k != "warnung platz:/" {
		t.Errorf("Warnung trotz Ereignis-Fehler: %q", k)
	}
}
