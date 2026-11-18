package dienst

import (
	"context"
	"net/http"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/hinweise"
	"github.com/officialtaxz2/hostdeck/internal/neueversion"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// NeueVersionReply ist die Antwort auf GET /api/versionshinweis.
type NeueVersionReply struct {
	// On: null, solange niemand gewählt hat (gilt als aus).
	On      *bool      `json:"an"`
	Running string     `json:"laeuft"` // die Version auf dem Server
	Checked *time.Time `json:"geprueft,omitempty"`
	Latest  string     `json:"neueste,omitempty"`
	Newer   bool       `json:"neuer"` // Latest ist neuer als Running
	// Unknown: Die laufende Version ist keine Versionsnummer (etwa eine
	// Testversion); ob Latest neuer ist, lässt sich dann nicht sagen.
	Unknown bool   `json:"unbekannt,omitempty"`
	Link    string `json:"link,omitempty"`
	Error   string `json:"fehler,omitempty"`
	Repo    string `json:"repo"`
}

func (d *Dienst) neueVersionReply() NeueVersionReply {
	st := d.Release.Load()
	r := NeueVersionReply{On: st.On, Running: d.Release.Version, Checked: st.Checked, Error: st.Error, Repo: d.releaseRepo()}
	if st.Enabled() {
		r.Latest, r.Link = st.Latest, st.Link
		r.Newer = neueversion.Newer(d.Release.Version, st.Latest)
		r.Unknown = st.Latest != "" && !neueversion.IsVersion(d.Release.Version)
	}
	return r
}

func (d *Dienst) releaseRepo() string {
	if d.Release.Repo != "" {
		return d.Release.Repo
	}
	return neueversion.Repo
}

// handleNeueVersion: GET /api/versionshinweis.
func (d *Dienst) handleNeueVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.neueVersionReply())
}

// handleNeueVersionSet: POST /api/versionshinweis mit {"an": true|false}.
// Eingeschaltet fragt HostDeck gleich einmal nach. Ändert nichts am Server,
// darum ohne Passwort; es steht im Protokoll.
func (d *Dienst) handleNeueVersionSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On *bool `json:"an"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.On == nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Angabe „an“ fehlt."})
		return
	}
	if err := d.Release.SetOn(*req.On); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Ließ sich nicht speichern: " + err.Error()})
		return
	}
	gate, addr := visitor(r)
	text := "Hinweis auf neue Versionen ausgeschaltet"
	if *req.On {
		text = "Hinweis auf neue Versionen eingeschaltet: HostDeck sieht einmal am Tag bei GitHub nach (" + d.releaseRepo() + ")"
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		_, _ = d.Release.Check(ctx)
		cancel()
	}
	d.note(protokoll.Entry{Kind: protokoll.Update, Text: text, Addr: addr, Gate: gate})
	if f, ok := d.Hints.(interface{ Forget() }); ok {
		f.Forget()
	}
	writeJSON(w, http.StatusOK, d.neueVersionReply())
}

// NeueVersionHints ist der Hinweis für Übersicht und Glocke, wenn es eine
// neuere Version gibt; sonst keiner.
func NeueVersionHints(s *neueversion.Service) func() []hinweise.Hint {
	return func() []hinweise.Hint {
		st := s.Load()
		if !st.Enabled() || !neueversion.Newer(s.Version, st.Latest) {
			return nil
		}
		return []hinweise.Hint{{
			Key: "neue-version", Level: hinweise.Warn, Icon: "updates",
			Title: "Neue Version " + st.Latest + " von HostDeck",
			Text:  "Auf dem Server läuft " + s.Version + ". Die Update-Datei liegt bei GitHub; einspielen unter Updates.",
			Link:  "#/updates", Go: "Zu den Updates",
		}}
	}
}
