package dockerapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMissing(t *testing.T) {
	root := t.TempDir()
	old := markerRoot
	markerRoot = root
	defer func() { markerRoot = old }()
	_, err := New(filepath.Join(root, "docker.sock")).Version(context.Background())
	if !Missing(err) {
		t.Fatalf("ohne Docker: %v", err)
	}
	if Missing(errors.New("connection refused")) || Missing(nil) {
		t.Error("andere Fehler gelten als fehlendes Docker")
	}
	os.MkdirAll(filepath.Join(root, "var/lib/docker"), 0o700)
	if Missing(err) {
		t.Error("installiertes, gestopptes Docker gilt als fehlend")
	}
}
