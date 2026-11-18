package dienst

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/aufraeumen"
	"github.com/officialtaxz2/hostdeck/internal/platz"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// Ordner und Dateien unter Speicherplatz: HostDeck misst die Platte wie du,
// zeigt Ordner und Dateien nach Größe und löscht, was du auswählst und die
// Schutzstufen erlauben. Wie beim Aufräumen: Vorschau, Passwort,
// Protokoll. Die Arbeit macht Space.

// Space misst, zeigt und löscht unter Speicherplatz (platz.Service).
type Space interface {
	Listing(ctx context.Context, host string) (platz.Listing, error)
	Measure(ctx context.Context, why string) error
	Measuring() platz.MeasureInfo
	Plan(ctx context.Context, paths []string) (platz.Plan, error)
	Run(ctx context.Context, stand string, trash bool, ev func(aufraeumen.Event)) (string, error)
	// Pending nennt die Pfade einer Vorschau (fürs Protokoll).
	Pending(stand string) []string
}

// MeasureTimeout begrenzt eine Messung der ganzen Platte.
const MeasureTimeout = 45 * time.Minute

// SpaceReply ist die Antwort auf GET /api/platz/ordner.
type SpaceReply struct {
	List *platz.Listing `json:"liste,omitempty"`
	Job  *CleanJob      `json:"auftrag,omitempty"`
}

type spacePlanRequest struct {
	Paths []string `json:"auswahl"`
}

type spaceRunRequest struct {
	Stand string `json:"stand"`
	Trash bool   `json:"papierkorb"`
}

func spaceStatus(err error) int {
	switch {
	case errors.Is(err, platz.ErrGone):
		return http.StatusNotFound
	case errors.Is(err, platz.ErrNotDir), errors.Is(err, platz.ErrNothing), errors.Is(err, platz.ErrTooMany), errors.Is(err, platz.ErrViaLink):
		return http.StatusBadRequest
	case errors.Is(err, platz.ErrIncomplete):
		return http.StatusServiceUnavailable
	case errors.Is(err, platz.ErrBusy), errors.Is(err, platz.ErrExpired), errors.Is(err, platz.ErrChanged):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (d *Dienst) spaceJob() *CleanJob {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pjob == nil {
		return nil
	}
	j := *d.pjob
	j.Steps = append(make([]Step, 0, len(d.pjob.Steps)), d.pjob.Steps...)
	return &j
}

func (d *Dienst) handleSpaceList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	host := r.URL.Query().Get("pfad")
	if host == "" {
		host = "/"
	}
	l, err := d.Space.Listing(ctx, host)
	if err != nil {
		writeJSON(w, spaceStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, SpaceReply{List: &l, Job: d.spaceJob()})
}

func (d *Dienst) handleSpaceMeasure(w http.ResponseWriter, r *http.Request) {
	if d.Space.Measuring().Running {
		writeJSON(w, http.StatusConflict, apiError{Error: platz.ErrBusy.Error()})
		return
	}
	d.mu.Lock()
	base := d.ctx
	d.mu.Unlock()
	if base == nil {
		base = context.Background()
	}
	d.startMeasure(base, "auf Knopfdruck")
	writeJSON(w, http.StatusAccepted, d.Space.Measuring())
}

// startMeasure misst im Hintergrund. Es nimmt keine Laufsperre: Messen
// liest nur und läuft schonend, auch neben einem Backup.
func (d *Dienst) startMeasure(base context.Context, why string) {
	started := make(chan struct{})
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(base, MeasureTimeout)
		defer cancel()
		close(started)
		if err := d.Space.Measure(ctx, why); err != nil && !errors.Is(err, platz.ErrBusy) {
			d.logf("Speicherplatz messen (%s): %v", why, err)
		}
	}()
	<-started
	// Kurz warten, damit die Antwort schon „läuft“ zeigt.
	for i := 0; i < 50 && !d.Space.Measuring().Running; i++ {
		time.Sleep(2 * time.Millisecond)
	}
}

func (d *Dienst) handleSpacePlan(w http.ResponseWriter, r *http.Request) {
	var req spacePlanRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	p, err := d.Space.Plan(ctx, req.Paths)
	if err != nil {
		writeJSON(w, spaceStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (d *Dienst) handleSpaceRun(w http.ResponseWriter, r *http.Request) {
	var req spaceRunRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Stand == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Erst auswählen und die Vorschau öffnen."})
		return
	}
	paths := d.Space.Pending(req.Stand)
	details := make([]string, 0, len(paths)+1)
	if req.Trash {
		details = append(details, "Erst in den Papierkorb")
	} else {
		details = append(details, "Endgültig, ohne Papierkorb")
	}
	for i, p := range paths {
		if i == 20 {
			details = append(details, "…")
			break
		}
		details = append(details, clip(p))
	}
	what := "Löschen unter Speicherplatz"
	d.startStepJob(w, r, &d.pjob, protokoll.Speicher, "Löschen", what, details, func(ctx context.Context, ev func(aufraeumen.Event)) (string, error) {
		return d.Space.Run(ctx, req.Stand, req.Trash, ev)
	}, func(context.Context) any { return SpaceReply{Job: d.spaceJob()} })
}
