package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestServerBackupsRemoveLeertOrdner(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "backup")
	for name, body := range map[string]string{"config": "k", "data/ab/abc": "12345", "keys/x": "kk"} {
		p := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "cache", "abc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cache", "abc", "index"), []byte("1234567890"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &serverBackups{Repo: repo, State: state}
	freed, err := b.Remove(context.Background())
	if err != nil || freed != 18 {
		t.Fatalf("Remove: %d %v", freed, err)
	}
	if _, err := os.Stat(filepath.Join(state, "cache")); err == nil {
		t.Fatal("restic-Cache liegt noch")
	}
	es, err := os.ReadDir(repo)
	if err != nil || len(es) != 0 {
		t.Fatalf("Ordner nicht leer: %v %v", es, err)
	}
	if _, _, err := b.Archive(); err == nil {
		t.Fatal("Archiv aus leerem Ordner")
	}
	// Ohne Ordner ist nichts zu tun.
	if n, err := (&serverBackups{Repo: filepath.Join(repo, "fehlt")}).Remove(context.Background()); n != 0 || err != nil {
		t.Fatalf("ohne Ordner: %d %v", n, err)
	}
}
