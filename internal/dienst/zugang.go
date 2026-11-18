package dienst

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// Zugang von außen: Der Dienst trägt den Leitstand bei Caddy ein
// (Freischalten) oder nimmt den Eintrag zurück. Die eigentliche Arbeit
// macht Access; der Dienst sorgt für Anmeldung, Bestätigung, Laufsperre,
// Fortschritt und Protokoll.

// Access schaltet den Leitstand über Caddy frei.
type Access interface {
	// State sagt, ob und wo der Leitstand von außen erreichbar ist.
	State(ctx context.Context) AccessState
	// Domain ist die freigeschaltete Adresse, ohne Caddy zu fragen.
	Domain() string
	// Check prüft eine Adresse (DNS, Caddy) und zeigt, was sich ändern würde.
	Check(ctx context.Context, domain string) (AccessPlan, error)
	// Publish schaltet frei. step meldet den Fortschritt.
	Publish(ctx context.Context, domain string, step func(Step)) error
	// Unpublish nimmt das Freischalten zurück.
	Unpublish(ctx context.Context, step func(Step)) error
}

// AccessState ist der Stand des Zugangs von außen.
type AccessState struct {
	Caddy   bool       `json:"caddy"`               // Freischalten geht hier
	Problem string     `json:"problem,omitempty"`   // warum nicht
	Domain  string     `json:"adresse,omitempty"`   // freigeschaltet unter
	Since   *time.Time `json:"seit,omitempty"`      // seit wann
	Suggest string     `json:"vorschlag,omitempty"` // Vorschlag für die Adresse
	Config  string     `json:"caddyfile,omitempty"` // Caddyfile, das HostDeck ändert
}

// AccessPlan zeigt, was das Freischalten ändert.
type AccessPlan struct {
	Domain string     `json:"adresse"`
	IPs    []string   `json:"ips"`     // dorthin zeigt die Adresse
	Files  []PlanFile `json:"dateien"` // Änderungen an Caddy
	Pages  int        `json:"seiten"`  // so viele Seiten vergleicht HostDeck
}

// PlanFile ist eine Datei, die sich ändert.
type PlanFile struct {
	Path string `json:"pfad"`
	New  bool   `json:"neu"`  // wird angelegt
	Text string `json:"text"` // Inhalt der neuen Datei oder angehängte Zeilen; leer: bleibt
	Note string `json:"hinweis,omitempty"`
}

// Step ist ein Schritt beim Freischalten oder Zurücknehmen.
type Step struct {
	Name   string `json:"name"`
	State  string `json:"stand"` // laeuft, ok, fehler, zurueck
	Detail string `json:"detail,omitempty"`
}

// AccessJob ist das laufende oder letzte Freischalten oder Zurücknehmen.
type AccessJob struct {
	Running bool   `json:"laeuft"`
	Action  string `json:"aktion"` // freischalten, zuruecknehmen
	Domain  string `json:"adresse,omitempty"`
	Steps   []Step `json:"schritte"`
	Error   string `json:"fehler,omitempty"`
	// Unchanged: Der Auftrag ging nicht, aber alles ist wie vorher. Ohne
	// das Feld muss jemand nachsehen.
	Unchanged bool       `json:"wie_vorher,omitempty"`
	Started   time.Time  `json:"start"`
	Done      *time.Time `json:"ende,omitempty"`
}

// UnchangedError ist ein Fehler, nach dem alles wie vorher ist: Caddy,
// Dateien und Eingang. Nur dann sagt der Leitstand „alles wie vorher“.
type UnchangedError struct{ Err error }

func (e *UnchangedError) Error() string { return e.Err.Error() }
func (e *UnchangedError) Unwrap() error { return e.Err }

// AccessReply ist die Antwort auf GET /api/zugang.
type AccessReply struct {
	State AccessState `json:"stand"`
	Job   *AccessJob  `json:"auftrag,omitempty"`
}

type accessRequest struct {
	Domain string `json:"adresse"`
}

func (d *Dienst) accessReply(ctx context.Context) AccessReply {
	rep := AccessReply{State: d.Access.State(ctx)}
	d.mu.Lock()
	if d.job != nil {
		j := *d.job
		j.Steps = append(make([]Step, 0, len(d.job.Steps)), d.job.Steps...)
		rep.Job = &j
	}
	d.mu.Unlock()
	return rep
}

func (d *Dienst) handleAccess(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.accessReply(r.Context()))
}

func (d *Dienst) handleAccessCheck(w http.ResponseWriter, r *http.Request) {
	var req accessRequest
	if !decode(w, r, &req) {
		return
	}
	plan, err := d.Access.Check(r.Context(), req.Domain)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (d *Dienst) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req accessRequest
	if !decode(w, r, &req) {
		return
	}
	d.startAccessJob(w, r, "freischalten", req.Domain)
}

func (d *Dienst) handleUnpublish(w http.ResponseWriter, r *http.Request) {
	d.startAccessJob(w, r, "zuruecknehmen", "")
}

// startAccessJob prüft und startet einen Auftrag im Hintergrund. Die
// Website startet dabei neu; die Seite fragt den Fortschritt ab.
func (d *Dienst) startAccessJob(w http.ResponseWriter, r *http.Request, action, domain string) {
	gate, addr := visitor(r)
	if action == "freischalten" {
		// Erst prüfen, damit ein Tippfehler gleich eine klare Antwort bekommt.
		plan, err := d.Access.Check(r.Context(), domain)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: err.Error()})
			return
		}
		domain = plan.Domain
	} else if d.Access.State(r.Context()).Domain == "" {
		writeJSON(w, http.StatusConflict, apiError{Error: "Der Leitstand ist nicht freigeschaltet; es gibt nichts zurückzunehmen."})
		return
	}
	what := "Freischalten"
	if action == "zuruecknehmen" {
		what = "Zugang von außen zurücknehmen"
	}
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
	job := &AccessJob{Running: true, Action: action, Domain: domain, Started: d.now(), Steps: []Step{}}
	d.mu.Lock()
	ctx := d.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	d.job = job
	d.mu.Unlock()
	if action == "freischalten" {
		d.note(protokoll.Entry{Kind: protokoll.Zugang, Text: "Freischalten gestartet: " + domain, Addr: addr, Gate: gate})
	} else {
		d.note(protokoll.Entry{Kind: protokoll.Zugang, Text: "Zurücknehmen des Zugangs von außen gestartet", Addr: addr, Gate: gate})
	}
	step := func(s Step) {
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
		var err error
		if action == "freischalten" {
			err = d.Access.Publish(ctx, domain, step)
		} else {
			err = d.Access.Unpublish(ctx, step)
		}
		// Erst die Sperre frei, dann „fertig“ melden: Wer danach gleich den
		// nächsten Auftrag startet, findet sie frei.
		// Danach das Ergebnis ins Protokoll: Wer nach „fertig“ ins Protokoll
		// schaut, findet es dort schon.
		l.Release()
		switch {
		case err != nil && action == "freischalten":
			d.note(protokoll.Entry{Kind: protokoll.Zugang, Text: "Freischalten von " + domain + " ging nicht: " + err.Error(), Addr: addr, Gate: gate})
		case err != nil:
			d.note(protokoll.Entry{Kind: protokoll.Zugang, Text: "Zurücknehmen ging nicht: " + err.Error(), Addr: addr, Gate: gate})
		case action == "freischalten":
			d.note(protokoll.Entry{Kind: protokoll.Zugang, Text: "Freigeschaltet: https://" + domain, Addr: addr, Gate: gate})
		default:
			d.note(protokoll.Entry{Kind: protokoll.Zugang, Text: "Zugang von außen zurückgenommen; der Leitstand geht nur noch über den Notausgang", Addr: addr, Gate: gate})
		}
		now := d.now()
		d.mu.Lock()
		job.Running = false
		job.Done = &now
		if err != nil {
			job.Error = err.Error()
			var u *UnchangedError
			job.Unchanged = errors.As(err, &u)
		}
		d.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, d.accessReply(r.Context()))
}
