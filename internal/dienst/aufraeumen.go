package dienst

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/officialtaxz2/hostdeck/internal/aufraeumen"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// Aufräumen: HostDeck listet auf, du hakst an. Was Daten
// enthält, kommt erst in den Papierkorb auf dem Server und lässt sich 30
// Tage lang zurückholen. Die Arbeit macht Cleanup; der Dienst sorgt für
// Anmeldung, Bestätigung, Laufsperre, Protokoll und den laufenden Auftrag.

// Cleanup räumt auf.
type Cleanup interface {
	Overview(ctx context.Context) aufraeumen.Overview
	Plan(ctx context.Context, keys []string) (aufraeumen.Plan, error)
	Run(ctx context.Context, keys []string, stand string, ev func(aufraeumen.Event)) (string, error)
	Restore(ctx context.Context, id string, ev func(aufraeumen.Event)) (string, error)
	Expire(ctx context.Context) (string, error)
}

// CleanTimeout begrenzt einen Lauf: Sichern und vollständiges Prüfen des
// Papierkorbs dauern bei vielen Gigabyte eine Weile.
const CleanTimeout = 3 * time.Hour

// CleanJob ist der laufende oder letzte Auftrag beim Aufräumen.
type CleanJob struct {
	Running bool       `json:"laeuft"`
	What    string     `json:"was"`
	Steps   []Step     `json:"schritte"`
	Result  string     `json:"ergebnis,omitempty"`
	Error   string     `json:"fehler,omitempty"`
	Started time.Time  `json:"start"`
	Done    *time.Time `json:"ende,omitempty"`
}

// CleanReply ist die Antwort auf GET /api/aufraeumen.
type CleanReply struct {
	State aufraeumen.Overview `json:"stand"`
	Job   *CleanJob           `json:"auftrag,omitempty"`
}

type cleanRequest struct {
	Keys  []string `json:"auswahl"`
	Stand string   `json:"stand"`
}

type restoreRequest struct {
	ID string `json:"id"`
}

func cleanStatus(err error) int {
	switch {
	case errors.Is(err, aufraeumen.ErrNothing):
		return http.StatusBadRequest
	case errors.Is(err, aufraeumen.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, aufraeumen.ErrBlocked), errors.Is(err, aufraeumen.ErrChanged):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (d *Dienst) cleanReply(ctx context.Context) CleanReply {
	rep := CleanReply{State: d.Cleanup.Overview(ctx)}
	d.mu.Lock()
	if d.cjob != nil {
		j := *d.cjob
		j.Steps = append(make([]Step, 0, len(d.cjob.Steps)), d.cjob.Steps...)
		rep.Job = &j
	}
	d.mu.Unlock()
	return rep
}

func (d *Dienst) handleCleanup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	writeJSON(w, http.StatusOK, d.cleanReply(ctx))
}

func (d *Dienst) handleCleanPlan(w http.ResponseWriter, r *http.Request) {
	var req cleanRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	p, err := d.Cleanup.Plan(ctx, req.Keys)
	if err != nil {
		writeJSON(w, cleanStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (d *Dienst) handleCleanStart(w http.ResponseWriter, r *http.Request) {
	var req cleanRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Stand == "" || len(req.Keys) == 0 {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Erst auswählen und die Vorschau öffnen."})
		return
	}
	details := make([]string, 0, len(req.Keys))
	for _, k := range req.Keys {
		details = append(details, clip(k))
	}
	d.startCleanJob(w, r, "Aufräumen", details, func(ctx context.Context, ev func(aufraeumen.Event)) (string, error) {
		return d.Cleanup.Run(ctx, req.Keys, req.Stand, ev)
	})
}

func (d *Dienst) handleCleanRestore(w http.ResponseWriter, r *http.Request) {
	var req restoreRequest
	if !decode(w, r, &req) {
		return
	}
	if req.ID == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Welcher Eintrag?"})
		return
	}
	name := req.ID
	for _, e := range d.Cleanup.Overview(r.Context()).Trash {
		if e.ID == req.ID {
			name = e.Name
		}
	}
	d.startCleanJob(w, r, "Aus dem Papierkorb zurückholen: "+name, nil, func(ctx context.Context, ev func(aufraeumen.Event)) (string, error) {
		return d.Cleanup.Restore(ctx, req.ID, ev)
	})
}

func (d *Dienst) startCleanJob(w http.ResponseWriter, r *http.Request, what string, details []string, run func(context.Context, func(aufraeumen.Event)) (string, error)) {
	d.startStepJob(w, r, &d.cjob, protokoll.Aufraeumen, "Aufräumen", what, details, run, func(ctx context.Context) any { return d.cleanReply(ctx) })
}

// startStepJob startet einen Auftrag mit Schritten unter der Laufsperre
// (Aufräumen, Löschen unter Speicherplatz): Protokoll vorher und nachher,
// danach eine Bestandsaufnahme, damit der Leitstand gleich sieht, was
// sich geändert hat. slot hält den laufenden oder letzten Auftrag.
func (d *Dienst) startStepJob(w http.ResponseWriter, r *http.Request, slot **CleanJob, kind, label, what string, details []string, run func(context.Context, func(aufraeumen.Event)) (string, error), reply func(context.Context) any) {
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
	gate, addr := visitor(r)
	job := &CleanJob{Running: true, What: what, Started: d.now(), Steps: []Step{}}
	d.mu.Lock()
	base := d.ctx
	if base == nil {
		base = context.Background()
	}
	*slot = job
	d.mu.Unlock()
	d.note(protokoll.Entry{Kind: kind, Text: clip(what + " gestartet"), Addr: addr, Gate: gate, Details: details})
	ev := func(e aufraeumen.Event) {
		s := Step{Name: e.Step, State: e.State, Detail: e.Detail}
		d.mu.Lock()
		defer d.mu.Unlock()
		for i := range job.Steps {
			if job.Steps[i].Name == s.Name {
				job.Steps[i] = s
				return
			}
		}
		job.Steps = append(job.Steps, s)
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(base, CleanTimeout)
		defer cancel()
		msg, err := run(ctx, ev)
		l.Release()
		if err == nil && d.Discover != nil {
			// Gelöschtes oder Zurückgeholtes soll der Leitstand gleich
			// sehen, nicht erst nach der nächtlichen Bestandsaufnahme.
			if derr := d.StartDiscover("nach dem " + label); derr != nil {
				d.logf("Bestandsaufnahme nach dem %s: %v", label, derr)
			}
		}
		// Erst ins Protokoll, dann „fertig“ melden.
		text := msg
		if err != nil {
			text = what + " ging nicht: " + err.Error()
			if msg != "" {
				text += " " + msg
			}
		}
		d.note(protokoll.Entry{Kind: kind, Text: clip(firstSentence(text)), Addr: addr, Gate: gate, Details: sentences(text)})
		d.logf("%s: %s", label, text)
		now := d.now()
		d.mu.Lock()
		job.Running, job.Done = false, &now
		if err != nil {
			job.Error = err.Error()
			if msg != "" {
				job.Error += " " + msg
			}
		} else {
			job.Result = msg
		}
		d.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, reply(r.Context()))
}

// firstSentence ist der erste Satz eines Ergebnisses, für die Zeile im
// Protokoll; der Rest steht in den Einzelheiten.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

func sentences(s string) []string {
	var out []string
	for _, p := range strings.SplitAfter(s, ". ") {
		p = strings.TrimSpace(p)
		for len(p) > 300 {
			_, n := utf8.DecodeLastRuneInString(p)
			p = p[:len(p)-n]
		}
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// expireWait ist der Abstand der Versuche, wenn nachts gerade ein anderer
// Lauf (meist die Bestandsaufnahme) die Sperre hält.
var expireWait = 10 * time.Minute

// expireTrash leert nachts, was länger als 30 Tage im Papierkorb liegt.
// Das hast du beim Wegräumen so gewählt. Hält ein anderer Lauf die
// Sperre, versucht es der Dienst bis zu zwölfmal; sonst in der nächsten
// Nacht.
func (d *Dienst) expireTrash(ctx context.Context) {
	var l *lauf.Lock
	for try := 0; ; try++ {
		var err error
		l, err = lauf.TakeIn(d.RunDir, "Papierkorb leeren")
		if err == nil {
			break
		}
		var busy *lauf.BusyError
		if !errors.As(err, &busy) || try >= 12 {
			d.logf("Papierkorb: nicht geleert (%v)", err)
			return
		}
		t := time.NewTimer(expireWait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
	defer l.Release()
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	msg, err := d.Cleanup.Expire(ctx)
	if err != nil {
		msg = "Papierkorb leeren ging nicht: " + err.Error()
	}
	if msg == "" {
		return
	}
	d.logf("%s", msg)
	d.note(protokoll.Entry{Kind: protokoll.Aufraeumen, Text: clip(firstSentence(msg)), Addr: AdminAddr, Details: sentences(msg)})
}
