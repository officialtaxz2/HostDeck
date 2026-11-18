package lauf

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNurEinLaufZurZeit(t *testing.T) {
	dir := t.TempDir()
	if i, err := CurrentIn(dir); err != nil || i != nil {
		t.Fatalf("ohne Sperre: %v %v", i, err)
	}
	l, err := TakeIn(dir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	i, err := CurrentIn(dir)
	if err != nil || i == nil || i.What != "Backup" {
		t.Fatalf("Lauf nicht erkannt: %+v %v", i, err)
	}
	_, err = TakeIn(dir, "Bestandsaufnahme")
	var busy *BusyError
	if !errors.As(err, &busy) || busy.Info == nil || busy.Info.What != "Backup" {
		t.Fatalf("zweiter Lauf nicht abgewiesen: %v", err)
	}
	if !strings.Contains(err.Error(), "Backup seit") {
		t.Errorf("Meldung: %v", err)
	}
	l.Release()
	l.Release() // zweimal schadet nicht
	if i, _ := CurrentIn(dir); i != nil {
		t.Fatalf("nach Release noch belegt: %+v", i)
	}
	l2, err := TakeIn(dir, "Bestandsaufnahme")
	if err != nil {
		t.Fatal(err)
	}
	l2.Release()
}

// Ein anderer Prozess mit flock sieht die Sperre auch.
func TestSperreGiltFuerAndereProzesse(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock fehlt")
	}
	dir := t.TempDir()
	l, err := TakeIn(dir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if err := exec.Command("flock", "-n", dir+"/lauf.lock", "true").Run(); err == nil {
		t.Fatal("anderer Prozess bekam die Sperre")
	}
}

// Eine Abfrage von Current hält die Sperre kurz. Ein Lauf, der genau dann
// startet, darf deshalb nicht abgewiesen werden.
func TestKurzeAbfrageSperrtKeinenLauf(t *testing.T) {
	dir := t.TempDir()
	l, err := TakeIn(dir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	// Sperre halten wie Current: ohne lauf.json.
	f, err := os.Open(filepath.Join(dir, lockFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := tryLock(f); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(3 * busyWait)
		_ = unlock(f)
	}()
	l, err = TakeIn(dir, "Bestandsaufnahme")
	<-done
	f.Close()
	if err != nil {
		t.Fatalf("Lauf abgewiesen, obwohl nur kurz abgefragt wurde: %v", err)
	}
	l.Release()
}
