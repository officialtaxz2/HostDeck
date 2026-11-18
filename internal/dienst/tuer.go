package dienst

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/officialtaxz2/hostdeck/internal/anmeldung"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/sitzung"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

// Die Tür: Anmelden mit Passwort oder Einmal-Link, Anmeldungen prüfen,
// abmelden, Passwort festlegen und mit dem Passwort bestätigen. Nur der
// Dienst kennt das Passwort; die Website reicht es weiter.

// AdminAddr steht im Protokoll für Befehle von root auf dem Server.
const AdminAddr = "Server (root)"

type sessionKey struct{}

// visitor liest Eingang und Adresse, die die Website mitschickt. Nur die
// Website und root dürfen den Socket ansprechen; beide setzen sie selbst.
func visitor(r *http.Request) (gate, addr string) {
	return clip(r.Header.Get(web.GateHeader)), clip(r.Header.Get(web.AddrHeader))
}

// clip kürzt Angaben fürs Protokoll.
func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	for len(s) > 100 {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return s
}

func (d *Dienst) note(e protokoll.Entry) {
	if err := d.Journal.Add(e); err != nil {
		d.logf("Protokoll: %v", err)
	}
}

// decode liest eine kleine JSON-Anfrage.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Anfrage nicht lesbar."})
		return false
	}
	return true
}

type doorRequest struct {
	Password string `json:"passwort,omitempty"`
	Token    string `json:"token,omitempty"`
	Session  string `json:"sitzung,omitempty"`
	Active   bool   `json:"aktiv,omitempty"`
	All      bool   `json:"ueberall,omitempty"`
	Gate     string `json:"eingang,omitempty"`
	Addr     string `json:"adresse,omitempty"`
}

type sessionReply struct {
	Session string `json:"sitzung"`
}

// checkPassword prüft ein Passwort samt Sperre. Bei false hat es die Antwort
// schon geschrieben. session ist beim Bestätigen die Anmeldung: Dort gilt
// statt der Bremse für alle eine Sperre je Anmeldung, damit Fremde mit
// Fehlversuchen dem Besitzer nicht auch das Bestätigen verbauen. Nach fünf
// falschen Bestätigungen ist die Anmeldung beendet.
func (d *Dienst) checkPassword(w http.ResponseWriter, r *http.Request, pw, gate, addr, what, session string) bool {
	if d.Password == nil || !d.Password.IsSet() {
		writeJSON(w, http.StatusConflict, apiError{Error: web.ErrNoPassword.Error()})
		return false
	}
	global := gate != d.EmergencyGate && session == ""
	key := lockKey(addr)
	locked := func(wait time.Duration) bool {
		e := &web.LockedError{Wait: wait}
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": e.Error(), "warten": int(math.Ceil(wait.Seconds()))})
		return false
	}
	if wait := d.Lockout.Begin(key, global); wait > 0 {
		return locked(wait)
	}
	skey := ""
	if session != "" {
		h := sha256.Sum256([]byte(session))
		skey = "Anmeldung " + hex.EncodeToString(h[:8])
		if wait := d.Lockout.Begin(skey, false); wait > 0 {
			d.Lockout.Cancel(key, global)
			return locked(wait)
		}
	}
	ok, err := d.Password.Verify(r.Context(), pw)
	if err != nil {
		// Nicht geprüft: zählt weder als Fehlversuch noch als Erfolg.
		d.Lockout.Cancel(key, global)
		if skey != "" {
			d.Lockout.Cancel(skey, false)
		}
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return false
	}
	addrLocked := d.Lockout.End(key, global, ok)
	sessLocked := skey != "" && d.Lockout.End(skey, false, ok)
	if ok {
		return true
	}
	d.note(protokoll.Entry{Kind: protokoll.Fehlversuch, Text: "Falsches Passwort (" + what + ")", Addr: addr, Gate: gate})
	if addrLocked {
		d.note(protokoll.Entry{Kind: protokoll.Gesperrt, Text: fmt.Sprintf("Anmelden für %d Minuten gesperrt nach %d Fehlversuchen", int(anmeldung.LockFor.Minutes()), anmeldung.MaxFails), Addr: addr, Gate: gate})
	}
	if sessLocked {
		d.Sessions.Drop(session)
		d.note(protokoll.Entry{Kind: protokoll.Abmeldung, Text: fmt.Sprintf("Abgemeldet nach %d falschen Bestätigungen", anmeldung.MaxFails), Addr: addr, Gate: gate})
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Zu oft ein falsches Passwort. Bitte neu anmelden.", "anmelden": true})
		return false
	}
	writeJSON(w, http.StatusUnauthorized, apiError{Error: web.ErrBadPassword.Error()})
	return false
}

// lockKey ist der Schlüssel für die Sperre: die Adresse, bei IPv6 das
// ganze /64-Netz. Ein Anschluss bekommt meist ein ganzes /64, darin
// kostet jede neue Adresse nichts.
func lockKey(addr string) string { return web.NetKey(addr) }

func (d *Dienst) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req doorRequest
	if !decode(w, r, &req) {
		return
	}
	gate, addr := clip(req.Gate), clip(req.Addr)
	if !d.checkPassword(w, r, req.Password, gate, addr, "Anmeldung", "") {
		return
	}
	id, err := d.Sessions.NewSession(sitzung.ViaPassword, gate, addr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	d.note(protokoll.Entry{Kind: protokoll.Anmeldung, Text: "Angemeldet mit Passwort", Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, sessionReply{Session: id})
}

func (d *Dienst) handleRedeem(w http.ResponseWriter, r *http.Request) {
	var req doorRequest
	if !decode(w, r, &req) {
		return
	}
	gate, addr := clip(req.Gate), clip(req.Addr)
	if d.EmergencyGate != "" && gate != d.EmergencyGate {
		// Einmal-Links gelten nur am Notausgang, nie über Caddy.
		d.note(protokoll.Entry{Kind: protokoll.Fehlversuch, Text: "Einmal-Link außerhalb des Notausgangs abgelehnt", Addr: addr, Gate: gate})
		writeJSON(w, http.StatusForbidden, apiError{Error: "Link ungültig oder abgelaufen."})
		return
	}
	if !d.Redeem(req.Token) {
		d.logf("Einmal-Link abgelehnt")
		d.note(protokoll.Entry{Kind: protokoll.Fehlversuch, Text: "Einmal-Link abgelehnt (falsch, abgelaufen oder schon benutzt)", Addr: addr, Gate: gate})
		writeJSON(w, http.StatusForbidden, apiError{Error: "Link ungültig oder abgelaufen."})
		return
	}
	id, err := d.Sessions.NewSession(sitzung.ViaLink, gate, addr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	d.logf("Einmal-Link eingelöst")
	d.note(protokoll.Entry{Kind: protokoll.Anmeldung, Text: "Angemeldet mit Einmal-Link", Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, sessionReply{Session: id})
}

func (d *Dienst) handleCheckSession(w http.ResponseWriter, r *http.Request) {
	var req doorRequest
	if !decode(w, r, &req) {
		return
	}
	if _, err := d.Sessions.Check(req.Session, req.Active); err != nil {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Nicht angemeldet."})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *Dienst) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req doorRequest
	if !decode(w, r, &req) {
		return
	}
	gate, addr := clip(req.Gate), clip(req.Addr)
	info, err := d.Sessions.Check(req.Session, false)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if req.All {
		n := d.Sessions.DropAll()
		d.note(protokoll.Entry{Kind: protokoll.Abmeldung, Text: abgemeldetText(n), Addr: addr, Gate: gate})
	} else {
		d.Sessions.Drop(req.Session)
		d.note(protokoll.Entry{Kind: protokoll.Abmeldung, Text: "Abgemeldet (angemeldet seit " + info.Created.Local().Format("02.01. 15:04") + ")", Addr: addr, Gate: gate})
	}
	w.WriteHeader(http.StatusNoContent)
}

// PasswordState ist die Antwort auf GET /intern/passwort.
type PasswordState struct {
	Set   bool       `json:"gesetzt"`
	SetAt *time.Time `json:"seit,omitempty"`
}

func (d *Dienst) handlePasswordState(w http.ResponseWriter, _ *http.Request) {
	var st PasswordState
	if d.Password != nil {
		if t, err := d.Password.SetAt(); err == nil {
			st.Set, st.SetAt = true, &t
		}
	}
	writeJSON(w, http.StatusOK, st)
}

func (d *Dienst) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	var req doorRequest
	if !decode(w, r, &req) {
		return
	}
	if d.Password == nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Passwort ist hier nicht eingerichtet."})
		return
	}
	if err := d.Password.Set(req.Password); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: err.Error()})
		return
	}
	n := d.Sessions.DropAll()
	d.Lockout.Reset()
	d.logf("Leitstand-Passwort geändert, %d Anmeldungen abgemeldet", n)
	d.note(protokoll.Entry{Kind: protokoll.Passwort, Text: fmt.Sprintf("Leitstand-Passwort festgelegt; alle abgemeldet (%d), Sperren aufgehoben", n), Addr: AdminAddr})
	w.WriteHeader(http.StatusNoContent)
}

// session lässt nur angemeldete Anfragen der Website durch; confirm
// verlangt zusätzlich eine frische Bestätigung mit dem Passwort.
func (d *Dienst) session(confirm bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, err := d.Sessions.Check(r.Header.Get(web.SessionHeader), false)
		if gate, _ := visitor(r); err == nil && info.Gate != gate {
			// Eine Anmeldung gilt nur am Eingang, an dem sie entstand. Sonst
			// ließe sich ein Cookie vom Notausgang (dort per Link, also
			// ohne Bestätigung) über die öffentliche Adresse benutzen.
			err = sitzung.ErrUnknown
		}
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Die Anmeldung ist abgelaufen. Bitte neu anmelden.", "anmelden": true})
			return
		}
		if confirm && !d.Sessions.Confirmed(info) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "Bitte bestätige diesen Schritt mit deinem Leitstand-Passwort.", "bestaetigen": true})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, info)))
	}
}

func (d *Dienst) handleConfirm(w http.ResponseWriter, r *http.Request) {
	var req doorRequest
	if !decode(w, r, &req) {
		return
	}
	gate, addr := visitor(r)
	if !d.checkPassword(w, r, req.Password, gate, addr, "Bestätigung", r.Header.Get(web.SessionHeader)) {
		return
	}
	until, err := d.Sessions.MarkConfirmed(r.Header.Get(web.SessionHeader))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Die Anmeldung ist abgelaufen. Bitte neu anmelden.", "anmelden": true})
		return
	}
	d.note(protokoll.Entry{Kind: protokoll.Bestaetigung, Text: "Mit Passwort bestätigt (gilt 5 Minuten)", Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, map[string]any{"bestaetigt_bis": until})
}

// SessionView ist die Antwort auf GET /api/sitzung.
type SessionView struct {
	sitzung.Info
	Confirmed   bool `json:"bestaetigt"`
	PasswordSet bool `json:"passwort_gesetzt"`
	Count       int  `json:"anmeldungen"`
}

func (d *Dienst) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	info, _ := r.Context().Value(sessionKey{}).(sitzung.Info)
	v := SessionView{Info: info, Confirmed: d.Sessions.Confirmed(info), Count: d.Sessions.Count()}
	v.PasswordSet = d.Password != nil && d.Password.IsSet()
	writeJSON(w, http.StatusOK, v)
}

func (d *Dienst) handleJournal(w http.ResponseWriter, _ *http.Request) {
	es, err := d.Journal.Last(500)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Protokoll nicht lesbar: " + err.Error()})
		return
	}
	if es == nil {
		es = []protokoll.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"eintraege": es})
}

// handleSave reicht das Speichern der Auswahl weiter und schreibt die
// Änderungen ins Protokoll.
func (d *Dienst) handleSave(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, apiError{Error: "Anfrage zu groß."})
		return
	}
	var req web.SaveRequest
	_ = json.Unmarshal(body, &req)
	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := &recorder{ResponseWriter: w, code: http.StatusOK}
	d.Data.ServeHTTP(rec, r)
	if rec.code != http.StatusOK {
		return
	}
	gate, addr := visitor(r)
	details := make([]string, 0, len(req.Edits))
	for _, e := range req.Edits {
		details = append(details, describeEdit(e))
	}
	d.note(protokoll.Entry{Kind: protokoll.Auswahl, Text: "Auswahl gespeichert (" + count(len(req.Edits), "Änderung", "Änderungen") + ")", Addr: addr, Gate: gate, Details: details})
}

type recorder struct {
	http.ResponseWriter
	code int
}

func (r *recorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// describeEdit beschreibt eine Änderung an der Auswahl in einer Zeile.
func describeEdit(e web.Edit) string {
	var parts []string
	if e.Decision != nil {
		parts = append(parts, "Entscheidung: "+decisionWord(*e.Decision))
	}
	if e.Image != nil {
		parts = append(parts, "Image: "+*e.Image)
	}
	if e.Strategy != nil {
		parts = append(parts, "Sicherung: "+*e.Strategy)
	}
	if e.Reason != nil {
		r := clip(*e.Reason)
		if r == "" {
			parts = append(parts, "Grund entfernt")
		} else {
			parts = append(parts, "Grund: „"+r+"“")
		}
	}
	if e.SplitOf != "" {
		parts = append(parts, "eigener Unterordner von "+clip(e.SplitOf))
	}
	if e.Remove {
		parts = append(parts, "entfernt")
	}
	if len(parts) == 0 {
		parts = append(parts, "geändert")
	}
	return clip(e.Key) + ": " + strings.Join(parts, ", ")
}

func decisionWord(s string) string {
	switch s {
	case "take":
		return "kommt mit"
	case "archive":
		return "ins Archiv"
	case "skip":
		return "bleibt zurück"
	case "open":
		return "offen"
	}
	return s
}

// limitBody begrenzt Anfragen auf 1 MiB, ein Update auf web.MaxUpload.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(1 << 20)
		if r.URL.Path == web.UploadPath {
			limit = web.MaxUpload
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

func (d *Dienst) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if d.Update == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "Updates im Leitstand gehen hier nicht."})
		return
	}
	gate, addr := visitor(r)
	h, err := d.Update(r.Context(), r.Body)
	if err != nil {
		code := http.StatusUnprocessableEntity
		var busy *lauf.BusyError
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &busy):
			code = http.StatusConflict
		case errors.As(err, &tooBig):
			code = http.StatusRequestEntityTooLarge
			err = errors.New("Die Datei ist zu groß für ein Update von HostDeck.")
		}
		d.note(protokoll.Entry{Kind: protokoll.Update, Text: "Update abgelehnt: " + err.Error(), Addr: addr, Gate: gate})
		writeJSON(w, code, apiError{Error: err.Error()})
		return
	}
	d.logf("Update auf %s angenommen, wird eingespielt", h.Version)
	d.note(protokoll.Entry{Kind: protokoll.Update, Text: fmt.Sprintf("Update auf Version %s hochgeladen (läuft: %s), Signatur geprüft, wird eingespielt", h.Version, d.Version), Addr: addr, Gate: gate})
	writeJSON(w, http.StatusAccepted, map[string]string{"version": h.Version})
}

// count schreibt eine Anzahl mit dem passenden Wort.
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// abgemeldetText beschreibt „Überall abmelden“ fürs Protokoll.
func abgemeldetText(n int) string {
	if n == 1 {
		return "Überall abgemeldet (1 Anmeldung)"
	}
	return fmt.Sprintf("Überall abgemeldet (%d Anmeldungen)", n)
}
