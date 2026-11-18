package dienst

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// Umzugs-Lotse: Die Seite Umzug
// führt Schritt für Schritt. Fast jeder Schritt ist „erledigt“ aus Daten,
// die HostDeck ohnehin hat (Auswahl, Backup, Generalprobe, Umzugstag, DNS).
// Was sonst nirgends steht, hält lotse.json im Ordner umzug fest: ein
// Probelauf ohne Fehler, „Passt so“ für die Server-Ebene und ein bewusst
// übersprungener Schritt. Plant man einen neuen Umzug, beginnt die Datei
// neu.

// LotseFile liegt neben modus.json.
const LotseFile = "lotse.json"

// Lotse ist der Inhalt von lotse.json.
type Lotse struct {
	// Probelauf: der letzte Probelauf ohne Fehler (auch ein geglückter
	// Probe-Umzug zählt).
	Probelauf *LotseRun `json:"probelauf,omitempty"`
	// PasstSo: Server-Ebene geprüft; gilt, solange die Haken dieselben
	// sind wie beim Bestätigen.
	PasstSo      *time.Time `json:"passt_so,omitempty"`
	PasstSoHaken []string   `json:"passt_so_haken,omitempty"`
	// OhneTestadresse: Schritt „DNS vorbereiten“ bewusst übersprungen,
	// mit Begründung (etwa: der Leitstand hat keine eigene Domain).
	OhneTestadresse string `json:"ohne_testadresse,omitempty"`
	// PCKopie: Empfehlung „Backup auf den PC laden“ erledigt
	// („geladen“) oder übersprungen („uebersprungen“).
	PCKopie string `json:"pc_kopie,omitempty"`
}

// LotseRun ist ein geglückter Lauf.
type LotseRun struct {
	At     time.Time `json:"am"`
	Target string    `json:"ziel"`
	Kind   MoveKind  `json:"art"` // Probelauf oder Probe-Umzug; beide prüfen den neuen Server
}

type lotseRequest struct {
	PasstSo         *bool    `json:"passt_so,omitempty"`
	Haken           []string `json:"haken,omitempty"`
	OhneTestadresse *string  `json:"ohne_testadresse,omitempty"`
	PCKopie         *string  `json:"pc_kopie,omitempty"`
}

func (d *Dienst) lotsePath() string {
	if d.ModeFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(d.ModeFile), LotseFile)
}

func (d *Dienst) loadLotse() Lotse {
	var l Lotse
	p := d.lotsePath()
	if p == "" {
		return l
	}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return l
}

func (d *Dienst) saveLotse(l Lotse) error {
	p := d.lotsePath()
	if p == "" {
		return errors.New("Das geht hier nicht.")
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return outdir.WritePrivate(p, append(b, '\n'))
}

// resetLotse beginnt den Lotsen neu (neuer Umzug geplant).
func (d *Dienst) resetLotse() {
	if p := d.lotsePath(); p != "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			d.logf("lotse.json: %v", err)
		}
	}
}

// lotseRun hält einen geglückten Probelauf oder Probe-Umzug fest.
func (d *Dienst) lotseRun(kind MoveKind, t MoveTarget) {
	if kind != MovePlan && kind != MoveRun {
		return
	}
	d.lotseMu.Lock()
	defer d.lotseMu.Unlock()
	l := d.loadLotse()
	l.Probelauf = &LotseRun{At: d.now().UTC(), Target: t.String(), Kind: kind}
	if err := d.saveLotse(l); err != nil {
		d.logf("lotse.json: %v", err)
	}
}

func (d *Dienst) handleLotse(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.loadLotse())
}

// handleLotseSet: POST /api/umzug/lotse. Ohne Passwort wie die DNS-Liste:
// Es ändert nur, was die Seite Umzug als erledigt zeigt.
func (d *Dienst) handleLotseSet(w http.ResponseWriter, r *http.Request) {
	var req lotseRequest
	if !decode(w, r, &req) {
		return
	}
	if d.ModeFile == "" {
		writeJSON(w, http.StatusConflict, apiError{Error: "Das geht hier nicht."})
		return
	}
	if req.OhneTestadresse != nil && len(*req.OhneTestadresse) > 500 {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Die Begründung ist zu lang."})
		return
	}
	if req.PCKopie != nil && !slices.Contains([]string{"", "geladen", "uebersprungen"}, *req.PCKopie) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Unbekannter Stand."})
		return
	}
	if len(req.Haken) > 500 {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Zu viele Haken."})
		return
	}
	d.lotseMu.Lock()
	l := d.loadLotse()
	if req.PasstSo != nil {
		if *req.PasstSo {
			now := d.now().UTC()
			l.PasstSo = &now
			l.PasstSoHaken = slices.Clone(req.Haken)
			slices.Sort(l.PasstSoHaken)
		} else {
			l.PasstSo, l.PasstSoHaken = nil, nil
		}
	}
	if req.OhneTestadresse != nil {
		l.OhneTestadresse = *req.OhneTestadresse
	}
	if req.PCKopie != nil {
		l.PCKopie = *req.PCKopie
	}
	err := d.saveLotse(l)
	d.lotseMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// ---------- Abschluss auf dem neuen Server (Schritt 10) ----------

// Finish ist der Abschluss eines Umzugs auf dem neuen Server
// (umzug-abschluss.json im Datenordner). HostDeck legt die Datei beim Einzug
// an; bis alle Punkte erledigt, bewusst weggeklickt oder die Reserve-Woche
// vorbei ist, steht „Umzug abschließen“ unter „Braucht dich“ und auf der
// Seite Umzug.
type Finish struct {
	Since     time.Time            `json:"seit"`
	Until     time.Time            `json:"bis"` // Ende der Reserve-Woche
	From      string               `json:"alter_server,omitempty"`
	Test      string               `json:"testadresse,omitempty"`
	Done      map[string]time.Time `json:"erledigt"`
	Dismissed *time.Time           `json:"weggeklickt,omitempty"`
}

// Punkte des Abschlusses. FinishTest gibt es nur mit Testadresse.
const (
	FinishTabs    = "tabs"
	FinishBackup  = "backup"
	FinishTest    = "testadresse"
	FinishTTL     = "ttl"
	FinishReserve = "reserve"
)

// FinishReply ist die Antwort auf GET /api/umzug/abschluss.
type FinishReply struct {
	*Finish
	Open bool `json:"offen"`
	// TestGone: HostDeck hat die Testadresse schon aus Caddy genommen; erst
	// dann darf der DNS-Eintrag weg.
	TestGone bool     `json:"testadresse_weg"`
	Keys     []string `json:"punkte"`
}

// ReserveDays: so lange bleibt der alte Server nach dem Einzug Reserve.
const ReserveDays = 7

// NewFinish legt den Abschluss beim Einzug an.
func NewFinish(from, test string, now time.Time) Finish {
	return Finish{Since: now.UTC(), Until: now.UTC().AddDate(0, 0, ReserveDays), From: from, Test: test, Done: map[string]time.Time{}}
}

func (f *Finish) keys() []string {
	k := []string{FinishTabs, FinishBackup}
	if f.Test != "" {
		k = append(k, FinishTest)
	}
	return append(k, FinishTTL, FinishReserve)
}

func (d *Dienst) loadFinish() *Finish {
	if d.FinishFile == "" {
		return nil
	}
	b, err := os.ReadFile(d.FinishFile)
	if err != nil {
		return nil
	}
	var f Finish
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	if f.Done == nil {
		f.Done = map[string]time.Time{}
	}
	return &f
}

func (d *Dienst) finishReply() FinishReply {
	f := d.loadFinish()
	if f == nil {
		return FinishReply{Keys: []string{}}
	}
	rep := FinishReply{Finish: f, Keys: f.keys(), TestGone: true}
	if f.Test != "" && d.TestAddresses != nil {
		rep.TestGone = !slices.Contains(d.TestAddresses(), f.Test)
	}
	open := f.Dismissed == nil && d.now().Before(f.Until)
	if open {
		all := true
		for _, k := range rep.Keys {
			if _, ok := f.Done[k]; !ok {
				all = false
			}
		}
		open = !all
	}
	rep.Open = open
	return rep
}

// finishOpen sagt, ob der Abschluss noch Punkte hat (für den Stand).
func (d *Dienst) finishOpen() bool {
	return d.FinishFile != "" && d.finishReply().Open
}

// finishBackupDone hakt nach einem vollständigen Download den Punkt „Erstes
// Backup hier erstellen und herunterladen“ ab: HostDeck weiß, dass er erledigt
// ist (Labor S3). Abhaken lässt er sich weiter von Hand.
func (d *Dienst) finishBackupDone() {
	d.lotseMu.Lock()
	defer d.lotseMu.Unlock()
	f := d.loadFinish()
	if f == nil || f.Dismissed != nil {
		return
	}
	if _, ok := f.Done[FinishBackup]; ok {
		return
	}
	f.Done[FinishBackup] = d.now().UTC()
	b, _ := json.MarshalIndent(f, "", "  ")
	_ = outdir.WritePrivate(d.FinishFile, append(b, '\n'))
}

func (d *Dienst) handleWrapUp(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.finishReply())
}

type finishRequest struct {
	Key  string `json:"punkt,omitempty"`
	Done bool   `json:"erledigt"`
	// All: alle Punkte bewusst wegklicken.
	All bool `json:"alle_weg,omitempty"`
}

// handleWrapUpSet: POST /api/umzug/abschluss {"punkt": "tabs", "erledigt":
// true} oder {"alle_weg": true}.
func (d *Dienst) handleWrapUpSet(w http.ResponseWriter, r *http.Request) {
	var req finishRequest
	if !decode(w, r, &req) {
		return
	}
	d.lotseMu.Lock()
	defer d.lotseMu.Unlock()
	f := d.loadFinish()
	if f == nil {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Hier steht kein Umzug zum Abschließen an."})
		return
	}
	gate, addr := visitor(r)
	switch {
	case req.All:
		now := d.now().UTC()
		f.Dismissed = &now
		d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: "Umzug abschließen: restliche Punkte weggeklickt", Addr: addr, Gate: gate})
	case slices.Contains(f.keys(), req.Key):
		if req.Key == FinishTest && req.Done {
			if rep := d.finishReply(); !rep.TestGone {
				writeJSON(w, http.StatusConflict, apiError{Error: "HostDeck hat die Testadresse noch nicht aus Caddy genommen. Erst danach den DNS-Eintrag löschen, sonst scheitert Caddy beim Erneuern ihres Zertifikats."})
				return
			}
		}
		if req.Done {
			f.Done[req.Key] = d.now().UTC()
		} else {
			delete(f.Done, req.Key)
		}
	default:
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Unbekannter Punkt."})
		return
	}
	b, _ := json.MarshalIndent(f, "", "  ")
	if err := outdir.WritePrivate(d.FinishFile, append(b, '\n')); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d.finishReply())
}
