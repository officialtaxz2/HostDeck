//go:build unix

package lauf

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Eine Sperrdatei, die andere lesen können, ersetzt HostDeck. Wer die alte offen
// hält und sperrt, blockiert damit keinen Lauf.
func TestFremdeSperrdateiWirdErsetzt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lockFile)
	old, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	os.Chmod(path, 0o644)
	if err := syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	l, err := TakeIn(dir, "Backup")
	if err != nil {
		t.Fatalf("alte Sperrdatei blockiert: %v", err)
	}
	defer l.Release()
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("Rechte: %v %v", fi.Mode(), err)
	}
	if _, err := TakeIn(dir, "Update"); err == nil {
		t.Fatal("zweiter Lauf trotz Sperre")
	}
	// Ein Symlink an der Stelle wird nicht verfolgt.
	l.Release()
	target := filepath.Join(dir, "ziel")
	os.WriteFile(target, []byte("bleibt"), 0o644)
	os.Remove(path)
	os.Symlink(target, path)
	l2, err := TakeIn(dir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	l2.Release()
	if b, _ := os.ReadFile(target); string(b) != "bleibt" {
		t.Fatal("Ziel des Symlinks verändert")
	}
	if fi, _ := os.Lstat(path); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("Symlink noch da")
	}
}
