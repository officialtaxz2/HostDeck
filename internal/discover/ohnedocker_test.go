package discover_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/fixture"
)

// Ein Server ohne Docker bekommt trotzdem eine Bestandsaufnahme (Host,
// Ports, Caddy, Cron …), nur ohne Container.
func TestOhneDocker(t *testing.T) {
	s := fixture.Beispiel(t, 1)
	os.RemoveAll(filepath.Join(s.Root, "var/lib/docker"))
	os.RemoveAll(filepath.Join(s.Root, "etc/docker"))
	d := s.Deps()
	d.Docker = dockerapi.New(filepath.Join(t.TempDir(), "docker.sock"))
	inv, err := discover.Run(context.Background(), d, s.Options())
	if err != nil {
		t.Fatal(err)
	}
	if !inv.Docker.Missing || len(inv.Containers) > 0 || len(inv.Projects) > 0 {
		t.Errorf("Docker %+v, %d Container, %d Projekte", inv.Docker, len(inv.Containers), len(inv.Projects))
	}
	if inv.Host.Hostname == "" || inv.Caddy == nil || len(inv.Tasks) == 0 || len(inv.Settings) == 0 {
		t.Errorf("Host fehlt: %+v, Caddy %v, %d Aufgaben, %d Einstellungen", inv.Host, inv.Caddy != nil, len(inv.Tasks), len(inv.Settings))
	}

	// Docker ist installiert, läuft aber nicht: Fehler statt einer
	// Bestandsaufnahme, in der still alle Projekte fehlen.
	s2 := fixture.Beispiel(t, 1)
	d2 := s2.Deps()
	d2.Docker = dockerapi.New(filepath.Join(t.TempDir(), "docker.sock"))
	if _, err := discover.Run(context.Background(), d2, s2.Options()); err == nil || !strings.Contains(err.Error(), "Docker nicht erreichbar") {
		t.Errorf("Docker gestoppt: %v", err)
	}
}
