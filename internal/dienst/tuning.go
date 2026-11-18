package dienst

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
)

// Tuning: tune.sh im Leitstand. Lesen (status, Vorschau)
// geht ohne Bestätigung; Anwenden, Zurücknehmen, CPU-Vorrang und Neustart
// fragen das Passwort erneut ab, nehmen die Laufsperre und landen im
// Protokoll.

// Tuner ruft tune.sh auf.
type Tuner interface {
	Status(ctx context.Context) (tuning.Status, error)
	Preview(ctx context.Context, opt tuning.Options) (tuning.Plan, error)
	Apply(ctx context.Context, opt tuning.Options, yes bool, stdin io.Reader, out io.Writer) (tuning.Outcome, error)
	RestorePreview(ctx context.Context) (tuning.Plan, error)
	Restore(ctx context.Context, out io.Writer) (tuning.Outcome, error)
	Prio(ctx context.Context, unit, level string, out io.Writer) (tuning.Outcome, error)
	Services(ctx context.Context) ([]tuning.Unit, error)
	Reboot(ctx context.Context) error
}

// TuneTimeout begrenzt Anwenden und Zurücknehmen (update-grub inklusive).
// Abbrechen gibt es nicht: Ein halb angewendetes Tuning ist schlechter als
// ein fertiges, und restore holt alles zurück.
const TuneTimeout = 20 * time.Minute

// tuneStatusAge: So lange gilt ein gelesener Status, bevor HostDeck tune.sh
// erneut fragt.
const tuneStatusAge = 5 * time.Second

// RebootDelay ist die Frist zwischen Antwort und Neustart, damit der
// Browser die Antwort noch bekommt.
var RebootDelay = 2 * time.Second

// RebootGiveUp: Startet der Server so lange nach einem erfolgreichen
// systemctl reboot nicht neu, gibt HostDeck die Sperre frei und meldet das.
var RebootGiveUp = 10 * time.Minute

// TuneReboot ist ein angestoßener Neustart.
type TuneReboot struct {
	At    time.Time `json:"seit"`
	Error string    `json:"fehler,omitempty"`
}

// TuneReply ist die Antwort auf GET /api/tuning.
type TuneReply struct {
	Version  string          `json:"version"`
	Status   *tuning.Status  `json:"stand,omitempty"`
	Error    string          `json:"fehler,omitempty"`
	Presets  []tuning.Preset `json:"presets"`
	Switches []tuning.Switch `json:"schalter"`
	Job      *BackupJob      `json:"auftrag,omitempty"`
	Reboot   *TuneReboot     `json:"neustart,omitempty"`
	Old      *TuneOld        `json:"alter_server,omitempty"`
}

// TuneOld ist das Tuning des alten Servers (Umzug). Der
// Leitstand bietet es hier mit Vorschau an; von selbst ändert sich nichts.
type TuneOld struct {
	From string `json:"von,omitempty"`
	// Applied: womit tune.sh dort lief; Offer: Preset und abweichende
	// Schalter dafür, ohne die Netzwerkkarte. Same: hier steht schon
	// dasselbe.
	Applied *tuning.Applied  `json:"angewendet,omitempty"`
	Offer   *tuning.Options  `json:"angebot,omitempty"`
	Same    bool             `json:"gleich,omitempty"`
	Prio    []tuning.PrioSet `json:"vorrang,omitempty"`
}

// tuneOld stellt das Angebot zusammen; nil: kein Umzug hierher oder dort
// kein Tuning.
func (d *Dienst) tuneOld(here *tuning.Status) *TuneOld {
	if d.OldTuning == nil {
		return nil
	}
	from, ap, prio := d.OldTuning()
	o := &TuneOld{From: from, Prio: prio}
	if ap != nil {
		if _, ok := tuning.PresetByName(ap.Preset); ok {
			off := ap.Offer()
			o.Applied, o.Offer = ap, &off
			o.Same = here != nil && ap.Same(here.Applied)
		}
	}
	if o.Applied == nil && len(o.Prio) == 0 {
		return nil
	}
	return o
}

type tuneCache struct {
	st  *tuning.Status
	err string
	at  time.Time
}

func (d *Dienst) tuneReply(ctx context.Context, from int) TuneReply {
	rep := TuneReply{Version: tuning.Version, Presets: tuning.Presets, Switches: tuning.Switches}
	d.mu.Lock()
	running := d.trun != nil && d.trun.job.Running
	c := d.tcache
	d.mu.Unlock()
	// Während Anwenden oder Zurücknehmen bleibt der letzte Stand stehen;
	// danach liest HostDeck neu.
	if !running && (c.st == nil && c.err == "" || d.now().Sub(c.at) > tuneStatusAge) {
		st, err := d.Tuning.Status(ctx)
		c = tuneCache{at: d.now()}
		if err != nil {
			c.err = "Den Stand des Tunings konnte HostDeck nicht lesen: " + err.Error()
		} else {
			c.st = &st
		}
		d.mu.Lock()
		d.tcache = c
		d.mu.Unlock()
	}
	rep.Status, rep.Error = c.st, c.err
	rep.Old = d.tuneOld(c.st)
	d.mu.Lock()
	rep.Job = runSnapshot(d.trun, from)
	if d.treboot != nil {
		r := *d.treboot
		rep.Reboot = &r
	}
	d.mu.Unlock()
	return rep
}

// tuneStale lässt den nächsten Abruf neu lesen; bis dahin bleibt der alte
// Stand sichtbar.
func (d *Dienst) tuneStale() {
	d.mu.Lock()
	d.tcache.at = time.Time{}
	d.mu.Unlock()
}

// handleTuneDropOld: Das Tuning vom alten Server willst du hier nicht.
// Ändert nur das Angebot, darum ohne Passwort.
func (d *Dienst) handleTuneDropOld(w http.ResponseWriter, r *http.Request) {
	if d.DropOldTuning == nil {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Kein Tuning vom alten Server."})
		return
	}
	if err := d.DropOldTuning(); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	gate, addr := visitor(r)
	d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: "Tuning vom alten Server nicht übernommen.", Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, d.tuneReply(r.Context(), 0))
}

func tuneStatus(err error) int {
	if errors.Is(err, tuning.ErrOptions) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func (d *Dienst) handleTune(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("ab"))
	writeJSON(w, http.StatusOK, d.tuneReply(r.Context(), from))
}

func (d *Dienst) handleTunePlan(w http.ResponseWriter, r *http.Request) {
	var opt tuning.Options
	if !decode(w, r, &opt) {
		return
	}
	p, err := d.Tuning.Preview(r.Context(), opt)
	if err != nil {
		writeJSON(w, tuneStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (d *Dienst) handleTuneRestorePlan(w http.ResponseWriter, r *http.Request) {
	p, err := d.Tuning.RestorePreview(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type tuneApplyRequest struct {
	tuning.Options
	// Understood: Der Warntext zu mitigations=off und Co. ist gelesen
	// („Ich habe verstanden“). Ersetzt die Abfrage des Skripts.
	Understood bool `json:"verstanden"`
}

// tuneLock nimmt die Laufsperre; false: Antwort ist schon geschrieben.
func (d *Dienst) tuneLock(w http.ResponseWriter, what string) (*lauf.Lock, bool) {
	d.mu.Lock()
	busy := d.trun != nil && d.trun.job.Running
	d.mu.Unlock()
	if busy {
		writeJSON(w, http.StatusConflict, apiError{Error: "Das Tuning arbeitet gerade. Bitte warten, bis es fertig ist."})
		return nil, false
	}
	l, err := lauf.TakeIn(d.RunDir, what)
	var be *lauf.BusyError
	switch {
	case errors.As(err, &be):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return nil, false
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return nil, false
	}
	return l, true
}

func (d *Dienst) handleTuneApply(w http.ResponseWriter, r *http.Request) {
	var req tuneApplyRequest
	if !decode(w, r, &req) {
		return
	}
	opt := req.Options
	if err := opt.Check(); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	// Die Vorschau sagt, ob das Skript nachfragen würde; HostDeck fragt dann
	// selbst (Warntext und Häkchen im Leitstand) und ruft mit --yes auf.
	p, err := d.Tuning.Preview(r.Context(), opt)
	if err != nil {
		writeJSON(w, tuneStatus(err), apiError{Error: err.Error()})
		return
	}
	if p.Code != 0 {
		writeJSON(w, http.StatusConflict, apiError{Error: "Die Vorschau von tune.sh meldet einen Fehler: " + p.Problem})
		return
	}
	if len(p.Risky) > 0 && !req.Understood {
		writeJSON(w, http.StatusConflict, apiError{Error: "Bitte erst den Warntext lesen und „Ich habe verstanden“ ankreuzen: " + strings.ReplaceAll(p.RiskText, "\n", " ")})
		return
	}
	text := "Tuning anwenden: " + strings.Join(opt.Args()[1:], " ")
	d.tuneJob(w, r, text, "anwenden", func(ctx context.Context, out io.Writer) (tuning.Outcome, error) {
		return d.Tuning.Apply(ctx, opt, true, nil, out)
	}, func(o tuning.Outcome) string {
		return "Tuning angewendet (" + strings.Join(opt.Args()[1:], " ") + ")" + rebootText(o, "Die Bootparameter wirken nach einem Neustart.")
	})
}

func (d *Dienst) handleTuneRestore(w http.ResponseWriter, r *http.Request) {
	d.tuneJob(w, r, "Tuning zurücknehmen (restore)", "zuruecknehmen", func(ctx context.Context, out io.Writer) (tuning.Outcome, error) {
		return d.Tuning.Restore(ctx, out)
	}, func(o tuning.Outcome) string {
		return "Tuning zurückgenommen" + rebootText(o, "Für die Bootparameter ist ein Neustart nötig.")
	})
}

func rebootText(o tuning.Outcome, reboot string) string {
	s := "."
	if o.Reboot {
		s += " " + reboot
	}
	if o.Warnings > 0 {
		s += " " + strconv.Itoa(o.Warnings) + " Warnung(en), siehe Ausgabe."
	}
	return s
}

// tuneJob startet Anwenden oder Zurücknehmen im Hintergrund.
func (d *Dienst) tuneJob(w http.ResponseWriter, r *http.Request, what, kind string, run func(context.Context, io.Writer) (tuning.Outcome, error), done func(tuning.Outcome) string) {
	l, ok := d.tuneLock(w, lauf.Tuning)
	if !ok {
		return
	}
	gate, addr := visitor(r)
	job := &backupRun{job: &BackupJob{Running: true, Kind: kind, Started: d.now(), Lines: []string{}}, addr: addr, gate: gate}
	d.mu.Lock()
	base := d.ctx
	if base == nil {
		base = context.Background()
	}
	// Bricht der Leitstand ab oder kommt die Anfrage nicht mehr an, läuft
	// das Skript zu Ende (keine halben Änderungen). Ein Stopp des Dienstes
	// (systemctl stop) beendet es dagegen mit (KillMode control-group);
	// Update und Einrichten warten darum auf die Laufsperre.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(base), TuneTimeout)
	job.cancel = cancel
	d.trun = job
	d.mu.Unlock()
	d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: clip(what), Addr: addr, Gate: gate})
	d.logf("%s", what)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		out := jobOutput{d: d, run: job}
		o, err := run(ctx, out)
		out.Write([]byte("\n"))
		l.Release()
		d.tuneStale()
		var text string
		switch {
		case err != nil:
			text = what + ": nicht gelaufen: " + err.Error()
		case o.Code != 0:
			text = fmt.Sprintf("%s: fehlgeschlagen (Code %d). %s", what, o.Code, o.Last)
		default:
			text = done(o)
		}
		d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: clip(text), Addr: addr, Gate: gate})
		d.logf("%s", text)
		now := d.now()
		d.mu.Lock()
		job.closing = true
		job.job.Running = false
		job.job.Done = &now
		job.job.Code = 0
		if err != nil {
			job.job.Code = 1
			job.job.Lines = append(job.job.Lines, "Fehler: "+err.Error())
		} else if o.Code != 0 {
			job.job.Code = 1
		}
		d.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, d.tuneReply(r.Context(), 0))
}

func (d *Dienst) handleTuneServices(w http.ResponseWriter, r *http.Request) {
	list, err := d.Tuning.Services(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Die Dienste ließen sich nicht lesen: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dienste": list})
}

type tunePrioRequest struct {
	Unit  string `json:"dienst"`
	Level string `json:"stufe"`
}

var levelText = map[string]string{"high": "hoch", "normal": "normal", "low": "niedrig"}

func (d *Dienst) handleTunePrio(w http.ResponseWriter, r *http.Request) {
	var req tunePrioRequest
	if !decode(w, r, &req) {
		return
	}
	if _, ok := tuning.Levels[req.Level]; !ok || !tuning.ValidUnit(req.Unit) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Ungültiger Dienst oder ungültige Stufe (high, normal, low)."})
		return
	}
	// „normal“ heißt für tune.sh prio: Drop-in löschen, auch ein fremdes,
	// ohne Sicherung. Darum nur für Dienste aus prio.list .
	if req.Level == "normal" {
		unit := req.Unit
		if !strings.Contains(unit, ".") {
			unit += ".service"
		}
		list, err := d.Tuning.Services(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
			return
		}
		managed := false
		for _, u := range list {
			managed = managed || (u.Name == unit && u.Managed)
		}
		if !managed {
			writeJSON(w, http.StatusConflict, apiError{Error: "Den CPU-Vorrang von " + unit + " hat nicht vps-tune gesetzt. „normal“ würde ihn ohne Sicherung löschen; HostDeck lässt ihn darum stehen."})
			return
		}
	}
	l, ok := d.tuneLock(w, lauf.Tuning)
	if !ok {
		return
	}
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	o, err := d.Tuning.Prio(ctx, req.Unit, req.Level, &buf)
	cancel()
	l.Release()
	d.tuneStale()
	gate, addr := visitor(r)
	text := "CPU-Vorrang für " + req.Unit + ": " + levelText[req.Level]
	switch {
	case err != nil:
		text += " ging nicht: " + err.Error()
	case o.Code != 0:
		text += fmt.Sprintf(" ging nicht (Code %d): %s", o.Code, o.Last)
	}
	d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: clip(text), Addr: addr, Gate: gate})
	if err != nil {
		writeJSON(w, tuneStatus(err), apiError{Error: err.Error()})
		return
	}
	list, lerr := d.Tuning.Services(r.Context())
	rep := map[string]any{"ergebnis": o, "ausgabe": buf.String()}
	if lerr == nil {
		rep["dienste"] = list
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleTuneReboot startet den Server neu, wenn Bootparameter warten.
func (d *Dienst) handleTuneReboot(w http.ResponseWriter, r *http.Request) {
	st, err := d.Tuning.Status(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	if !st.Reboot {
		writeJSON(w, http.StatusConflict, apiError{Error: "Es wartet nichts auf einen Neustart. HostDeck startet den Server nur neu, wenn Bootparameter des Tunings darauf warten."})
		return
	}
	// Die Sperre bleibt bis zum Neustart: Kein Backup oder Umzug fängt
	// in den letzten Sekunden noch an.
	l, ok := d.tuneLock(w, "Neustart des Servers")
	if !ok {
		return
	}
	gate, addr := visitor(r)
	d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: clip("Server-Neustart aus dem Leitstand: " + st.RebootWhy), Addr: addr, Gate: gate})
	d.logf("Neustart des Servers angefordert")
	d.mu.Lock()
	rb := &TuneReboot{At: d.now()}
	d.treboot = rb
	d.tlock = l
	d.mu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		time.Sleep(RebootDelay)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := d.Tuning.Reboot(ctx)
		cancel()
		if err == nil {
			// Ohne Neustart bliebe die Sperre sonst bis zum Neustart des
			// Dienstes stehen.
			wait := RebootGiveUp
			time.AfterFunc(wait, func() {
				d.mu.Lock()
				stuck := d.treboot == rb && d.tlock == l
				if stuck {
					rb.Error = "Der Server ist nicht neu gestartet. Die Sperre ist wieder frei; starte ihn von Hand neu (sudo systemctl reboot) oder versuche es hier noch einmal."
					d.tlock = nil
				}
				d.mu.Unlock()
				if stuck {
					l.Release()
					d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: "Neustart kam nicht: Der Server lief nach " + wait.String() + " noch; Sperre freigegeben.", Addr: addr, Gate: gate})
				}
			})
			return
		}
		l.Release()
		d.note(protokoll.Entry{Kind: protokoll.Leistung, Text: clip("Neustart ging nicht: " + err.Error()), Addr: addr, Gate: gate})
		d.mu.Lock()
		d.treboot.Error = "Der Neustart ging nicht: " + err.Error()
		d.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, d.tuneReply(r.Context(), 0))
}
