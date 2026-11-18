package restore

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
)

// Nach dem Zurückspielen kennt der neue Server, was du auf dem alten im
// Leitstand gestoppt hattest; was hier schon stand, bleibt.
func TestGestopptKommtMit(t *testing.T) {
	file := filepath.Join(t.TempDir(), "gestoppt.json")
	here := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	old := time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC)
	if err := betrieb.SaveStopped(file, map[string]time.Time{"hier-1": here}); err != nil {
		t.Fatal(err)
	}
	j := &Job{UserStoppedFile: file}
	if err := j.keepStopped(&Plan{UserStopped: map[string]time.Time{"pensum-app-1": old}}); err != nil {
		t.Fatal(err)
	}
	m := betrieb.LoadStopped(file)
	if len(m) != 2 || !m["hier-1"].Equal(here) || !m["pensum-app-1"].Equal(old) {
		t.Errorf("gestoppt.json: %v", m)
	}
	// Ohne Datei oder ohne Liste: nichts.
	if err := (&Job{}).keepStopped(&Plan{UserStopped: map[string]time.Time{"x": old}}); err != nil {
		t.Error(err)
	}
}
