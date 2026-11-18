package dienst

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/officialtaxz2/hostdeck/internal/outdir"
)

// Darstellung (Einstellungen › Darstellung): wie der Leitstand aussieht.
// Sie liegt beim Dienst, damit PC und Handy gleich aussehen; der Browser
// hält eine Kopie, damit beim Laden nichts aufblitzt. Nur Aussehen, nichts
// davon ändert etwas am Server; darum ohne Passwort und ohne Protokoll.
type Darstellung struct {
	Theme    string `json:"theme"`    // dark, light, system (wie das Gerät)
	Accent   string `json:"accent"`   // Akzentfarbe
	Bg       string `json:"bg"`       // Hintergrund hinter dem Glas
	Tz       bool   `json:"tz"`       // Hintergrund nach Tageszeit tönen
	Leiste   string `json:"leiste"`   // Seitenleiste breit oder schmal
	Ecken    string `json:"ecken"`    // rund oder weniger rund
	Dichte   string `json:"dichte"`   // bequem oder kompakt
	Bewegung string `json:"bewegung"` // an oder aus (Bewegungen reduzieren)
	// Glas: geraet (wie die Systemeinstellung „Transparenz“), an (immer)
	// oder aus. Leer bei Dateien von vor dieser Wahl; gilt als geraet.
	Glas string `json:"glas,omitempty"`
}

// DefaultDarstellung ist das Aussehen ohne eigene Wahl.
var DefaultDarstellung = Darstellung{Theme: "dark", Accent: "violett", Bg: "flecken", Tz: true, Leiste: "breit", Ecken: "rund", Dichte: "bequem", Bewegung: "an", Glas: "geraet"}

var lookChoices = map[string][]string{
	"theme":    {"dark", "light", "system"},
	"accent":   {"violett", "eisblau", "gruen", "gelb", "koralle", "rosa"},
	"bg":       {"flecken", "berge", "hoehen"},
	"leiste":   {"breit", "schmal"},
	"ecken":    {"rund", "weniger"},
	"dichte":   {"bequem", "kompakt"},
	"bewegung": {"an", "aus"},
	"glas":     {"geraet", "an", "aus"},
}

// valid sagt, ob jeder Wert einer der bekannten ist.
func (l Darstellung) valid() bool {
	for k, v := range map[string]string{"theme": l.Theme, "accent": l.Accent, "bg": l.Bg, "leiste": l.Leiste, "ecken": l.Ecken, "dichte": l.Dichte, "bewegung": l.Bewegung} {
		if !slices.Contains(lookChoices[k], v) {
			return false
		}
	}
	return l.Glas == "" || slices.Contains(lookChoices["glas"], l.Glas)
}

// loadLook liest darstellung.json; nil, wenn es keine gültige gibt.
func (d *Dienst) loadLook() *Darstellung {
	if d.LookFile == "" {
		return nil
	}
	b, err := os.ReadFile(d.LookFile)
	if err != nil {
		return nil
	}
	var l Darstellung
	if json.Unmarshal(b, &l) != nil || !l.valid() {
		return nil
	}
	return &l
}

// handleLook: GET /api/darstellung. „darstellung“ ist null, solange
// niemand etwas gewählt hat; dann gilt, was der Browser schon zeigt.
func (d *Dienst) handleLook(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"darstellung": d.loadLook(), "standard": DefaultDarstellung})
}

// handleLookSet: POST /api/darstellung mit dem ganzen Aussehen.
func (d *Dienst) handleLookSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Darstellung *Darstellung `json:"darstellung"`
	}
	if !decode(w, r, &req) {
		return
	}
	if d.LookFile == "" {
		writeJSON(w, http.StatusConflict, apiError{Error: "Das geht hier nicht."})
		return
	}
	if req.Darstellung == nil || !req.Darstellung.valid() {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Unbekannte Einstellung."})
		return
	}
	b, _ := json.MarshalIndent(req.Darstellung, "", "  ")
	d.lookMu.Lock()
	defer d.lookMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(d.LookFile), 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Konnte die Darstellung nicht speichern."})
		return
	}
	if err := outdir.WritePrivate(d.LookFile, append(b, '\n')); err != nil {
		d.logf("darstellung.json: %v", err)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Konnte die Darstellung nicht speichern."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"darstellung": req.Darstellung})
}
