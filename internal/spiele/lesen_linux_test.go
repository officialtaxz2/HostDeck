//go:build linux

package spiele

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
)

// Unter dem Datenordner folgt HostDeck keiner Verknüpfung, auch nicht
// in Zwischenordnern, und eine FIFO statt einer Datei hält den Suchlauf
// nicht an. Der Datenordner selbst darf hinter einer Verknüpfung liegen.
func TestDateienOhneVerknuepfungUndFIFO(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/etc/geheim/server.cfg", "hostname \"Geheim\"\n")
	write(t, root, "/srv/l4d/srcds_run", "#!/bin/sh\n")
	write(t, root, "/srv/l4d/left4dead2/gameinfo.txt", "\"GameInfo\" {}\n")
	if err := os.Symlink(filepath.Join(root, "/etc/geheim"), filepath.Join(root, "/srv/l4d/left4dead2/cfg")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "/srv/fifo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "/srv/fifo/server.properties"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, root, "/echt/mc/server.properties", "motd=Hinter Verknüpfung\n")
	if err := os.Symlink(filepath.Join(root, "/echt/mc"), filepath.Join(root, "/srv/mc")); err != nil {
		t.Fatal(err)
	}
	fl := &fakeList{list: []dockerapi.OpsContainer{
		{ID: "a", Names: []string{"/l4d"}, Image: "custom", State: "running", Mounts: []dockerapi.MountPoint{mount("/srv/l4d", "/data")}},
		{ID: "b", Names: []string{"/fifo"}, Image: "custom", State: "running", Mounts: []dockerapi.MountPoint{mount("/srv/fifo", "/data")}},
		{ID: "c", Names: []string{"/mc"}, Image: "custom", State: "running", Mounts: []dockerapi.MountPoint{mount("/srv/mc", "/data")}},
	}}
	s := &Service{Docker: fl, FS: hostfs.New(root)}
	done := make(chan struct{})
	go func() { s.Scan(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Suchlauf hängt an der FIFO")
	}
	got := map[string]Server{}
	for _, sv := range s.List() {
		got[sv.ID] = sv
	}
	if g, ok := got["l4d"]; !ok || g.Name == "Geheim" {
		t.Errorf("Source-Server: %+v (Name aus verknüpftem cfg/?)", g)
	}
	if _, ok := got["fifo"]; ok {
		t.Error("FIFO als server.properties gelesen")
	}
	if g, ok := got["mc"]; !ok || g.Name != "Hinter Verknüpfung" {
		t.Errorf("Datenordner hinter Verknüpfung: %+v", g)
	}
}
