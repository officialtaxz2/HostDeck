package dienst

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/aktualisieren"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

// Projekte aktualisieren: nachsehen, ob es Neues gibt,
// Vorschau, Aktualisieren mit Rückfall und Zurück auf vorher. Die Arbeit
// macht Updates; der Dienst sorgt für Anmeldung, Bestätigung, Laufsperre,
// Protokoll und hält den laufenden Auftrag, damit der Browser ihn verfolgen
// kann.

// Updates bringt Projekte auf einen neuen Stand.
type Updates interface {
	LastReport() aktualisieren.Report
	LastFallback(project string) *aktualisieren.Fallback
	Check(ctx context.Context, project string) (aktualisieren.Report, error)
	Plan(ctx context.Context, project string, want []string) (aktualisieren.Plan, error)
	Do(ctx context.Context, project string, want []string, out io.Writer) (aktualisieren.Result, error)
	RevertPlan(project string) (aktualisieren.Plan, error)
	Revert(ctx context.Context, project string, out io.Writer) (aktualisieren.Result, error)
}

// UpdateTimeout begrenzt eine Aktualisierung samt Bauen.
const UpdateTimeout = 45 * time.Minute

// UpdateJob ist die laufende oder letzte Aktualisierung.
type UpdateJob struct {
	Running bool                  `json:"laeuft"`
	Revert  bool                  `json:"zurueck,omitempty"`
	Project string                `json:"projekt"`
	Started time.Time             `json:"start"`
	Done    *time.Time            `json:"ende,omitempty"`
	Result  *aktualisieren.Result `json:"ergebnis,omitempty"`
	Error   string                `json:"fehler,omitempty"`
	Lines   []string              `json:"zeilen"`
	First   int                   `json:"erste"`
}

// UpdatesReply ist die Antwort auf GET /api/aktualisieren.
type UpdatesReply struct {
	Report    aktualisieren.Report               `json:"pruefstand"`
	Fallbacks map[string]*aktualisieren.Fallback `json:"rueckfall"`
	Job       *UpdateJob                         `json:"auftrag,omitempty"`
}

type updateRequest struct {
	Project  string   `json:"projekt"`
	Services []string `json:"dienste"`
}

// updateLines sammelt die Ausgabe eines Auftrags zeilenweise.
type updateLines struct {
	d       *Dienst
	job     *UpdateJob
	partial []byte
}

func (o *updateLines) Write(p []byte) (int, error) {
	o.d.mu.Lock()
	defer o.d.mu.Unlock()
	o.partial = append(o.partial, p...)
	for {
		i := strings.IndexAny(string(o.partial), "\n\r")
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(o.partial[:i]), " ")
		o.partial = o.partial[i+1:]
		if line == "" {
			continue
		}
		o.job.Lines = append(o.job.Lines, clip(line))
		if over := len(o.job.Lines) - maxJobLines; over > 0 {
			o.job.Lines = append([]string(nil), o.job.Lines[over:]...)
			o.job.First += over
		}
	}
	if len(o.partial) > 4096 {
		o.job.Lines = append(o.job.Lines, clip(string(o.partial)))
		o.partial = nil
	}
	return len(p), nil
}

func (d *Dienst) updateSnapshot(from int) *UpdateJob {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ujob == nil {
		return nil
	}
	j := *d.ujob
	skip := from - j.First
	if skip < 0 {
		skip = 0
	}
	if skip > len(j.Lines) {
		skip = len(j.Lines)
	}
	j.Lines = append([]string{}, j.Lines[skip:]...)
	j.First += skip
	return &j
}

func updateStatus(err error) int {
	switch {
	case errors.Is(err, aktualisieren.ErrBadChoice):
		return http.StatusBadRequest
	case errors.Is(err, aktualisieren.ErrNotFound):
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

func (d *Dienst) handleUpdates(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("ab"))
	rep := d.Updates.LastReport()
	fbs := map[string]*aktualisieren.Fallback{}
	for _, p := range rep.Projects {
		if fb := d.Updates.LastFallback(p.Project); fb != nil {
			fbs[p.Project] = fb
		}
	}
	if rep.Projects == nil {
		rep.Projects = []aktualisieren.ProjectCheck{}
	}
	writeJSON(w, http.StatusOK, UpdatesReply{Report: rep, Fallbacks: fbs, Job: d.updateSnapshot(from)})
}

func (d *Dienst) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Project != "" && !aktualisieren.ValidName(req.Project) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Ungültiger Projektname."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	rep, err := d.Updates.Check(ctx, req.Project)
	if err != nil {
		writeJSON(w, updateStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (d *Dienst) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	p, err := d.Updates.Plan(ctx, req.Project, req.Services)
	if err != nil {
		writeJSON(w, updateStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (d *Dienst) handleRevertPlan(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}
	if !aktualisieren.ValidName(req.Project) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Ungültiger Projektname."})
		return
	}
	p, err := d.Updates.RevertPlan(req.Project)
	if err != nil {
		writeJSON(w, updateStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (d *Dienst) handleUpdateStart(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Services == nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Bitte erst die Vorschau ansehen."})
		return
	}
	d.startUpdate(w, r, req, false)
}

func (d *Dienst) handleRevert(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}
	d.startUpdate(w, r, req, true)
}

// startUpdate startet eine Aktualisierung oder ein Zurückstellen im
// Hintergrund. Schließt jemand den Browser, läuft sie weiter.
func (d *Dienst) startUpdate(w http.ResponseWriter, r *http.Request, req updateRequest, revert bool) {
	if !aktualisieren.ValidName(req.Project) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Ungültiger Projektname."})
		return
	}
	for _, s := range req.Services {
		if !aktualisieren.ValidName(s) {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "Ungültiger Dienst."})
			return
		}
	}
	what := "Projekt " + req.Project + " aktualisieren"
	if revert {
		what = "Projekt " + req.Project + " zurückstellen"
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
	gate, addr := visitor(r)
	job := &UpdateJob{Running: true, Revert: revert, Project: req.Project, Started: d.now(), Lines: []string{}}
	d.mu.Lock()
	base := d.ctx
	if base == nil {
		base = context.Background()
	}
	d.ujob = job
	d.mu.Unlock()
	d.note(protokoll.Entry{Kind: protokoll.Projekt, Text: clip(what + " gestartet" + list(req.Services)), Addr: addr, Gate: gate})
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer l.Release()
		ctx, cancel := context.WithTimeout(base, UpdateTimeout)
		defer cancel()
		out := &updateLines{d: d, job: job}
		var res aktualisieren.Result
		var err error
		if revert {
			res, err = d.Updates.Revert(ctx, req.Project, out)
		} else {
			res, err = d.Updates.Do(ctx, req.Project, req.Services, out)
		}
		out.Write([]byte("\n"))
		// Erst ins Protokoll, dann „fertig“ melden.
		text := res.Text()
		switch {
		case err != nil:
			text = what + ": nicht begonnen (" + err.Error() + ")"
		case revert && res.OK:
			text = "Projekt " + req.Project + " auf den Stand von vorher zurückgestellt"
		case revert:
			text = "Projekt " + req.Project + ": Zurückstellen gescheitert (" + res.Problem + ")"
		}
		d.note(protokoll.Entry{Kind: protokoll.Projekt, Text: clip(text), Addr: addr, Gate: gate})
		if err == nil && res.OK {
			title := req.Project + " aktualisiert"
			if revert {
				title = req.Project + " zurückgestellt"
			}
			_ = d.Events.Add(ueberwachung.Happening{Kind: ueberwachung.HapProject, Level: ueberwachung.LevelOK, Project: req.Project, Title: title, Text: clip(firstSentence(text)), Link: "#/projekte/project:" + req.Project})
		}
		d.logf("Aktualisieren: %s", text)
		now := d.now()
		d.mu.Lock()
		job.Running = false
		job.Done = &now
		if err != nil {
			job.Error = err.Error()
		} else {
			job.Result = &res
		}
		d.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, d.updateSnapshot(0))
}

func list(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return ": " + strings.Join(s, ", ")
}
