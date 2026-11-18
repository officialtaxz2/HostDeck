package web

import (
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Ein erledigter Einmal-Job (Code 0, keine Neustart-Regel) zählt nicht als
// gestoppter Container; ein abgestürzter oder ein Dienst mit Neustart-Regel schon.
func TestErledigteJobsZaehlenNicht(t *testing.T) {
	inv := &inventory.Inventory{Containers: []inventory.Container{
		{Key: "p/web", State: "running", Running: true, RestartPolicy: "unless-stopped"},
		{Key: "p/cleanup", State: "exited", ExitCode: 0, RestartPolicy: "no"},
		{Key: "p/migrate", State: "exited", ExitCode: 0, RestartPolicy: "on-failure"},
		{Key: "p/kaputt", State: "exited", ExitCode: 1, RestartPolicy: "no"},
		{Key: "p/dienst", State: "exited", ExitCode: 0, RestartPolicy: "unless-stopped"},
		{Key: "p/neu", State: "created", RestartPolicy: "no"},
	}}
	keys := []string{"p/web", "p/cleanup", "p/migrate", "p/kaputt", "p/dienst", "p/neu", "p/fehlt"}
	if n := finishedJobs(inv, keys); n != 2 {
		t.Errorf("%d erledigte Jobs, erwartet 2 (cleanup, migrate)", n)
	}
}
