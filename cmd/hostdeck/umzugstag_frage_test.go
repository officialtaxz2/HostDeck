package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/backup"
)

type askRecorder struct{ asked []string }

func (a *askRecorder) Ask(_ context.Context, q backup.Question) (string, error) {
	a.asked = append(a.asked, q.Note)
	return "nein", nil
}

// Labor S4: Am Umzugstag fragt HostDeck nicht ein zweites Mal „Jetzt
// zurückspielen?“; andere Fragen gehen weiter an dich.
func TestUmzugstagFragtNichtNochmal(t *testing.T) {
	rec := &askRecorder{}
	var out bytes.Buffer
	c := confirmedRestore{rec, &out}
	ans, err := c.Ask(context.Background(), backup.Question{Note: "Jetzt auf den neuen Server (Rechnername neu) zurückspielen? Der Server …",
		Choices: []backup.Choice{{Label: "Jetzt zurückspielen", Answer: "ja"}, {Label: "Nicht starten", Answer: "nein"}}})
	if err != nil || ans != "ja" || len(rec.asked) != 0 || out.Len() == 0 {
		t.Fatalf("zurückspielen: %q %v %v %q", ans, err, rec.asked, out.String())
	}
	ans, _ = c.Ask(context.Background(), backup.Question{Note: "Auf dem neuen Server liegt schon etwas. Trotzdem weiter?",
		Choices: []backup.Choice{{Label: "Überschreiben", Answer: "überschreiben"}, {Label: "Nicht starten", Answer: "nein"}}})
	if ans != "nein" || len(rec.asked) != 1 {
		t.Fatalf("überschreiben muss gefragt werden: %q %v", ans, rec.asked)
	}
}

// Labor S5: Nach einem Rückweg hat drüben schon ein Leitstand übernommen.
// Der Umzugstag merkt das vor dem letzten Lauf, nicht erst bei Schritt 2,
// wenn die Seiten hier schon aus sind.
func TestUmzugstagErkenntUebernommenenServer(t *testing.T) {
	if why := adoptedProblem(nil); why != "" {
		t.Fatalf("ohne Datei: %q", why)
	}
	if why := adoptedProblem([]byte(`{"run":"x","hostdeck":"v"}`)); why != "" {
		t.Fatalf("Probe-Kopie: %q", why)
	}
	if why := adoptedProblem([]byte("kaputt")); why != "" {
		t.Fatalf("unlesbar: %q", why)
	}
	why := adoptedProblem([]byte(`{"run":"x","uebernommen":"2026-11-14T09:37:00Z"}`))
	if !strings.Contains(why, "abgeschlossen") || !strings.Contains(why, "neu auf") {
		t.Fatalf("übernommen: %q", why)
	}
}
