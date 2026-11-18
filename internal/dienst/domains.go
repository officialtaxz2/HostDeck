package dienst

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/zustand"
)

// Domains: Caddy einmal ordnen, danach Domains anlegen,
// Ziel ändern, ab- und anschalten. Die Arbeit macht Domains; der Dienst
// sorgt für Anmeldung, Bestätigung, Laufsperre, Protokoll und den
// laufenden Auftrag.

// Domains verwaltet die Domains von Caddy.
type Domains interface {
	Overview(ctx context.Context) domains.Overview
	ReorgPlan(ctx context.Context, opts *domains.Options) (domains.ReorgPlan, error)
	Reorg(ctx context.Context, opts *domains.Options, stand string, event func(caddy.Event)) (string, error)
	ChangePlan(ctx context.Context, req domains.ChangeReq) (domains.ChangePlan, error)
	Change(ctx context.Context, req domains.ChangeReq, stand string, event func(caddy.Event)) (string, error)
}

// DomainTimeout begrenzt eine Änderung samt Warten auf das Zertifikat.
const DomainTimeout = 10 * time.Minute

// DomainJob ist die laufende oder letzte Änderung an den Domains.
type DomainJob struct {
	Running bool       `json:"laeuft"`
	What    string     `json:"was"`
	Domain  string     `json:"domain,omitempty"`
	Steps   []Step     `json:"schritte"`
	Result  string     `json:"ergebnis,omitempty"`
	Error   string     `json:"fehler,omitempty"`
	Undone  bool       `json:"wie_vorher,omitempty"` // ging nicht, alles ist zurückgelegt
	Started time.Time  `json:"start"`
	Done    *time.Time `json:"ende,omitempty"`
}

// DomainsReply ist die Antwort auf GET /api/domains.
type DomainsReply struct {
	State domains.Overview `json:"stand"`
	Job   *DomainJob       `json:"auftrag,omitempty"`
	// Zustand je Seite aus der einen Quelle; der Leitstand rechnet ihn
	// nicht selbst aus.
	States []zustand.Domain `json:"zustand"`
}

// StatesReply ist die Antwort auf GET /api/domains/zustand: nur der Zustand,
// für Übersicht, „Braucht dich“ und Projekte.
type StatesReply struct {
	States  []zustand.Domain `json:"zustand"`
	Pending bool             `json:"pruefung_laeuft"`
	Checked time.Time        `json:"geprueft,omitempty"`
}

func (d *Dienst) states(ctx context.Context, sites []domains.Site) []zustand.Domain {
	if d.DomainStates == nil {
		return []zustand.Domain{}
	}
	out := d.DomainStates(ctx, sites)
	if out == nil {
		out = []zustand.Domain{}
	}
	return out
}

func (d *Dienst) handleDomainStates(w http.ResponseWriter, r *http.Request) {
	ov := d.Domains.Overview(r.Context())
	writeJSON(w, http.StatusOK, StatesReply{States: d.states(r.Context(), ov.Sites), Pending: ov.Pending, Checked: ov.Checked})
}

type reorgRequest struct {
	Options *domains.Options `json:"auswahl"`
	Stand   string           `json:"stand"`
}

type domainRequest struct {
	domains.ChangeReq
	Stand string `json:"stand"`
}

func domainStatus(err error) int {
	switch {
	case errors.Is(err, domains.ErrBadChoice):
		return http.StatusUnprocessableEntity
	case errors.Is(err, domains.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, caddy.ErrNotFound):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (d *Dienst) domainsReply(ctx context.Context) DomainsReply {
	rep := DomainsReply{State: d.Domains.Overview(ctx)}
	rep.States = d.states(ctx, rep.State.Sites)
	d.mu.Lock()
	if d.djob != nil {
		j := *d.djob
		j.Steps = append(make([]Step, 0, len(d.djob.Steps)), d.djob.Steps...)
		rep.Job = &j
	}
	d.mu.Unlock()
	return rep
}

func (d *Dienst) handleDomains(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.domainsReply(r.Context()))
}

func (d *Dienst) handleReorgPlan(w http.ResponseWriter, r *http.Request) {
	var req reorgRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	p, err := d.Domains.ReorgPlan(ctx, req.Options)
	if err != nil {
		writeJSON(w, domainStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (d *Dienst) handleDomainPlan(w http.ResponseWriter, r *http.Request) {
	var req domainRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	p, err := d.Domains.ChangePlan(ctx, req.ChangeReq)
	if err != nil {
		writeJSON(w, domainStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (d *Dienst) handleReorg(w http.ResponseWriter, r *http.Request) {
	var req reorgRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Stand == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Erst die Vorschau öffnen."})
		return
	}
	d.startDomainJob(w, r, "Caddy ordnen", "", func(ctx context.Context, ev func(caddy.Event)) (string, error) {
		return d.Domains.Reorg(ctx, req.Options, req.Stand, ev)
	})
}

var domainWhat = map[string]string{
	domains.ActNew:    "Domain anlegen",
	domains.ActTarget: "Ziel einer Domain ändern",
	domains.ActOff:    "Domain abschalten",
	domains.ActOn:     "Domain anschalten",
}

func (d *Dienst) handleDomainChange(w http.ResponseWriter, r *http.Request) {
	var req domainRequest
	if !decode(w, r, &req) {
		return
	}
	what, ok := domainWhat[req.Action]
	if !ok || req.Stand == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Unbekannte Änderung oder keine Vorschau."})
		return
	}
	host, err := domains.NormHost(req.Host)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: err.Error()})
		return
	}
	d.startDomainJob(w, r, what, host, func(ctx context.Context, ev func(caddy.Event)) (string, error) {
		return d.Domains.Change(ctx, req.ChangeReq, req.Stand, ev)
	})
}

func (d *Dienst) startDomainJob(w http.ResponseWriter, r *http.Request, what, host string, run func(context.Context, func(caddy.Event)) (string, error)) {
	label := what
	if host != "" {
		label += ": " + host
	}
	l, err := lauf.TakeIn(d.RunDir, label)
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
	job := &DomainJob{Running: true, What: what, Domain: host, Started: d.now(), Steps: []Step{}}
	d.mu.Lock()
	base := d.ctx
	if base == nil {
		base = context.Background()
	}
	d.djob = job
	d.mu.Unlock()
	d.note(protokoll.Entry{Kind: protokoll.Domain, Text: clip(label + " gestartet"), Addr: addr, Gate: gate})
	ev := func(e caddy.Event) {
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
		ctx, cancel := context.WithTimeout(base, DomainTimeout)
		defer cancel()
		msg, err := run(ctx, ev)
		l.Release()
		// Erst ins Protokoll, dann „fertig“ melden.
		text := msg
		if err != nil {
			text = label + " ging nicht: " + err.Error()
		}
		d.note(protokoll.Entry{Kind: protokoll.Domain, Text: clip(text), Addr: addr, Gate: gate})
		d.logf("Domains: %s", text)
		now := d.now()
		var undone *caddy.ErrUndone
		d.mu.Lock()
		job.Running, job.Done = false, &now
		if err != nil {
			job.Error = err.Error()
			job.Undone = errors.As(err, &undone) || !changedFiles(job.Steps)
		} else {
			job.Result = msg
		}
		d.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, d.domainsReply(r.Context()))
}

// changedFiles: Ein Schritt hat Dateien geschrieben. Ging es vorher
// schief, ist nichts geändert.
func changedFiles(steps []Step) bool {
	for _, s := range steps {
		if s.Name == caddy.StepWrite && s.State == caddy.StateOK {
			return true
		}
	}
	return false
}
