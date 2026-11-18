package dienst

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

type fakeKeeper struct{ keys map[string]bool }

func (f *fakeKeeper) Accept(key string, on bool) (string, error) {
	switch key {
	case "platz:/":
		return "", ueberwachung.ErrNotAcceptable
	case "steal":
		f.keys[key] = on
		if on {
			return "Bleibt bewusst so: Der Hoster teilt den Prozessor knapp zu", nil
		}
		return "Warnt wieder: Der Hoster teilt den Prozessor knapp zu", nil
	}
	return "", ueberwachung.ErrNoAlert
}

type forgetHints struct {
	fakeHints
	forgot int
}

func (f *forgetHints) Forget() { f.forgot++ }

// „Bewusst so lassen“ für Warnungen und Ports: angemeldet, ohne Passwort,
// im Protokoll mit Adresse; „Braucht dich“ sammelt danach gleich neu.
func TestBewusstSoLassenImLeitstand(t *testing.T) {
	fk, fs, fh := &fakeKeeper{keys: map[string]bool{}}, &fakeSecurity{}, &forgetHints{}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Alerts, d.Security, d.Hints = fk, fs, fh })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "POST", "/api/ueberwachung/so-lassen", "", `{"schluessel":"steal"}`); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	if code, _ := r.api(t, "POST", "/api/ueberwachung/so-lassen", sid, `{"schluessel":"platz:/"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("Platte: %d", code)
	}
	if code, _ := r.api(t, "POST", "/api/ueberwachung/so-lassen", sid, `{"schluessel":"docker"}`); code != http.StatusNotFound {
		t.Errorf("keine laufende Warnung: %d", code)
	}
	if code, b := r.api(t, "POST", "/api/ueberwachung/so-lassen", sid, `{"schluessel":"steal"}`); code != 200 || !fk.keys["steal"] || !strings.Contains(b, "Bleibt bewusst so") {
		t.Fatalf("so lassen: %d %s", code, b)
	}
	if code, _ := r.api(t, "POST", "/api/ueberwachung/so-lassen", sid, `{"schluessel":"steal","zuruecknehmen":true}`); code != 200 || fk.keys["steal"] {
		t.Fatalf("zurück: %d", code)
	}
	if code, b := r.api(t, "POST", "/api/sicherheit/offen-lassen", sid, `{"schluessel":"tcp/3552"}`); code != 200 || !fs.keptOpen || !strings.Contains(b, "Bleibt bewusst offen") {
		t.Fatalf("Port: %d %s", code, b)
	}
	if code, _ := r.api(t, "POST", "/api/sicherheit/offen-lassen", sid, `{"schluessel":"tcp/1"}`); code != http.StatusNotFound {
		t.Errorf("unbekannter Port: %d", code)
	}
	if fh.forgot != 3 {
		t.Errorf("Hinweise neu gesammelt: %d", fh.forgot)
	}
	jr := journal(t, r)
	for _, want := range []string{"ueberwachung|Bleibt bewusst so: Der Hoster", "Warnt wieder:", "sicherheit|Bleibt bewusst offen: 3552/tcp (arcane).", "203.0.113.9"} {
		if !strings.Contains(jr, want) {
			t.Errorf("Protokoll ohne %q: %s", want, jr)
		}
	}
}
