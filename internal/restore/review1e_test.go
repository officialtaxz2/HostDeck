package restore

// Regressionstests zu früheren Funden. Rot heißt: Befund
// bestätigt; sie prüfen das gewünschte Verhalten.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/restic"
)

// Befund N3: Passt das mitgebrachte Passwort nicht, fehlt danach die
// bisherige Passwortdatei (sie liegt nur noch als .vorher-… daneben).
func TestReview1eFalschesMitgebrachtesPasswort(t *testing.T) {
	e := newJobEnv(t)
	if err := os.MkdirAll(filepath.Dir(e.job.PasswordFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.job.PasswordFile, []byte("eigenes-altes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.job.Password = "passt-nicht"
	if code := e.job.Run(context.Background()); code != ExitBlocked {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	b, err := os.ReadFile(e.job.PasswordFile)
	if err != nil || strings.TrimSpace(string(b)) != "eigenes-altes" {
		left, _ := filepath.Glob(e.job.PasswordFile + "*")
		t.Fatalf("bisherige Passwortdatei nicht zurück (%v); im Ordner: %v\n%s", err, left, e.out)
	}
}

// Befund M3: Beim zweiten Lauf entscheidet nur die Zeilenzahl. Ob die
// Datenbank wirklich den Stand aus dem Backup hat, weiß HostDeck nicht; die
// Meldung darf das nicht behaupten.
func TestReview1eZweitlaufNurZeilenzahl(t *testing.T) {
	c := NewDumpCounter("postgres")
	c.Write([]byte(testDump))
	e := newJobEnv(t, "richtig", "ja")
	// Gleich viele Zeilen, aber (gedacht) anderer Inhalt: etwa ein Bot hat
	// auf dem neuen Server Kontostände geändert, oder das Backup ist neuer.
	e.docker.preData = c.Counts()
	e.job.Run(context.Background())
	out := e.out.String()
	if strings.Contains(out, "wie im Backup; nichts eingespielt") || strings.Contains(out, "Stand aus dem Backup") {
		t.Fatalf("meldet gleichen Stand allein nach Zeilenzahl:\n%s", out)
	}
}

// Befund M4: Liegt ein ungültiger Lauf neben gültigen, steht er als
// neuester oben in der Auswahl, ohne Hinweis.
func TestReview1eUngueltigerLaufInDerAuswahl(t *testing.T) {
	e := newJobEnv(t, "richtig", "1", "ja")
	lauf := "/root/.hostdeck/laeufe/20261102-100000"
	var rf backup.RunFile
	json.Unmarshal([]byte(e.store.files["lauf1|/root/.hostdeck/laeufe/20261101-100000/lauf.json"]), &rf)
	rf.Run, rf.Valid = "20261102-100000", false
	rb, _ := json.Marshal(rf)
	e.store.files["lauf2|"+lauf+"/lauf.json"] = string(rb)
	e.store.files["lauf2|"+lauf+"/inventory.json"] = e.store.files["lauf1|/root/.hostdeck/laeufe/20261101-100000/inventory.json"]
	e.store.snaps = append(e.store.snaps, restic.Snapshot{ID: "lauf2", Short: "lauf2", Time: time.Date(2026, 11, 2, 10, 5, 0, 0, time.UTC), Paths: []string{lauf}, Host: "alt"})
	e.job.Run(context.Background())
	var pick *backup.Question
	for i, q := range e.input.questions {
		if strings.HasPrefix(q.Text, "Welchen Lauf") {
			pick = &e.input.questions[i]
		}
	}
	if pick == nil {
		t.Fatalf("keine Auswahl gefragt:\n%s", e.out)
	}
	first := pick.Choices[0]
	if !strings.Contains(strings.ToLower(first.Label+pick.Note), "ungültig") && !strings.Contains(strings.ToLower(first.Label+pick.Note), "nicht gültig") {
		t.Fatalf("ungültiger Lauf ohne Hinweis zuerst angeboten: %+v\n%s", pick.Choices, e.out)
	}
}
