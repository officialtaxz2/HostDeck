package dienst

import (
	"context"
	"errors"
	"net/http"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/umzugstag"
)

// Der Umzugstag: Nach einer bestandenen Generalprobe (ein
// Probe-Umzug, bei dem alles klappte) zieht ein fester Ablauf alles auf den
// neuen Server: letzter Lauf, Probe-Kopie entfernen, zurückspielen,
// Leitstand, Server-Ebene, Seiten prüfen. Danach wartet er auf die
// DNS-Umstellung; zeigen die Domains auf den neuen Server, ist dieser hier
// abgelöst. Bis dahin startet der Rückweg alles hier wieder.

const (
	MoveDay  MoveKind = "umzugstag" // der ganze Ablauf
	MoveBack MoveKind = "rueckweg"  // Projekte hier wieder starten
)

// DayState ist die Antwort auf GET /api/umzug/umzugstag.
type DayState struct {
	Probe *umzugstag.Probe `json:"generalprobe,omitempty"`
	// Ready: Der Umzugstag kann auf das Ziel der Generalprobe starten.
	// Sonst sagt Problem, was fehlt.
	Ready   bool            `json:"bereit"`
	Problem string          `json:"problem,omitempty"`
	Day     *umzugstag.Day  `json:"umzugstag,omitempty"`
	Done    *umzugstag.Done `json:"abgeloest,omitempty"`
	// LeftOff: so viele Container hat der letzte Lauf hier aus gelassen.
	LeftOff int `json:"aus_gelassen"`
}

// MoveDays kann Moves zusätzlich: den Umzugstag.
type MoveDays interface {
	DayState(ctx context.Context) DayState
	// DayProblem sagt, warum der Umzugstag auf host nicht starten kann.
	DayProblem(ctx context.Context, host string) string
	// Finish schließt den Umzug von Hand ab: Dieser Server ist abgelöst.
	Finish(ctx context.Context) error
}

func (d *Dienst) days() (MoveDays, bool) {
	m, ok := d.Moves.(MoveDays)
	return m, ok
}

// dayBlocked sagt, warum eine Art von Lauf wegen des Umzugstags gerade
// nicht geht; leer: geht.
func (d *Dienst) dayBlocked(ctx context.Context, kind MoveKind) string {
	m, ok := d.days()
	if !ok {
		return ""
	}
	st := m.DayState(ctx)
	switch {
	case kind == MoveBack:
		if st.Done == nil && !st.Day.Active() && st.LeftOff == 0 {
			return "Hier ist nichts zurückzuholen: Die Projekte laufen."
		}
		return ""
	case st.Done != nil:
		return "Dieser Server ist " + st.Done.Text() + ". Umzüge gehen hier nicht mehr; der Rückweg steht unter Umzug."
	case kind == MoveDay && st.Day != nil && st.Day.Phase == umzugstag.PhaseDNS:
		return "Der Umzug ist schon drüben und wartet auf die DNS-Umstellung."
	case kind == MoveDay:
		return ""
	case st.Day.Active():
		return "Der Umzugstag ist nicht abgeschlossen: Die Projekte hier sind aus. Nimm den Rückweg oder starte den Umzugstag erneut."
	}
	return ""
}

// backupBlocked sagt, warum gerade kein Backup geht (Umzugstag); leer: geht.
func (d *Dienst) backupBlocked(ctx context.Context) string {
	m, ok := d.days()
	if !ok {
		return ""
	}
	st := m.DayState(ctx)
	switch {
	case st.Done != nil:
		return "Dieser Server ist " + st.Done.Text() + ". Backups macht jetzt der neue Server."
	case st.Day.Active():
		return "Der Umzugstag läuft: Die Projekte hier sind aus, ein Backup jetzt hätte nichts Neues."
	}
	return ""
}

// handleDay: GET /api/umzug/umzugstag
func (d *Dienst) handleDay(w http.ResponseWriter, r *http.Request) {
	m, ok := d.days()
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Den Umzugstag gibt es hier nicht."})
		return
	}
	writeJSON(w, http.StatusOK, m.DayState(r.Context()))
}

// handleDayStart: POST /api/umzug/umzugstag (mit Passwort), Eingaben wie
// beim Probe-Umzug.
func (d *Dienst) handleDayStart(w http.ResponseWriter, r *http.Request) {
	d.startMove(w, r, MoveDay)
}

// handleBack: POST /api/umzug/zurueck (mit Passwort): startet die Projekte
// hier wieder, die der letzte Lauf aus gelassen hat.
func (d *Dienst) handleBack(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.days(); !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Den Rückweg gibt es hier nicht."})
		return
	}
	gate, addr := visitor(r)
	if why := d.dayBlocked(r.Context(), MoveBack); why != "" {
		writeJSON(w, http.StatusConflict, apiError{Error: why})
		return
	}
	what := MoveBack.what()
	l, err := lauf.TakeIn(d.RunDir, what)
	var busy *lauf.BusyError
	switch {
	case errors.As(err, &busy):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	reply := d.launchMove(MoveTarget{}, MoveBack, l, addr, gate)
	writeJSON(w, http.StatusAccepted, MoveReply{State: d.Moves.State(r.Context()), Job: reply})
}

// handleFinish: POST /api/umzug/abschliessen (mit Passwort)
func (d *Dienst) handleFinish(w http.ResponseWriter, r *http.Request) {
	m, ok := d.days()
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Den Umzugstag gibt es hier nicht."})
		return
	}
	gate, addr := visitor(r)
	if err := m.Finish(r.Context()); err != nil {
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	}
	st := m.DayState(r.Context())
	text := "Umzug von Hand abgeschlossen"
	if st.Done != nil {
		text += ": dieser Server ist " + st.Done.Text()
	}
	d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: text, Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, st)
}

// handleArrive: POST /api/umzug/uebernehmen (mit Passwort), auf dem neuen
// Server: Die stille Kopie vom Umzugstag wird der echte Leitstand.
func (d *Dienst) handleArrive(w http.ResponseWriter, r *http.Request) {
	if d.Arrive == nil {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Das geht hier nicht."})
		return
	}
	gate, addr := visitor(r)
	if err := d.Arrive(); err != nil {
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	}
	d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: "Umzug abgeschlossen (von Hand): Dieser Leitstand ist jetzt der echte; Backups, Umzüge und Mitteilungen gehen hier", Addr: addr, Gate: gate})
	w.WriteHeader(http.StatusNoContent)
}
