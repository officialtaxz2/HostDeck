package dienst

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// Umzug als Modus: Solange ein Umzug ansteht, führt der Leitstand durch
// Auswahl, Backup und Umzug und erinnert an Offenes. Im Alltag danach
// stehen Betrieb und Pflege vorn; Hinweise zu Auswahl und Umzug zeigen
// nur ihre eigenen Seiten. Wer wieder umziehen will, schaltet den Umzug
// auf der Seite Umzug ein.
//
// Ohne Eintrag gilt: Hat dieser Leitstand nach einem Umzug übernommen
// (Arrived), ist Alltag; sonst steht ein Umzug an, denn dafür richtet man
// HostDeck meist ein.

// moveMode ist der Inhalt von modus.json.
type moveMode struct {
	Planned bool      `json:"umzug_geplant"`
	Since   time.Time `json:"seit"`
}

func (d *Dienst) loadMode() (moveMode, bool) {
	if d.ModeFile == "" {
		return moveMode{}, false
	}
	b, err := os.ReadFile(d.ModeFile)
	if err != nil {
		return moveMode{}, false
	}
	var m moveMode
	if json.Unmarshal(b, &m) != nil {
		return moveMode{}, false
	}
	return m, true
}

// everyday sagt, ob gerade kein Umzug ansteht. Eine Probe-Kopie und ein
// abgelöster Server sind mitten im Umzug.
func (d *Dienst) everyday() bool {
	if d.ModeFile == "" {
		return false
	}
	if d.Copy != nil && d.Copy() != nil {
		return false
	}
	if d.Replaced != nil && d.Replaced() != nil {
		return false
	}
	if m, ok := d.loadMode(); ok {
		return !m.Planned
	}
	return d.Arrived != nil && d.Arrived()
}

type modeRequest struct {
	Planned bool `json:"geplant"`
	// Anyway: trotz einer Probe-Kopie, die auf einem anderen Server noch
	// läuft, auf Alltag schalten.
	Anyway bool `json:"trotzdem,omitempty"`
}

// ProbeCopy ist eine Probe-Kopie, die ein Probe-Umzug von hier auf einem
// anderen Server angelegt hat (probe-ziel.json im Ordner umzug). Sie
// bleibt, bis sie entfernt ist oder der Umzugstag sie ersetzt.
type ProbeCopy struct {
	Target string    `json:"ziel"` // benutzer@server
	Host   string    `json:"server"`
	Since  time.Time `json:"seit"`
	Halted bool      `json:"angehalten,omitempty"`
}

func (d *Dienst) probeCopy() *ProbeCopy {
	if d.ProbeFile == "" {
		return nil
	}
	b, err := os.ReadFile(d.ProbeFile)
	if err != nil {
		return nil
	}
	var p ProbeCopy
	if json.Unmarshal(b, &p) != nil || p.Host == "" {
		return nil
	}
	return &p
}

// noteProbe hält nach einem Lauf fest, ob drüben eine Probe-Kopie steht.
func (d *Dienst) noteProbe(kind MoveKind, t MoveTarget) {
	if d.ProbeFile == "" {
		return
	}
	switch kind {
	case MoveRun:
		p := ProbeCopy{Target: t.String(), Host: t.Host, Since: d.now().UTC()}
		b, _ := json.MarshalIndent(p, "", "  ")
		if os.MkdirAll(filepath.Dir(d.ProbeFile), 0o700) == nil {
			_ = outdir.WritePrivate(d.ProbeFile, append(b, '\n'))
		}
	case MoveHalt:
		if p := d.probeCopy(); p != nil && p.Host == t.Host {
			p.Halted = true
			b, _ := json.MarshalIndent(p, "", "  ")
			_ = outdir.WritePrivate(d.ProbeFile, append(b, '\n'))
		}
	case MoveRemove, MoveDay:
		_ = os.Remove(d.ProbeFile)
	}
}

func (d *Dienst) handleMode(w http.ResponseWriter, r *http.Request) {
	var req modeRequest
	if !decode(w, r, &req) {
		return
	}
	if d.ModeFile == "" {
		writeJSON(w, http.StatusConflict, apiError{Error: "Das geht hier nicht."})
		return
	}
	if why := d.copyBlocked("Umzüge"); why != "" {
		writeJSON(w, http.StatusConflict, apiError{Error: why})
		return
	}
	if !req.Planned {
		if why := d.modeBlocked(r.Context(), req.Anyway); why != "" {
			writeJSON(w, http.StatusConflict, apiError{Error: why})
			return
		}
	}
	// Ein neu geplanter Umzug beginnt den Lotsen von vorn.
	if was, ok := d.loadMode(); req.Planned && (!ok || !was.Planned) && d.everyday() {
		d.resetLotse()
	}
	b, _ := json.MarshalIndent(moveMode{Planned: req.Planned, Since: d.now().UTC()}, "", "  ")
	err := os.MkdirAll(filepath.Dir(d.ModeFile), 0o700)
	if err == nil {
		err = outdir.WritePrivate(d.ModeFile, append(b, '\n'))
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	gate, addr := visitor(r)
	if req.Planned {
		d.keepFetched()
	}
	text := "Kein Umzug geplant: Der Leitstand zeigt den Betrieb; Hinweise zu Auswahl und Umzug stehen nur noch auf ihren Seiten"
	if req.Planned {
		text = "Umzug geplant: Der Leitstand erinnert wieder an Auswahl, Backup und Umzug, und Backups bleiben bis zum Umzug auf dem Server"
	}
	d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: text, Addr: addr, Gate: gate})
	if !req.Planned {
		d.armFetched(addr, gate)
	}
	writeJSON(w, http.StatusOK, d.Status())
}

// modeBlocked sagt, warum gerade nicht auf Alltag umgeschaltet werden
// kann; leer: geht. Mitten im Umzug nie; eine laufende Probe-Kopie auf
// einem anderen Server nur mit anyway, damit sie nicht aus dem Blick gerät.
func (d *Dienst) modeBlocked(ctx context.Context, anyway bool) string {
	if i, _ := lauf.CurrentIn(d.RunDir); i != nil && moveRunning(i.What) {
		return "Gerade läuft „" + i.What + "“. Umschalten geht, wenn es fertig ist."
	}
	if m, ok := d.days(); ok {
		if st := m.DayState(ctx); st.Day.Active() {
			return "Der Umzugstag ist nicht abgeschlossen. Erst abschließen oder den Rückweg nehmen."
		}
	}
	if p := d.probeCopy(); p != nil && !p.Halted && !anyway {
		return "Auf " + p.Host + " läuft noch die Probe-Kopie vom Probe-Umzug; ihre Projekte laufen dort doppelt. Erst anhalten oder entfernen (Seite Umzug, unten bei „Neuer Server“)."
	}
	return ""
}

func moveRunning(what string) bool {
	for _, p := range []string{"Probe-Umzug", "Probelauf Umzug", "Probe-Kopie", "Umzugstag", "Rückweg"} {
		if strings.HasPrefix(what, p) {
			return true
		}
	}
	return false
}
