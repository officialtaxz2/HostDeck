package leistung

import (
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

// R3: Der Steal-Hinweis folgt der Grenze der Überwachung wie die Kachel,
// nicht einer eigenen Zahl.
func TestStealHinweisFolgtGrenze(t *testing.T) {
	s := &Service{}
	def := ueberwachung.DefaultLimits.StealWarn
	if n := s.stealNote(def); n != nil {
		t.Errorf("genau auf der Voreinstellung (%v %%) schon ein Hinweis: %v", def, n)
	}
	if n := s.stealNote(def + 1); len(n) != 1 {
		t.Errorf("über der Voreinstellung kein Hinweis")
	}
	if n := s.stealNote(12); def > 12 && n != nil {
		t.Errorf("12 %% unter der Grenze %v %% gibt einen Hinweis: %v", def, n)
	}
	lim := ueberwachung.DefaultLimits
	lim.StealWarn = 8
	s.Limits = func() ueberwachung.Limits { return lim }
	if n := s.stealNote(9); len(n) != 1 {
		t.Errorf("geänderte Grenze 8 %%: bei 9 %% kein Hinweis")
	}
	if n := s.stealNote(8); n != nil {
		t.Errorf("geänderte Grenze 8 %%: bei 8 %% schon ein Hinweis")
	}
}
