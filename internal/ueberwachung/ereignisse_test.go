package ueberwachung

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalContainers(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	j := &Journal{File: filepath.Join(t.TempDir(), "ereignisse.json"), Now: func() time.Time { return now }}
	up := []ContainerState{{Name: "shop-web-1", Project: "shop", Running: true}, {Name: "shop-db-1", Project: "shop", Running: true}, {Name: "solo", Running: false}}
	// Erster Blick: nur merken.
	if err := j.containers(up, "", now); err != nil {
		t.Fatal(err)
	}
	if n := len(j.Since(time.Time{})); n != 0 {
		t.Fatalf("erster Blick erzeugt %d Ereignisse", n)
	}
	// Von dir gestoppt: ein Ereignis je Projekt.
	down := []ContainerState{{Name: "shop-web-1", Project: "shop", StoppedByYou: true}, {Name: "shop-db-1", Project: "shop", StoppedByYou: true}, {Name: "solo", Running: true}}
	if err := j.containers(down, "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := j.Since(time.Time{})
	if len(got) != 2 {
		t.Fatalf("Ereignisse: %+v", got)
	}
	titles := got[0].Title + "|" + got[1].Title
	if !strings.Contains(titles, "shop gestoppt") || !strings.Contains(titles, "solo gestartet") {
		t.Errorf("Titel: %s", titles)
	}
	// Unerwartet aus: das melden die Regeln, nicht das Journal.
	crash := []ContainerState{{Name: "shop-web-1", Project: "shop", StoppedByYou: true}, {Name: "shop-db-1", Project: "shop", StoppedByYou: true}, {Name: "solo", Running: false, ExitCode: 137}}
	_ = j.containers(crash, "", now.Add(2*time.Minute))
	// Docker startet ihn nach dem Absturz neu: kein „gestartet“.
	again := []ContainerState{{Name: "shop-web-1", Project: "shop", StoppedByYou: true}, {Name: "shop-db-1", Project: "shop", StoppedByYou: true}, {Name: "solo", Running: true, Restarts: 1}}
	_ = j.containers(again, "", now.Add(3*time.Minute))
	// Während eines Backups startet HostDeck selbst.
	_ = j.containers(up, "Backup", now.Add(4*time.Minute))
	if n := len(j.Since(time.Time{})); n != 2 {
		t.Fatalf("danach %d Ereignisse, erwartet 2: %+v", n, j.Since(time.Time{}))
	}
	// Bleibt über einen Neustart des Dienstes erhalten.
	j2 := &Journal{File: j.File, Now: j.Now}
	if n := len(j2.Since(time.Time{})); n != 2 {
		t.Fatalf("nach dem Laden %d Ereignisse", n)
	}
	if fi, err := os.Stat(j.File); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("Datei: %v %v", fi, err)
	}
}

func TestJournalCertAndSpike(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	j := &Journal{Now: func() time.Time { return now }}
	a := now.Add(20 * 24 * time.Hour)
	_ = j.sites([]SiteCheck{{Host: "a.test", CertUntil: &a}}, now)
	_ = j.sites([]SiteCheck{{Host: "a.test", CertUntil: &a}}, now)
	b := now.Add(90 * 24 * time.Hour)
	_ = j.sites([]SiteCheck{{Host: "a.test", CertUntil: &b}}, now)
	got := j.Since(time.Time{})
	if len(got) != 1 || got[0].Kind != HapCert || got[0].Host != "a.test" {
		t.Fatalf("Zertifikat: %+v", got)
	}

	lim := DefaultLimits
	info := Info{Containers: []ContainerInfo{{Name: "spiel-1", Project: "spiel"}}}
	hi := Frame{Values: map[string]float64{KeyCPU: 96, "p/spiel/cpu": 300, "c/spiel-1/cpu": 300, "c/frei/cpu": 20}}
	lo := Frame{Values: map[string]float64{KeyCPU: 10}}
	t0 := now.Add(time.Hour)
	_ = j.frame(hi, info, lim, t0)
	_ = j.frame(hi, info, lim, t0.Add(10*time.Second))
	_ = j.frame(lo, info, lim, t0.Add(40*time.Second))
	got = j.Since(time.Time{})
	if len(got) != 2 || got[0].Kind != HapSpike {
		t.Fatalf("Spitze: %+v", got)
	}
	sp := got[0]
	if sp.Value != 96 || sp.Project != "spiel" || !sp.At.Equal(t0) || !strings.Contains(sp.Title, "96 %") || !strings.Contains(sp.Title, "40 Sekunden") {
		t.Errorf("Spitze: %+v", sp)
	}
	// Gleich die nächste: zählt nicht eigens.
	_ = j.frame(hi, info, lim, t0.Add(time.Minute))
	_ = j.frame(lo, info, lim, t0.Add(2*time.Minute))
	if n := len(j.Since(time.Time{})); n != 2 {
		t.Errorf("zweite Spitze in 30 Minuten eingetragen: %d", n)
	}
	// Hält sie so lange wie die Regel wartet, warnt die Regel: keine Spitze.
	t1 := t0.Add(2 * time.Hour)
	_ = j.frame(hi, info, lim, t1)
	_ = j.frame(lo, info, lim, t1.Add(lim.CPUHold))
	if n := len(j.Since(time.Time{})); n != 2 {
		t.Errorf("lange Last als Spitze eingetragen: %d", n)
	}
}

func TestJournalRulesAndUpdate(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	j := &Journal{Now: func() time.Time { return now }}
	_ = j.rule(Event{At: now, Kind: EvStart, Key: "seite:a.test", Level: LevelErr, Title: "a.test antwortet nicht"})
	_ = j.rule(Event{At: now, Kind: EvRemind, Key: "seite:a.test", Level: LevelErr, Title: "a.test antwortet nicht"})
	_ = j.rule(Event{At: now, Kind: EvClear, Key: "seite:a.test", Level: LevelErr, Title: "a.test antwortet wieder"})
	_ = j.rule(Event{At: now, Kind: EvStart, Key: "aus:shop-web-1", Level: LevelErr, Title: "shop-web-1 ist unerwartet aus"})
	got := j.Since(time.Time{})
	if len(got) != 3 {
		t.Fatalf("Regeln: %+v", got)
	}
	kinds := map[string]string{}
	for _, h := range got {
		kinds[h.Title] = h.Kind + "/" + h.Level
	}
	if kinds["a.test antwortet wieder"] != "domain/ok" || kinds["a.test antwortet nicht"] != "domain/err" || kinds["shop-web-1 ist unerwartet aus"] != "projekt/err" {
		t.Errorf("Arten: %v", kinds)
	}
	_ = j.Started("v1")
	_ = j.Started("v1")
	if n := len(j.Since(time.Time{})); n != 3 {
		t.Errorf("erster Start erzeugt ein Ereignis: %d", n)
	}
	_ = j.Started("v2")
	if got := j.Since(time.Time{}); len(got) != 4 || got[0].Kind != HapUpdate {
		t.Errorf("Update: %+v", got)
	}
	// Alt wird gekürzt.
	now = now.Add(31 * 24 * time.Hour)
	_ = j.Add(Happening{Kind: HapBackup, Title: "Backup"})
	if got := j.Since(time.Time{}); len(got) != 1 {
		t.Errorf("nach 31 Tagen: %+v", got)
	}
	var nilJ *Journal
	if err := nilJ.Add(Happening{}); err != nil || len(nilJ.Since(time.Time{})) != 0 {
		t.Error("nil-Journal")
	}
}
