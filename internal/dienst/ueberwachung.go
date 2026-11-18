package dienst

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/melden"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

// Monitor misst rund um die Uhr; nur anzeigen.
type Monitor interface {
	Run(ctx context.Context)
	Snapshot() ueberwachung.Snapshot
	History(rng string, patterns []string, points int, peaks bool) (ueberwachung.History, error)
	// LiveView: Messungen nach since (Unix-ms) für die Live-Ansicht;
	// startet den schnellen Takt, solange jemand fragt.
	LiveView(since int64, patterns []string) ueberwachung.LiveView
}

func (d *Dienst) handleMonitor(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.Monitor.Snapshot())
}

// handleHistory: GET /api/ueberwachung/verlauf?zeitraum=1d&serien=s/,c/*/cpu&punkte=360&spitzen=1
func (d *Dienst) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rng := q.Get("zeitraum")
	if rng == "" {
		rng = "1h"
	}
	var prefixes []string
	for _, p := range strings.Split(q.Get("serien"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			prefixes = append(prefixes, p)
		}
	}
	points, _ := strconv.Atoi(q.Get("punkte"))
	h, err := d.Monitor.History(rng, prefixes, points, q.Get("spitzen") == "1")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// handleLive: GET /api/ueberwachung/live?seit=<Unix-ms>&serien=s/,p/*/cpu
// Der Leitstand fragt jede Sekunde als Hintergrund-Abfrage; die Antwort
// enthält nur, was nach seit dazukam.
func (d *Dienst) handleLive(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("seit"), 10, 64)
	var patterns []string
	for _, p := range strings.Split(q.Get("serien"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			patterns = append(patterns, p)
		}
	}
	writeJSON(w, http.StatusOK, d.Monitor.LiveView(since, patterns))
}

// Notifier schickt Warnungen nach außen.
type Notifier interface {
	View() melden.View
	SetNtfy(server, token string, newTopic bool) (melden.View, error)
	SetBeat(addr string) (melden.View, error)
	Disable(kind string) (melden.View, error)
	Subscribe(sub melden.Subscription, name string) (melden.View, string, error)
	RemoveDevice(id string) (melden.View, string, error)
	Test(ctx context.Context, m melden.Message, only string) ([]melden.Result, error)
}

// AlertKeeper lässt laufende Warnungen bewusst so (ueberwachung.Rules).
type AlertKeeper interface {
	Accept(key string, on bool) (string, error)
}

// handleAlertKeep: POST /api/ueberwachung/so-lassen. Ohne Passwort wie die
// Grenzen: Es ändert nichts am Server, nur ob HostDeck dazu warnt; im Protokoll.
func (d *Dienst) handleAlertKeep(w http.ResponseWriter, r *http.Request) {
	var req keepRequest
	if !decode(w, r, &req) {
		return
	}
	msg, err := d.Alerts.Accept(req.Key, !req.Back)
	switch {
	case errors.Is(err, ueberwachung.ErrNoAlert):
		writeJSON(w, http.StatusNotFound, apiError{Error: err.Error()})
		return
	case errors.Is(err, ueberwachung.ErrNotAcceptable):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Konnte das nicht speichern: " + err.Error()})
		return
	}
	if msg == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ergebnis": "Schon so."})
		return
	}
	gate, addr := visitor(r)
	d.note(protokoll.Entry{Kind: protokoll.Ueberwachung, Text: clip(msg), Addr: addr, Gate: gate})
	d.forgetHints()
	writeJSON(w, http.StatusOK, map[string]any{"ergebnis": msg})
}

// LimitSetter ändert die Grenzen der Warnungen.
type LimitSetter interface {
	SetLimits(l ueberwachung.Limits) ([]string, error)
}

func (d *Dienst) handleNotify(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.Notify.View())
}

func (d *Dienst) notifyReply(w http.ResponseWriter, r *http.Request, v melden.View, err error, text string) {
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	gate, addr := visitor(r)
	d.note(protokoll.Entry{Kind: protokoll.Ueberwachung, Text: clip(text), Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, v)
}

// handleNotifyDevice: POST /api/ueberwachung/nachrichten/geraet (mit Passwort)
func (d *Dienst) handleNotifyDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sub  melden.Subscription `json:"abo"`
		Name string              `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	v, id, err := d.Notify.Subscribe(req.Sub, req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	// Nie den rohen Namen aus dem Browser ins Protokoll: gespeichert ist
	// nur die bereinigte Form.
	name := melden.CleanName(req.Name)
	gate, addr := visitor(r)
	d.note(protokoll.Entry{Kind: protokoll.Ueberwachung, Text: clip("Mitteilungen eingerichtet auf „" + name + "“"), Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, map[string]any{"stand": v, "id": id})
}

// handleNotifyRemove: POST /api/ueberwachung/nachrichten/geraet/entfernen (mit Passwort)
func (d *Dienst) handleNotifyRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	v, name, err := d.Notify.RemoveDevice(req.ID)
	d.notifyReply(w, r, v, err, "Mitteilungen ausgetragen für „"+name+"“")
}

// handleNotifyNtfy: POST /api/ueberwachung/nachrichten/ntfy (mit Passwort)
func (d *Dienst) handleNotifyNtfy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Server   string `json:"server"`
		Token    string `json:"token"`
		NewTopic bool   `json:"neues_thema"`
	}
	if !decode(w, r, &req) {
		return
	}
	v, err := d.Notify.SetNtfy(req.Server, req.Token, req.NewTopic)
	text := "ntfy eingerichtet"
	if err == nil && v.Ntfy != nil {
		text += " (" + v.Ntfy.Host + ")"
		if req.NewTopic {
			text += ", neues Thema"
		}
	}
	d.notifyReply(w, r, v, err, text)
}

// handleNotifyBeat: POST /api/ueberwachung/nachrichten/herzschlag (mit Passwort)
func (d *Dienst) handleNotifyBeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"adresse"`
	}
	if !decode(w, r, &req) {
		return
	}
	v, err := d.Notify.SetBeat(req.Address)
	text := "Herzschlag eingerichtet"
	if err == nil && v.Beat != nil {
		text += " (" + v.Beat.Address + ")"
	}
	d.notifyReply(w, r, v, err, text)
}

// handleNotifyOff: POST /api/ueberwachung/nachrichten/aus (mit Passwort)
func (d *Dienst) handleNotifyOff(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kanal"`
	}
	if !decode(w, r, &req) {
		return
	}
	v, err := d.Notify.Disable(req.Kind)
	name := map[string]string{"ntfy": "ntfy", "herzschlag": "Herzschlag"}[req.Kind]
	d.notifyReply(w, r, v, err, name+" abgeschaltet")
}

// handleNotifyTest: POST /api/ueberwachung/nachrichten/probe
func (d *Dienst) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Device string `json:"geraet,omitempty"`
	}
	if !decode(w, r, &req) {
		return
	}
	d.mu.Lock()
	wait := time.Until(d.testAt.Add(5 * time.Second))
	if wait <= 0 {
		d.testAt = time.Now()
	}
	d.mu.Unlock()
	if wait > 0 {
		writeJSON(w, http.StatusTooManyRequests, apiError{Error: "Bitte ein paar Sekunden warten, dann noch einmal."})
		return
	}
	m := melden.Message{Title: "Probe-Nachricht von HostDeck", Priority: 3, Tags: []string{"white_check_mark"}, Tag: "probe",
		Text: "So sieht eine Warnung von HostDeck aus. Wenn du das liest, kommen Warnungen bei dir an."}
	if host, _ := os.Hostname(); host != "" {
		m.Text = "So sieht eine Warnung von " + host + " aus. Wenn du das liest, kommen Warnungen bei dir an."
	}
	if d.Access != nil {
		if dom := d.Access.Domain(); dom != "" {
			m.Click = "https://" + dom + "/#/ueberwachung"
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	res, err := d.Notify.Test(ctx, m, req.Device)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ergebnisse": res, "stand": d.Notify.View()})
}

// handleLimits: POST /api/ueberwachung/grenzen (ohne Passwort, im Protokoll)
func (d *Dienst) handleLimits(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Limits   *ueberwachung.Limits `json:"grenzen,omitempty"`
		Defaults bool                 `json:"voreinstellungen,omitempty"`
	}
	if !decode(w, r, &req) {
		return
	}
	l := ueberwachung.DefaultLimits
	if !req.Defaults {
		if req.Limits == nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "Keine Grenzen geschickt."})
			return
		}
		l = *req.Limits
	}
	changes, err := d.Limits.SetLimits(l)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if len(changes) > 0 {
		gate, addr := visitor(r)
		text := "Grenzen der Warnungen geändert"
		if req.Defaults {
			text = "Grenzen der Warnungen auf Voreinstellungen zurückgesetzt"
		}
		d.note(protokoll.Entry{Kind: protokoll.Ueberwachung, Text: text, Details: changes, Addr: addr, Gate: gate})
	}
	writeJSON(w, http.StatusOK, map[string]any{"aenderungen": changes})
}

// handleEvents: GET /api/ereignisse?tage=1 – was passiert ist, neueste
// zuerst; höchstens die 30 Tage, die HostDeck aufhebt.
func (d *Dienst) handleEvents(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("tage"))
	if days <= 0 || days > 30 {
		days = 30
	}
	now := d.now()
	out := map[string]any{"ereignisse": d.Events.Since(now.Add(-time.Duration(days) * 24 * time.Hour)), "stand": now}
	// seit: ab wann HostDeck sammelt („Noch nichts passiert seit …“, „Deine Woche“).
	if b := d.Events.Begun(); !b.IsZero() {
		out["seit"] = b
	}
	writeJSON(w, http.StatusOK, out)
}
