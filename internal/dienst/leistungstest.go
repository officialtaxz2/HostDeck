package dienst

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/leistungstest"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// Leistungstest: benchmark.sh als Lauf. Die Arbeit macht
// Benchmarks; der Dienst sorgt für Anmeldung, Bestätigung, Laufsperre,
// Protokoll und hält die Ausgabe, damit der Browser sie verfolgen kann.

// Benchmarks führt Leistungstests aus und verwaltet die Ergebnisse.
type Benchmarks interface {
	Info() leistungstest.Info
	List() ([]leistungstest.Result, error)
	Report(id string) (leistungstest.Result, error)
	Run(ctx context.Context, opt leistungstest.Options, out io.Writer) (leistungstest.Result, error)
	ZipPath(id string) (string, error)
	Claim(id string) (string, error)
	Delete(id string) error
	RemoveGeekbench() (int64, error)
}

// BenchTimeout begrenzt einen Test samt Paketinstallation und Download.
const BenchTimeout = 3 * time.Hour

// BenchReply ist die Antwort auf GET /api/leistungstest.
type BenchReply struct {
	Info    leistungstest.Info     `json:"vorab"`
	Results []leistungstest.Result `json:"tests"`
	Error   string                 `json:"fehler,omitempty"`
	Job     *BackupJob             `json:"auftrag,omitempty"`
	// Last ist das Ergebnis des Tests, den der Dienst zuletzt gestartet
	// hat (Kennung).
	Last string `json:"letzter,omitempty"`
}

func (d *Dienst) benchReply(from int) BenchReply {
	rep := BenchReply{Info: d.Benchmarks.Info()}
	list, err := d.Benchmarks.List()
	if err != nil {
		rep.Error = "Die Ergebnisse ließen sich nicht lesen: " + err.Error()
	}
	if list == nil {
		list = []leistungstest.Result{}
	}
	rep.Results = list
	d.mu.Lock()
	rep.Job = runSnapshot(d.lrun, from)
	rep.Last = d.lastBench
	d.mu.Unlock()
	return rep
}

func benchStatus(err error) int {
	switch {
	case errors.Is(err, leistungstest.ErrBadID), errors.Is(err, leistungstest.ErrOptions):
		return http.StatusBadRequest
	case errors.Is(err, leistungstest.ErrNotFound):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func (d *Dienst) handleBench(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("ab"))
	writeJSON(w, http.StatusOK, d.benchReply(from))
}

func (d *Dienst) handleBenchReport(w http.ResponseWriter, r *http.Request) {
	res, err := d.Benchmarks.Report(r.URL.Query().Get("id"))
	if err != nil {
		writeJSON(w, benchStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleBenchStart startet einen Test im Hintergrund. Schließt jemand den
// Browser, läuft er weiter.
func (d *Dienst) handleBenchStart(w http.ResponseWriter, r *http.Request) {
	var opt leistungstest.Options
	if !decode(w, r, &opt) {
		return
	}
	if err := opt.Check(); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if opt.Geekbench {
		if in := d.Benchmarks.Info(); !in.GeekbenchOK {
			writeJSON(w, http.StatusConflict, apiError{Error: in.GeekbenchWhy + " Starte den Test ohne Geekbench."})
			return
		}
	}
	gate, addr := visitor(r)
	l, err := lauf.TakeIn(d.RunDir, lauf.Leistungstest)
	var busy *lauf.BusyError
	switch {
	case errors.As(err, &busy):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	run := &backupRun{job: &BackupJob{Running: true, Started: d.now(), Lines: []string{}}, addr: addr, gate: gate}
	d.mu.Lock()
	base := d.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, BenchTimeout)
	run.cancel = cancel
	d.lrun = run
	d.mu.Unlock()
	what := "Leistungstest gestartet (" + benchText(opt) + ")"
	d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: what, Addr: addr, Gate: gate})
	d.logf("%s", what)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		out := jobOutput{d: d, run: run}
		res, err := d.Benchmarks.Run(ctx, opt, out)
		out.Write([]byte("\n"))
		// Erst die Sperre frei und das Ergebnis ins Protokoll, dann „fertig“.
		l.Release()
		d.mu.Lock()
		run.closing = true
		cancelled := run.job.Cancelled
		d.mu.Unlock()
		text := benchResultText(res, err, cancelled)
		d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: clip(text), Addr: addr, Gate: gate})
		d.logf("%s", text)
		now := d.now()
		d.mu.Lock()
		run.job.Running = false
		run.job.Done = &now
		switch {
		case err != nil:
			run.job.Code = 1
			run.job.Lines = append(run.job.Lines, "Fehler: "+err.Error())
		case res.State == leistungstest.StateDone:
			run.job.Code = 0
		default:
			run.job.Code = 1
		}
		if err == nil {
			d.lastBench = res.ID
		}
		d.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, d.benchReply(0))
}

func benchText(opt leistungstest.Options) string {
	s := opt.Seconds
	if s == 0 {
		s = leistungstest.DefaultSeconds
	}
	gb := "ohne Geekbench"
	if opt.Geekbench {
		gb = "mit Geekbench"
	}
	return fmt.Sprintf("%s, Sysbench je %d s", gb, s)
}

func benchResultText(res leistungstest.Result, err error, cancelled bool) string {
	switch {
	case err != nil:
		return "Leistungstest nicht gelaufen: " + err.Error()
	case cancelled || res.State == leistungstest.StateCancelled:
		return "Leistungstest abgebrochen; was bis dahin gemessen war, liegt als ZIP bereit"
	case res.State == leistungstest.StateFailed:
		return "Leistungstest gescheitert, bevor er messen konnte (siehe Ausgabe)"
	case res.State == leistungstest.StateIncomplete:
		return "Leistungstest unvollständig: mindestens eine Phase ging schief"
	}
	return fmt.Sprintf("Leistungstest fertig: Sysbench %s Events/s mit 1 Thread, %s mit %d Threads", num(res.SysbenchSingle), num(res.SysbenchMulti), res.Threads)
}

// num schreibt eine Zahl mit zwei Stellen und deutschem Komma.
func num(v float64) string { return strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1) }

func (d *Dienst) handleBenchCancel(w http.ResponseWriter, r *http.Request) {
	gate, addr := visitor(r)
	d.mu.Lock()
	run := d.lrun
	ok := run != nil && run.job.Running && !run.closing
	if ok {
		run.job.Cancelled = true
	}
	d.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusConflict, apiError{Error: "Es läuft gerade kein Leistungstest."})
		return
	}
	d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: "Leistungstest: Abbrechen angefordert", Addr: addr, Gate: gate})
	run.cancel()
	w.WriteHeader(http.StatusNoContent)
}

func (d *Dienst) handleBenchZip(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	p, err := d.Benchmarks.ZipPath(id)
	if err != nil {
		writeJSON(w, benchStatus(err), apiError{Error: err.Error()})
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+leistungstest.ZipName(id)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (d *Dienst) handleBenchClaim(w http.ResponseWriter, r *http.Request) {
	link, err := d.Benchmarks.Claim(r.URL.Query().Get("id"))
	if err != nil {
		writeJSON(w, benchStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"link": link})
}

// handleBenchGeekbench löscht den heruntergeladenen Geekbench (rund
// 480 MB). Der nächste Test mit Geekbench lädt ihn neu.
func (d *Dienst) handleBenchGeekbench(w http.ResponseWriter, r *http.Request) {
	// Die Laufsperre, nicht nur der eigene Lauf: Ein Test aus dem Terminal
	// (hostdeck leistungstest) braucht Geekbench auch (Nachprüfung 6a NP2).
	l, err := lauf.TakeIn(d.RunDir, lauf.Leistungstest)
	var busy *lauf.BusyError
	switch {
	case errors.As(err, &busy):
		writeJSON(w, http.StatusConflict, apiError{Error: "Geekbench bleibt, solange etwas läuft: " + err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	n, err := d.Benchmarks.RemoveGeekbench()
	l.Release()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	gate, addr := visitor(r)
	d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: fmt.Sprintf("Geekbench vom Server entfernt (%s frei)", sizeText(n)), Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, d.benchReply(0))
}

// sizeText schreibt eine Größe in MB oder GB mit deutschem Komma.
func sizeText(n int64) string {
	if n >= 1e9 {
		return strings.Replace(strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64), ".", ",", 1) + " GB"
	}
	return strconv.FormatInt((n+5e5)/1e6, 10) + " MB"
}

type benchIDRequest struct {
	ID string `json:"id"`
}

func (d *Dienst) handleBenchDelete(w http.ResponseWriter, r *http.Request) {
	var req benchIDRequest
	if !decode(w, r, &req) {
		return
	}
	d.mu.Lock()
	busy := d.lrun != nil && d.lrun.job.Running
	d.mu.Unlock()
	if busy {
		writeJSON(w, http.StatusConflict, apiError{Error: "Während ein Leistungstest läuft, bleiben die Ergebnisse, wie sie sind."})
		return
	}
	if err := d.Benchmarks.Delete(req.ID); err != nil {
		writeJSON(w, benchStatus(err), apiError{Error: err.Error()})
		return
	}
	gate, addr := visitor(r)
	d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: "Leistungstest vom " + req.ID + " gelöscht", Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, d.benchReply(0))
}
