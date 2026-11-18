package anmeldung

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPasswortFestlegenUndPruefen(t *testing.T) {
	file := filepath.Join(t.TempDir(), "anmeldung.json")
	p := &Passwort{File: file}
	if p.IsSet() {
		t.Fatal("ohne Datei gesetzt")
	}
	if _, err := p.Verify(context.Background(), "egal"); err != ErrNotSet {
		t.Fatalf("ohne Passwort: %v", err)
	}
	if err := p.Set("zu-kurz"); err == nil {
		t.Fatal("kurzes Passwort angenommen")
	}
	const pw = "mein-leitstand-passwort"
	if err := p.Set(pw); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file)
	if strings.Contains(string(b), pw) {
		t.Fatal("Passwort im Klartext gespeichert")
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("Rechte %v", fi.Mode().Perm())
	}
	for in, want := range map[string]bool{pw: true, pw + " ": false, "Mein-leitstand-passwort": false, "": false} {
		if ok, err := p.Verify(context.Background(), in); err != nil || ok != want {
			t.Errorf("%q: %v %v", in, ok, err)
		}
	}
	// Neues Passwort ersetzt das alte.
	if err := p.Set("ein-ganz-anderes-passwort"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := p.Verify(context.Background(), pw); ok {
		t.Fatal("altes Passwort gilt noch")
	}
}

func TestPasswortRegeln(t *testing.T) {
	for pw, ok := range map[string]bool{
		"123456789012": true, "zwölf-zeichen": true, " leerzeichen-vorne": false, "steuer\tzeichen-x": false,
		strings.Repeat("x", 257): false, "elf-zeichen": false,
	} {
		if err := Validate(pw); (err == nil) != ok {
			t.Errorf("%q: %v", pw, err)
		}
	}
}

func TestErzeugtesPasswort(t *testing.T) {
	re := regexp.MustCompile(`^[a-hjkmnp-z2-9]{5}(-[a-hjkmnp-z2-9]{5}){3}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw, err := Generate()
		if err != nil || !re.MatchString(pw) || Validate(pw) != nil || seen[pw] {
			t.Fatalf("%q %v", pw, err)
		}
		seen[pw] = true
	}
}

// fail ist ein ganzer Fehlversuch: Begin, dann End.
func fail(t *testing.T, s *Sperre, addr string, global bool) bool {
	t.Helper()
	if w := s.Begin(addr, global); w != 0 {
		t.Fatalf("%s: Versuch abgewiesen (%v)", addr, w)
	}
	return s.End(addr, global, false)
}

// wait fragt, ob addr prüfen dürfte, ohne einen Versuch offen zu lassen.
func wait(s *Sperre, addr string, global bool) time.Duration {
	w := s.Begin(addr, global)
	if w == 0 {
		s.mu.Lock()
		s.pending[addr]--
		if global {
			s.pendingGlobal--
		}
		s.mu.Unlock()
	}
	return w
}

func TestSperreNachFuenfFehlversuchen(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sperre.json")
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := &Sperre{File: file, Now: clock}
	for i := 0; i < 4; i++ {
		if fail(t, s, "a", true) {
			t.Fatalf("Versuch %d schon gesperrt", i+1)
		}
	}
	if !fail(t, s, "a", true) {
		t.Fatal("fünfter Fehlversuch sperrt nicht")
	}
	if w := wait(s, "a", true); w != LockFor {
		t.Fatalf("Wartezeit %v", w)
	}
	if wait(s, "b", true) != 0 {
		t.Fatal("andere Adresse gesperrt")
	}
	// Übersteht einen Neustart.
	s2 := &Sperre{File: file, Now: clock}
	now = now.Add(10 * time.Minute)
	if w := wait(s2, "a", true); w != 5*time.Minute {
		t.Fatalf("nach Neustart: %v", w)
	}
	now = now.Add(6 * time.Minute)
	if wait(s2, "a", true) != 0 {
		t.Fatal("nach 16 Minuten noch gesperrt")
	}
}

func TestGleichzeitigeVersucheZaehlenMit(t *testing.T) {
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	s := &Sperre{Now: func() time.Time { return now }}
	fail(t, s, "a", true)
	// Vier laufen gleichzeitig, mit dem einen Fehlversuch sind es fünf.
	for i := 0; i < 4; i++ {
		if w := s.Begin("a", true); w != 0 {
			t.Fatalf("Versuch %d abgewiesen", i+2)
		}
	}
	if w := s.Begin("a", true); w == 0 {
		t.Fatal("sechster gleichzeitiger Versuch durchgelassen")
	}
	if wait(s, "b", true) != 0 {
		t.Fatal("andere Adresse gebremst")
	}
	locked := false
	for i := 0; i < 4; i++ {
		locked = s.End("a", true, false) || locked
	}
	if !locked || wait(s, "a", true) != LockFor {
		t.Fatal("nach fünf Fehlversuchen nicht gesperrt")
	}
	// Bremse für alle: laufende Versuche zählen mit.
	s = &Sperre{Now: func() time.Time { return now }}
	for i := 0; i < GlobalMax; i++ {
		if w := s.Begin(fmt.Sprintf("198.51.100.%d", i), true); w != 0 {
			t.Fatalf("Versuch %d abgewiesen", i)
		}
	}
	if wait(s, "203.0.113.1", true) != 0 {
		t.Fatal("erster gebremster Versuch abgewiesen")
	}
	if wait(s, "203.0.113.2", true) == 0 {
		t.Fatal("gleichzeitige Versuche umgehen die Bremse für alle")
	}
}

func TestErfolgHebtSperreNichtAufUndVertraut(t *testing.T) {
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	s := &Sperre{Now: func() time.Time { return now }}
	for i := 0; i < MaxFails; i++ {
		fail(t, s, "a", true)
	}
	s.mu.Lock()
	s.pending = map[string]int{"a": 1}
	s.mu.Unlock()
	s.End("a", true, true) // ein richtiges Passwort hebt eine laufende Sperre nicht auf
	if wait(s, "a", true) == 0 {
		t.Fatal("Sperre durch Erfolg aufgehoben")
	}
	// Von dieser Adresse kam das richtige Passwort: Die Bremse für alle
	// gilt für sie nicht mehr.
	if wait(s, "owner", true) != 0 || s.End("owner", true, true) {
		t.Fatal("Besitzer")
	}
	for i := 0; i < GlobalMax; i++ {
		s.End(fmt.Sprintf("198.51.100.%d", i), true, false)
	}
	if wait(s, "203.0.113.1", true) != 0 || wait(s, "203.0.113.2", true) == 0 {
		t.Fatal("Bremse für alle greift nicht")
	}
	for i := 0; i < 3; i++ {
		if wait(s, "owner", true) != 0 {
			t.Fatal("Besitzer von der Bremse für alle ausgesperrt")
		}
	}
}

func TestBremseFuerAlleNichtFuerNotausgang(t *testing.T) {
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	s := &Sperre{Now: func() time.Time { return now }}
	for i := 0; i < GlobalMax; i++ {
		fail(t, s, "198.51.100."+string(rune('a'+i%26))+string(rune('a'+i/26)), true)
	}
	if wait(s, "203.0.113.1", true) != 0 {
		t.Fatal("erster gebremster Versuch abgewiesen")
	}
	if wait(s, "203.0.113.2", true) == 0 {
		t.Fatal("Bremse für alle greift nicht")
	}
	if wait(s, "Notausgang", false) != 0 {
		t.Fatal("Notausgang gebremst")
	}
	now = now.Add(GlobalGap)
	if wait(s, "203.0.113.2", true) != 0 {
		t.Fatal("nach dem Abstand weiter gebremst")
	}
	now = now.Add(Window)
	wait(s, "x", true)
	if wait(s, "y", true) != 0 {
		t.Fatal("Bremse hört nicht auf")
	}
}
