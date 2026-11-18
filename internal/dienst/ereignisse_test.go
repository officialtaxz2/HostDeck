package dienst

import (
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

// Ereignisse: nur angemeldet, neueste zuerst, höchstens der gefragte
// Zeitraum.
func TestEreignisse(t *testing.T) {
	j := &ueberwachung.Journal{}
	r, sid := modeRig(t, func(d *Dienst) { d.Events = j; j.Now = d.now })
	now := j.Now()
	_ = j.Add(ueberwachung.Happening{At: now.Add(-3 * 24 * time.Hour), Kind: ueberwachung.HapBackup, Title: "alt"})
	_ = j.Add(ueberwachung.Happening{Kind: ueberwachung.HapProject, Title: "shop gestartet"})
	code, b := r.api(t, "GET", "/api/ereignisse?tage=1", sid, "")
	if code != 200 || !strings.Contains(b, "shop gestartet") || strings.Contains(b, `"alt"`) {
		t.Fatalf("1 Tag: %d %s", code, b)
	}
	code, b = r.api(t, "GET", "/api/ereignisse", sid, "")
	if code != 200 || strings.Index(b, "shop gestartet") > strings.Index(b, `"alt"`) {
		t.Fatalf("30 Tage: %d %s", code, b)
	}
	if code, _ := r.api(t, "GET", "/api/ereignisse", "", ""); code != 401 {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
}
