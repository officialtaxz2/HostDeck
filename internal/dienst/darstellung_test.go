package dienst

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Darstellung: liegt beim Dienst, damit PC und Handy gleich aussehen; nur
// bekannte Werte, sonst bleibt die Datei, wie sie war.
func TestDarstellung(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hostdeck", "darstellung.json")
	r, sid := modeRig(t, func(d *Dienst) { d.LookFile = file })
	code, b := r.api(t, "GET", "/api/darstellung", sid, "")
	if code != 200 || !strings.Contains(b, `"darstellung":null`) || !strings.Contains(b, `"theme":"dark"`) {
		t.Fatalf("ohne Wahl: %d %s", code, b)
	}
	good := `{"darstellung":{"theme":"light","accent":"gruen","bg":"berge","tz":true,"leiste":"schmal","ecken":"weniger","dichte":"kompakt","bewegung":"aus"}}`
	if code, b := r.api(t, "POST", "/api/darstellung", sid, good); code != 200 {
		t.Fatalf("speichern: %d %s", code, b)
	}
	code, b = r.api(t, "GET", "/api/darstellung", sid, "")
	if code != 200 || !strings.Contains(b, `"accent":"gruen"`) || !strings.Contains(b, `"tz":true`) {
		t.Fatalf("nach dem Speichern: %d %s", code, b)
	}
	// Glas: Dateien ohne die Wahl bleiben gültig; „immer“ wird gespeichert.
	if !strings.Contains(b, `"glas":"geraet"`) {
		t.Errorf("Standard ohne glas fehlt: %s", b)
	}
	withGlass := strings.Replace(good, `"bewegung":"aus"}`, `"bewegung":"aus","glas":"an"}`, 1)
	if code, b := r.api(t, "POST", "/api/darstellung", sid, withGlass); code != 200 {
		t.Fatalf("Glas speichern: %d %s", code, b)
	}
	if _, b := r.api(t, "GET", "/api/darstellung", sid, ""); !strings.Contains(b, `"glas":"an"`) {
		t.Fatalf("Glas nicht gespeichert: %s", b)
	}
	if code, _ := r.api(t, "POST", "/api/darstellung", sid, strings.Replace(withGlass, `"glas":"an"`, `"glas":"halb"`, 1)); code != 400 {
		t.Fatalf("unbekanntes Glas angenommen: %d", code)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("Datei: %v %v", fi, err)
	}
	for _, bad := range []string{
		`{"darstellung":{"theme":"pink","accent":"gruen","bg":"berge","leiste":"breit","ecken":"rund","dichte":"bequem","bewegung":"an"}}`,
		`{"darstellung":{"theme":"dark"}}`,
		`{"darstellung":null}`,
		`{"farbe":"rot"}`,
	} {
		if code, _ := r.api(t, "POST", "/api/darstellung", sid, bad); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	if _, b := r.api(t, "GET", "/api/darstellung", sid, ""); !strings.Contains(b, `"accent":"gruen"`) {
		t.Fatalf("Fehlversuch hat überschrieben: %s", b)
	}
	// Ohne Anmeldung nichts.
	if code, _ := r.api(t, "GET", "/api/darstellung", "", ""); code != 401 {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
}
