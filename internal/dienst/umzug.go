package dienst

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/anmeldung"
	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

// Probe-Umzug im Leitstand: Der Dienst verbindet sich per SSH mit einem
// anderen Server, bringt HostDeck dorthin und spielt dort das letzte gültige
// Backup von diesem Server ein. Die eigentliche Arbeit macht Moves; der
// Dienst sorgt für Anmeldung, Bestätigung, Laufsperre, Fortschritt und
// Protokoll. Das Passwort des Zielservers lebt nur im Speicher dieses
// Laufs und steht in keinem Protokoll und keiner Antwort.

// MoveKind ist die Art des Laufs auf dem anderen Server.
type MoveKind string

const (
	MovePlan   MoveKind = "probelauf"       // nur zeigen, was passieren würde
	MoveRun    MoveKind = "probe-umzug"     // zurückspielen
	MoveHalt   MoveKind = "probe-anhalten"  // Probe-Kopie dort anhalten
	MoveRemove MoveKind = "probe-entfernen" // Probe-Kopie dort entfernen
)

// what ist der Name des Laufs für Sperre und Protokoll.
func (k MoveKind) what() string {
	switch k {
	case MovePlan:
		return "Probelauf Umzug"
	case MoveHalt:
		return "Probe-Kopie anhalten"
	case MoveRemove:
		return "Probe-Kopie entfernen"
	case MoveDay:
		return "Umzugstag"
	case MoveBack:
		return "Rückweg zum alten Server"
	}
	return "Probe-Umzug"
}

// Moves spielt das Backup auf einen anderen Server.
type Moves interface {
	// Run verbindet sich mit t und führt dort den Lauf kind aus. Ergebnis
	// wie restore.Job.Run.
	Run(ctx context.Context, t MoveTarget, kind MoveKind, in backup.Input, out io.Writer) int
	// State beschreibt, ob ein Probe-Umzug gehen kann.
	State(ctx context.Context) MoveState
	// BackupPasswordOK vergleicht pw mit dem Backup-Passwort dieses
	// Servers. Das Backup-Passwort geht beim Probe-Umzug an den Zielserver;
	// das Leitstand-Passwort allein darf dafür nicht reichen.
	BackupPasswordOK(pw string) (bool, error)
}

// MoveTarget ist der Zielserver.
type MoveTarget struct {
	Host     string `json:"server"`
	Port     int    `json:"port"`
	User     string `json:"benutzer"`
	Password string `json:"passwort"`
}

// String nennt das Ziel ohne Passwort.
func (t MoveTarget) String() string {
	h := t.Host
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	s := t.User + "@" + h
	if t.Port != 22 {
		s += ":" + strconv.Itoa(t.Port)
	}
	return s
}

// MoveState ist der Stand für die Seite Umzug.
type MoveState struct {
	// Ready: Ein Probe-Umzug kann starten. Sonst sagt Problem, was fehlt.
	Ready   bool       `json:"bereit"`
	Problem string     `json:"problem,omitempty"`
	Backup  *time.Time `json:"backup,omitempty"` // neuester gültiger Lauf
	// Last ist das letzte Ziel (ohne Passwort), zum Vorbelegen.
	Last *MoveTarget `json:"zuletzt,omitempty"`
}

// MoveReply ist die Antwort auf GET /api/umzug.
type MoveReply struct {
	State MoveState  `json:"stand"`
	Job   *BackupJob `json:"auftrag,omitempty"`
	// Target ist das Ziel des laufenden oder letzten Auftrags.
	Target string `json:"ziel,omitempty"`
}

type moveRequest struct {
	Host     string `json:"server"`
	Port     int    `json:"port"`
	User     string `json:"benutzer"`
	Password string `json:"passwort"`
	// BackupPassword ist das Backup-Passwort, nur zum Vergleich. Es wird
	// nicht gespeichert und nicht weitergereicht.
	BackupPassword string `json:"backup_passwort"`
}

var (
	hostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	userName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// check prüft die Eingaben und setzt Vorgaben.
func (m *moveRequest) check() (MoveTarget, error) {
	t := MoveTarget{Host: strings.TrimSpace(m.Host), Port: m.Port, User: strings.TrimSpace(m.User), Password: m.Password}
	t.Host = strings.TrimSuffix(strings.TrimPrefix(t.Host, "["), "]")
	if t.Port == 0 {
		t.Port = 22
	}
	if t.User == "" {
		t.User = "root"
	}
	switch {
	case t.Host == "":
		return t, errors.New("Bitte die Adresse des neuen Servers eingeben, etwa 203.0.113.10.")
	case net.ParseIP(t.Host) == nil && !hostName.MatchString(t.Host):
		return t, fmt.Errorf("„%s“ ist keine gültige Server-Adresse.", clip(t.Host))
	case t.Port < 1 || t.Port > 65535:
		return t, errors.New("Der Port muss zwischen 1 und 65535 liegen.")
	case !userName.MatchString(t.User):
		return t, errors.New("Der Benutzername ist ungültig.")
	case t.Password == "":
		return t, errors.New("Bitte das Passwort des neuen Servers eingeben.")
	case len(t.Password) > 1024:
		return t, errors.New("Das Passwort ist zu lang.")
	}
	h := strings.ToLower(strings.TrimSuffix(t.Host, "."))
	if ip := net.ParseIP(t.Host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) || h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return t, errors.New("Das ist dieser Server selbst. Der Probe-Umzug geht auf einen anderen Server.")
	}
	return t, nil
}

func (d *Dienst) handleMove(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("ab"))
	d.mu.Lock()
	job, target := runSnapshot(d.mrun, from), d.mtarget
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, MoveReply{State: d.Moves.State(r.Context()), Job: job, Target: target})
}

func (d *Dienst) handleMovePlan(w http.ResponseWriter, r *http.Request) {
	d.startMove(w, r, MovePlan)
}

func (d *Dienst) handleMoveStart(w http.ResponseWriter, r *http.Request) {
	d.startMove(w, r, MoveRun)
}

// handleMoveHalt: POST /api/umzug/probe-anhalten (mit Passwort)
func (d *Dienst) handleMoveHalt(w http.ResponseWriter, r *http.Request) {
	d.startMove(w, r, MoveHalt)
}

// handleMoveRemove: POST /api/umzug/probe-entfernen (mit Passwort)
func (d *Dienst) handleMoveRemove(w http.ResponseWriter, r *http.Request) {
	d.startMove(w, r, MoveRemove)
}

// startMove startet einen Lauf auf dem anderen Server im Hintergrund.
// Anhalten und Entfernen brauchen kein Backup und kein Backup-Passwort:
// Sie schicken nichts vom Backup an den anderen Server.
func (d *Dienst) startMove(w http.ResponseWriter, r *http.Request, kind MoveKind) {
	restoring := kind == MovePlan || kind == MoveRun || kind == MoveDay
	gate, addr := visitor(r)
	var req moveRequest
	if !decode(w, r, &req) {
		return
	}
	if why := d.copyBlocked("Umzüge"); why != "" {
		writeJSON(w, http.StatusConflict, apiError{Error: why})
		return
	}
	if why := d.dayBlocked(r.Context(), kind); why != "" {
		writeJSON(w, http.StatusConflict, apiError{Error: why})
		return
	}
	t, err := req.check()
	req.Password = ""
	backupPW := req.BackupPassword
	req.BackupPassword = ""
	if err == nil && restoring && backupPW == "" {
		err = errors.New("Bitte dein Backup-Passwort eingeben. Es geht an den neuen Server; HostDeck vergleicht es hier nur und speichert es nicht.")
	}
	if err == nil && len(backupPW) > 1024 {
		err = errors.New("Das Backup-Passwort ist zu lang.")
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	what := kind.what()
	if m, ok := d.days(); ok && kind == MoveDay {
		if why := m.DayProblem(r.Context(), t.Host); why != "" {
			writeJSON(w, http.StatusConflict, apiError{Error: why})
			return
		}
	}
	if restoring {
		if st := d.Moves.State(r.Context()); !st.Ready {
			writeJSON(w, http.StatusConflict, apiError{Error: st.Problem})
			return
		}
		if !d.checkBackupPassword(w, backupPW, gate, addr, what) {
			return
		}
	}
	l, err := lauf.TakeIn(d.RunDir, what+" auf "+t.Host)
	var busy *lauf.BusyError
	switch {
	case errors.As(err, &busy):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	job := d.launchMove(t, kind, l, addr, gate)
	writeJSON(w, http.StatusAccepted, MoveReply{State: d.Moves.State(r.Context()), Job: job, Target: t.String()})
}

// launchMove führt den Lauf im Hintergrund aus; l ist die schon genommene
// Laufsperre.
func (d *Dienst) launchMove(t MoveTarget, kind MoveKind, l *lauf.Lock, addr, gate string) *BackupJob {
	plan := kind == MovePlan
	what := kind.what()
	where := ""
	if t.Host != "" {
		where = " auf " + t.Host
	}
	run := &backupRun{
		job:    &BackupJob{Running: true, Plan: plan, Kind: string(kind), Started: d.now(), Lines: []string{}},
		answer: make(chan string, 1),
		addr:   addr, gate: gate,
	}
	d.mu.Lock()
	base := d.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	run.cancel = cancel
	d.mrun = run
	if t.Host != "" {
		d.mtarget = t.String()
	}
	d.mu.Unlock()
	started := what + " gestartet"
	if t.Host != "" {
		started = what + " auf " + t.String() + " gestartet"
	}
	d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: started, Addr: addr, Gate: gate})
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		out := jobOutput{d: d, run: run}
		code := d.Moves.Run(ctx, t, kind, jobInput{d: d, run: run}, out)
		if kind == MoveRun || kind == MoveDay {
			d.forgetFetched() // der Lauf macht oder liest ein Backup
		}
		out.Write([]byte("\n"))
		// Erst die Sperre frei und das Ergebnis ins Protokoll, dann „fertig“
		// melden (wie beim Backup).
		l.Release()
		d.mu.Lock()
		run.closing = true
		run.job.Question = nil
		cancelled := run.job.Cancelled
		d.mu.Unlock()
		var text string
		switch {
		case cancelled:
			text = what + where + " abgebrochen"
		case code == backup.ExitBlocked:
			text = what + where + " nicht gestartet: Vorprüfung, Verbindung oder nicht bestätigt, siehe Seite Umzug"
		case plan && code == backup.ExitOK:
			text = "Probelauf Umzug auf " + t.Host + " fertig: alles bereit"
		case kind == MoveHalt && code == backup.ExitOK:
			text = "Probe-Kopie auf " + t.Host + " angehalten"
		case kind == MoveRemove && code == backup.ExitOK:
			text = "Probe-Kopie auf " + t.Host + " entfernt"
		case kind == MoveDay && code == backup.ExitOK:
			text = "Umzugstag: alles auf " + t.Host + ", wartet auf die DNS-Umstellung"
		case kind == MoveBack && code == backup.ExitOK:
			text = "Rückweg: Projekte laufen wieder auf diesem Server"
		case code == backup.ExitOK:
			text = "Probe-Umzug auf " + t.Host + " fertig: alles zurückgespielt und geprüft"
		default:
			text = what + where + " mit Fehlern beendet, siehe Seite Umzug"
		}
		if code == backup.ExitOK && !cancelled {
			d.noteProbe(kind, t)
			d.lotseRun(kind, t)
		}
		d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: text, Addr: addr, Gate: gate})
		now := d.now()
		d.mu.Lock()
		run.job.Running = false
		run.job.Done = &now
		run.job.Code = code
		d.mu.Unlock()
	}()
	d.mu.Lock()
	job := runSnapshot(run, 0)
	d.mu.Unlock()
	return job
}

// backupKey ist der Eintrag der Sperre für das Backup-Passwort: Er gilt
// für alle Adressen zusammen, fünf Fehlversuche sperren den Probe-Umzug
// für eine Viertelstunde.
const backupKey = "Backup-Passwort (Probe-Umzug)"

// checkBackupPassword prüft das Backup-Passwort samt Sperre. Bei false hat
// es die Antwort schon geschrieben.
func (d *Dienst) checkBackupPassword(w http.ResponseWriter, pw, gate, addr, what string) bool {
	if wait := d.Lockout.Begin(backupKey, false); wait > 0 {
		e := &web.LockedError{Wait: wait}
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Zu oft ein falsches Backup-Passwort. " + e.Error(), "warten": int(math.Ceil(wait.Seconds()))})
		return false
	}
	ok, err := d.Moves.BackupPasswordOK(pw)
	if err != nil {
		d.Lockout.Cancel(backupKey, false)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return false
	}
	locked := d.Lockout.End(backupKey, false, ok)
	if ok {
		return true
	}
	d.note(protokoll.Entry{Kind: protokoll.Fehlversuch, Text: "Falsches Backup-Passwort (" + what + ")", Addr: addr, Gate: gate})
	if locked {
		d.note(protokoll.Entry{Kind: protokoll.Gesperrt, Text: fmt.Sprintf("Probe-Umzug für %d Minuten gesperrt nach %d falschen Backup-Passwörtern", int(anmeldung.LockFor.Minutes()), anmeldung.MaxFails), Addr: addr, Gate: gate})
	}
	writeJSON(w, http.StatusForbidden, apiError{Error: "Das Backup-Passwort stimmt nicht."})
	return false
}

func (d *Dienst) handleMoveAnswer(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	run := d.mrun
	d.mu.Unlock()
	d.answerRun(w, r, run)
}

func (d *Dienst) handleMoveCancel(w http.ResponseWriter, r *http.Request) {
	gate, addr := visitor(r)
	d.mu.Lock()
	run := d.mrun
	ok := run != nil && run.job.Running && !run.closing
	kind := MoveRun
	if ok {
		run.job.Cancelled = true
		kind = MoveKind(run.job.Kind)
	}
	d.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusConflict, apiError{Error: "Es läuft gerade nichts auf dem neuen Server."})
		return
	}
	d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: "Abbrechen angefordert: " + kind.what(), Addr: addr, Gate: gate})
	run.cancel()
	w.WriteHeader(http.StatusNoContent)
}
