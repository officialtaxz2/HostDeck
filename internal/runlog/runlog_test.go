package runlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListeHaeltDieNeuestenLaeufe(t *testing.T) {
	dir := t.TempDir()
	if l, err := Load(dir); err != nil || len(l) != 0 {
		t.Fatalf("ohne Datei: %v %v", l, err)
	}
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < Keep+5; i++ {
		e := Entry{Run: t0.Add(time.Duration(i) * time.Hour).Format("20060102-150405"), Started: t0.Add(time.Duration(i) * time.Hour), Valid: i%2 == 0}
		if err := Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != Keep {
		t.Fatalf("%d Einträge, erwartet %d", len(l), Keep)
	}
	if !l[0].Started.Equal(t0.Add(time.Duration(Keep+4) * time.Hour)) {
		t.Errorf("neuester zuerst erwartet, ist %v", l[0].Started)
	}
	// Derselbe Lauf noch einmal ersetzt den alten Eintrag.
	if err := Append(dir, Entry{Run: l[0].Run, Started: l[0].Started, Valid: true, Verdict: "GÜLTIG"}); err != nil {
		t.Fatal(err)
	}
	l2, _ := Load(dir)
	if len(l2) != Keep || l2[0].Verdict != "GÜLTIG" {
		t.Errorf("Lauf doppelt oder nicht ersetzt: %d, %q", len(l2), l2[0].Verdict)
	}
	if fi, _ := os.Stat(filepath.Join(dir, File)); fi.Mode().Perm() != 0o600 {
		t.Errorf("Rechte %v", fi.Mode().Perm())
	}
}

func TestKaputteListeWirdErsetzt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, File), []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("kaputte Liste ohne Fehler gelesen")
	}
	if err := Append(dir, Entry{Run: "a", Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if l, err := Load(dir); err != nil || len(l) != 1 {
		t.Errorf("nach Ersetzen: %v %v", l, err)
	}
}
