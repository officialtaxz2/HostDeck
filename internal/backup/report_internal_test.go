package backup

import (
	"strings"
	"testing"
)

// Früherer Fund: Dateien, die restic nicht lesen konnte,
// gehören ins Urteil.
func TestUrteilMitUnlesbarenDateien(t *testing.T) {
	res := &Result{Steps: []StepResult{
		{Phase: PhasePre, Unreadable: []string{"/vorlauf/egal"}},
		{Phase: PhaseFinal, Unreadable: []string{"/opt/app/geheim.db", "/opt/app/x"}},
	}}
	r := &Report{Result: res, Valid: true}
	if n, ex := r.Unreadable(); n != 2 || ex != "/opt/app/geheim.db" {
		t.Errorf("Unreadable: %d %q", n, ex)
	}
	if v := r.verdict(); !strings.HasPrefix(v, "GÜLTIG, aber 2 Dateien waren nicht lesbar") {
		t.Errorf("Urteil: %s", v)
	}
	res.Final, r.Valid = true, false
	if v := r.verdict(); !strings.HasPrefix(v, "UNGÜLTIG für den Umzug: 2 Dateien") {
		t.Errorf("Urteil im letzten Lauf: %s", v)
	}
}
