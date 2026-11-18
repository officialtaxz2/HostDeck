package dienst

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/officialtaxz2/hostdeck/internal/hinweise"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/leistung"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/sicherheit"
)

// Sicherheit und Leistung: erst messen, dann Vorschläge.
// Jede Änderung einzeln, mit Vorschau, Bestätigung, Laufsperre, Protokoll
// und Rückweg. Die Arbeit machen die Pakete sicherheit und leistung; beide
// sind schnell genug, um in der Anfrage zu laufen.

// Security zeigt offene Ports und schließt sie auf Wunsch.
type Security interface {
	Overview(ctx context.Context) sicherheit.Overview
	Plan(ctx context.Context, ch sicherheit.Change) (sicherheit.Plan, error)
	Apply(ctx context.Context, ch sicherheit.Change, stand string) (string, error)
	Keep(ctx context.Context) (string, error)
	Reload(ctx context.Context) (string, error)
	// KeepOpen lässt einen vorgeschlagenen Port bewusst offen (on) oder
	// nimmt das zurück; an der Firewall ändert das nichts.
	KeepOpen(ctx context.Context, key string, on bool) (string, error)
}

// Performance misst Speicher, Prozessor und Logs und ändert auf Wunsch.
type Performance interface {
	Overview(ctx context.Context) leistung.Overview
	Plan(ctx context.Context, key string) (leistung.Plan, error)
	Apply(ctx context.Context, key, stand string) (string, error)
}

// Hints sammelt die Hinweise für die Übersicht; nur anzeigen.
type Hints interface {
	Hints(ctx context.Context) hinweise.Reply
}

func (d *Dienst) handleHints(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, d.Hints.Hints(ctx))
}

// ChangeReply ist die Antwort auf eine Änderung.
type ChangeReply struct {
	Result string `json:"ergebnis"`
	// Wait: Der Leitstand muss die Änderung bis dahin bestätigen
	// (POST /api/sicherheit/behalten), sonst nimmt HostDeck sie zurück.
	Wait *time.Time `json:"bestaetigen_bis,omitempty"`
}

type securityRequest struct {
	sicherheit.Change
	Stand string `json:"stand"`
}

type perfRequest struct {
	Key   string `json:"schluessel"`
	Stand string `json:"stand"`
}

func securityStatus(err error) int {
	switch {
	case errors.Is(err, sicherheit.ErrNothing):
		return http.StatusBadRequest
	case errors.Is(err, sicherheit.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, sicherheit.ErrLocked):
		return http.StatusUnprocessableEntity
	case errors.Is(err, sicherheit.ErrChanged), errors.Is(err, sicherheit.ErrPending):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// withLock hält die Laufsperre, solange f läuft. busy: 409 ist schon
// geschrieben.
func (d *Dienst) withLock(w http.ResponseWriter, what string, f func()) {
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
	defer l.Release()
	f()
}

func (d *Dienst) handleSecurity(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, d.Security.Overview(ctx))
}

func (d *Dienst) handleSecurityPlan(w http.ResponseWriter, r *http.Request) {
	var req securityRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	p, err := d.Security.Plan(ctx, req.Change)
	if err != nil {
		writeJSON(w, securityStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (d *Dienst) handleSecurityChange(w http.ResponseWriter, r *http.Request) {
	var req securityRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Stand == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Erst die Vorschau öffnen."})
		return
	}
	gate, addr := visitor(r)
	d.withLock(w, "Ports ändern", func() {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		msg, err := d.Security.Apply(ctx, req.Change, req.Stand)
		if err != nil {
			d.noteLong(protokoll.Sicherheit, "Ports ändern ging nicht: "+err.Error(), gate, addr)
			writeJSON(w, securityStatus(err), apiError{Error: err.Error()})
			return
		}
		d.noteLong(protokoll.Sicherheit, msg, gate, addr)
		rep := ChangeReply{Result: msg}
		if p := d.Security.Overview(ctx).Pending; p != nil {
			until := p.Until
			rep.Wait = &until
		}
		writeJSON(w, http.StatusOK, rep)
	})
}

// handleSecurityKeep bestätigt eine wartende Änderung. Der Leitstand ruft
// das von selbst auf, sobald er den Dienst nach der Änderung wieder
// erreicht; ein Passwort braucht es dafür nicht.
func (d *Dienst) handleSecurityKeep(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	msg, err := d.Security.Keep(ctx)
	if err != nil {
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	}
	gate, addr := visitor(r)
	d.noteLong(protokoll.Sicherheit, msg, gate, addr)
	writeJSON(w, http.StatusOK, ChangeReply{Result: msg})
}

// keepRequest: bewusst so lassen (zurueck: das zurücknehmen).
type keepRequest struct {
	Key  string `json:"schluessel"`
	Back bool   `json:"zuruecknehmen"`
}

// forgetHints lässt „Braucht dich“ gleich neu sammeln, damit ein bewusst
// gelassener Hinweis sofort verschwindet (oder zurückkommt).
func (d *Dienst) forgetHints() {
	if f, ok := d.Hints.(interface{ Forget() }); ok {
		f.Forget()
	}
}

// handleSecurityKeepOpen: POST /api/sicherheit/offen-lassen. Ohne Passwort:
// Es ändert nichts am Server, nur ob HostDeck den Port vorschlägt; im Protokoll.
func (d *Dienst) handleSecurityKeepOpen(w http.ResponseWriter, r *http.Request) {
	var req keepRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	msg, err := d.Security.KeepOpen(ctx, req.Key, !req.Back)
	if err != nil {
		writeJSON(w, securityStatus(err), apiError{Error: err.Error()})
		return
	}
	gate, addr := visitor(r)
	d.noteLong(protokoll.Sicherheit, msg, gate, addr)
	d.forgetHints()
	writeJSON(w, http.StatusOK, ChangeReply{Result: msg})
}

func (d *Dienst) handleSecurityReload(w http.ResponseWriter, r *http.Request) {
	gate, addr := visitor(r)
	d.withLock(w, "Regeln neu laden", func() {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		msg, err := d.Security.Reload(ctx)
		if err != nil {
			writeJSON(w, securityStatus(err), apiError{Error: err.Error()})
			return
		}
		d.note(protokoll.Entry{Kind: protokoll.Sicherheit, Text: clip(msg), Addr: addr, Gate: gate})
		writeJSON(w, http.StatusOK, ChangeReply{Result: msg})
	})
}

// noteLong schreibt ein Ergebnis ins Protokoll: der erste Satz als Zeile,
// der ganze Text in den Einzelheiten, wenn er nicht in die Zeile passt.
func (d *Dienst) noteLong(kind, text, gate, addr string) {
	e := protokoll.Entry{Kind: kind, Text: clip(firstSentence(text)), Details: sentences(text), Addr: addr, Gate: gate}
	if e.Details == nil && e.Text != text {
		e.Details = []string{clipLong(text)}
	}
	d.note(e)
}

func clipLong(s string) string {
	for len(s) > 300 {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return s
}

// SecurityExpired schreibt ins Protokoll, dass HostDeck eine unbestätigte
// Änderung zurückgenommen hat.
func (d *Dienst) SecurityExpired(text string) {
	d.noteLong(protokoll.Sicherheit, text, "", AdminAddr)
	d.logf("Sicherheit: %s", text)
}

func perfStatus(err error) int {
	switch {
	case errors.Is(err, leistung.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, leistung.ErrChanged):
		return http.StatusConflict
	}
	return http.StatusUnprocessableEntity
}

var perfWhat = map[string]string{
	leistung.KeyLogs:     "Container-Logs begrenzen",
	leistung.KeyLogsBack: "Log-Grenze zurücknehmen",
	leistung.KeySwap:     "Swap-Puffer anlegen",
	leistung.KeySwapOff:  "Swap-Puffer entfernen",
}

func (d *Dienst) handlePerformance(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, d.Performance.Overview(ctx))
}

func (d *Dienst) handlePerfPlan(w http.ResponseWriter, r *http.Request) {
	var req perfRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	p, err := d.Performance.Plan(ctx, req.Key)
	if err != nil {
		writeJSON(w, perfStatus(err), apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (d *Dienst) handlePerfChange(w http.ResponseWriter, r *http.Request) {
	var req perfRequest
	if !decode(w, r, &req) {
		return
	}
	what, ok := perfWhat[req.Key]
	if !ok || req.Stand == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Unbekannter Vorschlag oder keine Vorschau."})
		return
	}
	gate, addr := visitor(r)
	d.withLock(w, what, func() {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		msg, err := d.Performance.Apply(ctx, req.Key, req.Stand)
		if err != nil {
			d.noteLong(protokoll.Leistung, what+" ging nicht: "+err.Error(), gate, addr)
			writeJSON(w, perfStatus(err), apiError{Error: err.Error()})
			return
		}
		d.noteLong(protokoll.Leistung, msg, gate, addr)
		writeJSON(w, http.StatusOK, ChangeReply{Result: msg})
	})
}
