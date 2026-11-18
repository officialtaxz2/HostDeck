package dienst

import (
	"context"
	"net/http"
)

// Die Server-Ebene der Seite Umzug: was außer den Projekten
// mitkommt. Zeitzone und Kernel-Einstellungen kommen von selbst mit;
// zeitgesteuerte Aufgaben und Zugänge nur, wenn sie angekreuzt sind. Der
// Probe-Umzug prüft sie auf dem neuen Server nur, übernommen werden sie am
// Umzugstag.

// ServerItem ist ein Punkt der Server-Ebene, ohne Inhalte.
type ServerItem struct {
	Key        string `json:"schluessel"`
	Kind       string `json:"art"` // automatisch, aufgabe, zugang
	Title      string `json:"titel"`
	Detail     string `json:"detail,omitempty"`
	Manual     string `json:"von_hand,omitempty"` // warum HostDeck das nicht übernimmt
	Selectable bool   `json:"waehlbar"`
}

// ServerLevel ist die Antwort auf GET /api/umzug/server-ebene.
type ServerLevel struct {
	Items   []ServerItem `json:"punkte"`
	Chosen  []string     `json:"angekreuzt"`
	Rest    []string     `json:"von_hand"` // geänderte Dateien in /etc, die HostDeck nicht übernimmt
	Problem string       `json:"problem,omitempty"`
}

// MoveServer kann Moves zusätzlich: die Server-Ebene.
type MoveServer interface {
	ServerLevel(ctx context.Context) ServerLevel
	ChooseServer(keys []string) error
}

// handleServerLevel: GET /api/umzug/server-ebene
func (d *Dienst) handleServerLevel(w http.ResponseWriter, r *http.Request) {
	m, ok := d.Moves.(MoveServer)
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Die Server-Ebene gibt es hier nicht."})
		return
	}
	writeJSON(w, http.StatusOK, m.ServerLevel(r.Context()))
}

// handleServerChoose: POST /api/umzug/server-ebene {"angekreuzt": [...]}
func (d *Dienst) handleServerChoose(w http.ResponseWriter, r *http.Request) {
	m, ok := d.Moves.(MoveServer)
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Die Server-Ebene gibt es hier nicht."})
		return
	}
	var req struct {
		Chosen []string `json:"angekreuzt"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Chosen) > 500 {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Zu viele Punkte."})
		return
	}
	if err := m.ChooseServer(req.Chosen); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m.ServerLevel(r.Context()))
}
