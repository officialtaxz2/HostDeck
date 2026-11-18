package dienst

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// Betrieb im Leitstand: Container ansehen, Logs lesen,
// starten, stoppen, neu starten. Die Arbeit macht Containers; der Dienst
// sorgt für Anmeldung, Bestätigung mit Passwort, Laufsperre und Protokoll.

// Containers steuert die Container des Servers.
type Containers interface {
	List(ctx context.Context) (betrieb.Snapshot, error)
	Plan(ctx context.Context, target, action string) (betrieb.Plan, error)
	Do(ctx context.Context, target, action string, want []string) (betrieb.Result, error)
	Logs(ctx context.Context, name string, lines int) (betrieb.Logs, error)
}

// ContainerDetails kann zusätzlich die Einzelheiten eines Containers
// lesen (betrieb.Service). Kann Containers das nicht, antwortet der Dienst
// mit 404.
type ContainerDetails interface {
	Details(ctx context.Context, name string) (betrieb.Details, error)
}

// ActionTimeout begrenzt eine Container-Aktion; die Website wartet auf die
// Antwort.
const ActionTimeout = 100 * time.Second

type containerRequest struct {
	Target     string   `json:"ziel"`
	Action     string   `json:"aktion"`
	Containers []string `json:"container,omitempty"` // wie in der Vorschau gezeigt
}

// ContainerReply ist die Antwort auf GET /api/container.
type ContainerReply struct {
	betrieb.Snapshot
	Error string `json:"fehler,omitempty"`
}

func (d *Dienst) handleContainers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	snap, err := d.Containers.List(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ContainerReply{Snapshot: snap})
}

func (d *Dienst) handleContainerPlan(w http.ResponseWriter, r *http.Request) {
	var req containerRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	p, err := d.Containers.Plan(ctx, req.Target, req.Action)
	if err != nil {
		writeJSON(w, containerStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func containerStatus(err error) int {
	switch {
	case errors.Is(err, betrieb.ErrBadAction), errors.Is(err, betrieb.ErrBadTarget):
		return http.StatusBadRequest
	case errors.Is(err, betrieb.ErrNotFound):
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

func (d *Dienst) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	var req containerRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Containers == nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Bitte erst die Vorschau ansehen."})
		return
	}
	if _, _, err := betrieb.ParseTarget(req.Target); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	what := map[string]string{betrieb.Start: "starten", betrieb.Stop: "stoppen", betrieb.Restart: "neu starten"}[req.Action]
	if what == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: betrieb.ErrBadAction.Error()})
		return
	}
	l, err := lauf.TakeIn(d.RunDir, "Container "+what+" ("+req.Target+")")
	var busy *lauf.BusyError
	switch {
	case errors.As(err, &busy):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	defer l.Release()
	gate, addr := visitor(r)
	// Schließt jemand den Browser, läuft die Aktion trotzdem zu Ende; sonst
	// bliebe ein Projekt halb gestoppt.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), ActionTimeout)
	defer cancel()
	res, err := d.Containers.Do(ctx, req.Target, req.Action, req.Containers)
	if err != nil {
		if len(res.Steps) == 0 {
			code := containerStatus(err)
			if res.Plan.Action != "" {
				code = http.StatusConflict
			}
			writeJSON(w, code, map[string]any{"error": err.Error(), "plan": res.Plan})
			return
		}
	}
	d.note(protokoll.Entry{Kind: protokoll.Container, Text: clip(res.Text()), Addr: addr, Gate: gate})
	d.logf("Betrieb: %s", res.Text())
	// Der Zustand der Domains hängt am Stand der Container: Die letzte
	// Prüfung gilt nicht mehr (sonst stünde nach dem Starten bis zu einer
	// Minute „antwortet nicht“ da).
	if rf, ok := d.Domains.(interface{ Refresh() }); ok {
		rf.Refresh()
	}
	// Ebenso die Seitenprüfung der Überwachung: Sonst stand dort nach dem
	// Starten bis zu fünf Minuten „pausiert“, auf Domains schon „antwortet“.
	if rc, ok := d.Limits.(interface{ RecheckSites() }); ok {
		rc.RecheckSites()
	}
	writeJSON(w, http.StatusOK, res)
}

func (d *Dienst) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("zeilen"))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	lg, err := d.Containers.Logs(ctx, r.URL.Query().Get("name"), lines)
	if err != nil {
		writeJSON(w, containerStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, lg)
}

func (d *Dienst) handleContainerDetails(w http.ResponseWriter, r *http.Request) {
	cd, ok := d.Containers.(ContainerDetails)
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Einzelheiten kann dieser Leitstand nicht lesen."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	det, err := cd.Details(ctx, r.URL.Query().Get("name"))
	if err != nil {
		writeJSON(w, containerStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, det)
}
