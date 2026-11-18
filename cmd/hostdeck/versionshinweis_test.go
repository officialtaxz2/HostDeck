package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/neueversion"
)

// D5: Einrichten fragt einmal nach dem Hinweis auf neue Versionen; Enter
// heißt ja, eine Angabe auf der Befehlszeile ersetzt die Frage.
func TestEinrichtenFragtVersionshinweis(t *testing.T) {
	s := &neueversion.Service{File: filepath.Join(t.TempDir(), "versionshinweis.json"), Version: "1.0.0"}
	p := &scriptPrompt{answers: []string{"vielleicht", ""}}
	var out strings.Builder
	askReleaseHint(s, "", p, &out, io.Discard)
	if !s.Load().Enabled() || !strings.Contains(p.out.String(), "Eingespielt wird nie von selbst") || !strings.Contains(out.String(), "ist an") {
		t.Fatalf("Enter: %+v\n%s\n%s", s.Load(), p.out.String(), out.String())
	}
	// Schon gewählt: keine Frage mehr.
	p = &scriptPrompt{answers: []string{"n"}}
	askReleaseHint(s, "", p, io.Discard, io.Discard)
	if p.out.Len() != 0 || !s.Load().Enabled() {
		t.Fatalf("zweites Einrichten fragt: %q", p.out.String())
	}
	askReleaseHint(s, "aus", p, io.Discard, io.Discard)
	if s.Load().Enabled() || s.Load().On == nil {
		t.Fatalf("--versionshinweis aus: %+v", s.Load())
	}
	s2 := &neueversion.Service{File: filepath.Join(t.TempDir(), "v.json")}
	askReleaseHint(s2, "", &scriptPrompt{answers: []string{"nein"}}, io.Discard, io.Discard)
	if st := s2.Load(); st.On == nil || st.Enabled() {
		t.Fatalf("nein: %+v", st)
	}
}
