package report

import (
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Als Dienst eingerichtet gibt es kein „hostdeck serve“; die Auswahl
// geschieht im Leitstand.
func TestNaechsterSchritt(t *testing.T) {
	for _, leitstand := range []bool{false, true} {
		var b strings.Builder
		manifestSection(&b, Input{Inv: &inventory.Inventory{}, State: ManifestCreated, Leitstand: leitstand})
		serve := strings.Contains(b.String(), "hostdeck serve")
		if serve == leitstand || strings.Contains(b.String(), "Leitstand") != leitstand {
			t.Fatalf("Leitstand=%v: %q", leitstand, b.String())
		}
	}
}
