package betrieb

import (
	"context"
	"testing"
	"time"
)

// Datenprüfung H2: Misst die Überwachung einen Container, gilt ihr Wert;
// docker stats misst nur die übrigen. Ohne Messung der Überwachung (etwa
// cgroup v1) misst docker stats alles wie bisher.
func TestEineQuelleFuerJetzt(t *testing.T) {
	f := newFake()
	s := newService(t, f)
	s.Measured = func() map[string]Measured {
		return map[string]Measured{"pensum-app": {CPU: 2.25, Mem: 42 << 20}, "pensum-db": {CPU: 0, Mem: 7 << 20, MemLimit: 1 << 30},
			"pensum-cache": {CPU: 9, Mem: 1}} // gestoppt: zählt nicht
	}
	snap, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := byName(snap)
	if a := m["pensum-app"]; a.CPU == nil || *a.CPU != 2.25 || a.Mem != 42<<20 {
		t.Errorf("App aus der Überwachung: %+v", a)
	}
	if d := m["pensum-db"]; d.CPU == nil || *d.CPU != 0 || d.MemLimit != 1<<30 {
		t.Errorf("Datenbank aus der Überwachung: %+v", d)
	}
	if c := m["pensum-cache"]; c.CPU != nil {
		t.Errorf("gestoppter Container mit Messwert: %+v", c)
	}
	// Die übrigen laufenden (Panel, Spielserver, arcane) misst docker stats.
	if a := m["arcane"]; a.CPU == nil || a.Mem != 100<<20 {
		t.Errorf("arcane ohne Messung der Überwachung: %+v", a)
	}
	f.mu.Lock()
	n := f.stats
	f.mu.Unlock()
	if n != 3 {
		t.Errorf("docker stats %d-mal, erwartet 3 (nur ohne Messung der Überwachung)", n)
	}
	// Ohne Messung der Überwachung: alles aus docker stats.
	s2 := newService(t, newFake())
	s2.Measured = func() map[string]Measured { return nil }
	snap2, _ := s2.List(context.Background())
	if a := byName(snap2)["pensum-app"]; a.CPU == nil || a.Mem != 100<<20 {
		t.Errorf("ohne Überwachung: %+v", a)
	}
}

// Spielserver eines Panels haben dieselben Zustände wie jeder Container,
// soweit HostDeck sie sicher kennt; aus ist bei ihnen kein Fehler.
func TestVerwalteteServerWieContainer(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		c                Container
		class, word, why string
		level            string
	}{
		{Container{ManagedBy: "wings", State: "running", Running: true}, KlasseLaeuft, WortLaeuft, "", StufeOK},
		{Container{ManagedBy: "wings", State: "running", Running: true, Health: "unhealthy"}, KlasseUnhealthy, WortUnhealthy, "", StufeWarn},
		{Container{ManagedBy: "wings", State: "restarting", Running: true, Restarts: 5}, KlasseSchleife, WortSchleife, "5 Neustarts", StufeErr},
		// Abgestürzt: Grund als Text, aber keine Warnung (E2).
		{Container{ManagedBy: "wings", State: "exited", ExitCode: 1}, KlasseAus, WortAus, "abgestürzt (Code 1)", StufeIdle},
		{Container{ManagedBy: "wings", State: "exited", ExitCode: 137}, KlasseAus, WortAus, "", StufeIdle},
		{Container{ManagedBy: "wings", State: "exited"}, KlasseAus, WortAus, "", StufeIdle},
	} {
		c := tc.c
		c.classify(now)
		if c.Class != tc.class || c.Word != tc.word || c.Why != tc.why || c.Level != tc.level {
			t.Errorf("%+v: %s/%s/%q/%s, erwartet %s/%s/%q/%s", tc.c, c.Class, c.Word, c.Why, c.Level, tc.class, tc.word, tc.why, tc.level)
		}
	}
}
