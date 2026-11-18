// Package web ist die Auswahlseite von hostdeck serve. Sie zeigt das Inventar
// und schreibt die Entscheidungen in manifest.yaml.
//
// Sicherheit: hostdeck serve lauscht nur auf 127.0.0.1 und ist von außen nur
// über einen SSH-Tunnel erreichbar. Die Seite selbst enthält keine Daten.
// Der Schlüssel, den hostdeck serve beim Start ausgibt, steht im Link hinter dem
// #, wird also nie mitgeschickt und landet in keinem Log. Die Seite tauscht
// ihn gegen ein Cookie (HttpOnly, SameSite=Strict); jede Anfrage an /api
// braucht dieses Cookie. Fremde Hostnamen (DNS-Rebinding) und fremde
// Origins werden abgewiesen. Als Dienst (hostdeck web) gilt dasselbe, nur kommt
// der Link dort von hostdeck notausgang und gilt einmal.
package web

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/runlog"
	"github.com/officialtaxz2/hostdeck/internal/sitzung"
)

//go:embed static
var static embed.FS

// Go kennt die Endung der Schriftdateien nicht; ohne Inhaltstyp lehnt der
// Browser sie wegen nosniff ab.
func init() {
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

// Dateinamen im Ergebnisordner.
const (
	InventoryFile = "inventory.json"
	ManifestFile  = "manifest.yaml"
	InvalidFile   = "manifest.yaml.ungueltig"
)

const maxBody = 1 << 20

type Server struct {
	Dir   string    // Ergebnisordner von hostdeck discover
	Token string    // Einmal-Schlüssel für /api
	Log   io.Writer // kurze Meldungen fürs Terminal
	Now   func() time.Time

	mu       sync.Mutex // ein Speichervorgang zur Zeit
	once     sync.Once
	sessions *sitzung.Store
}

// NewToken erzeugt einen zufälligen Schlüssel mit 128 Bit.
func NewToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ErrNoInventory: discover ist noch nicht gelaufen.
var ErrNoInventory = errors.New("im Ergebnisordner liegt noch keine Bestandsaufnahme (inventory.json)")

// Check prüft vor dem Start, ob sich der Ordner laden lässt.
func (s *Server) Check() error {
	_, err := s.load()
	return err
}

// Handler liefert die Seite für hostdeck serve: nur über 127.0.0.1 oder
// localhost, angemeldet per Link aus dem Terminal.
func (s *Server) Handler() http.Handler {
	s.once.Do(func() { s.sessions = sitzung.New("") })
	s.sessions.Now = s.Now
	f := &Front{
		API: s.DataHandler(), Bearer: s.Token, Log: s.Log, Now: s.Now,
		Door: &MemoryDoor{Store: s.sessions, Link: func(t string) bool {
			// Bei hostdeck serve gilt der Link, solange hostdeck serve läuft.
			return s.Token != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.Token)) == 1
		}},
	}
	return f.Handler(Gate{
		Name: "hostdeck serve", Hosts: LoopbackHost, Cookie: "hostdeck",
		Hint: "Diese Seite braucht den Link aus dem Terminal. Öffne genau die Adresse, die hostdeck serve anzeigt (mit dem Teil nach #).",
	})
}

// DataHandler bedient /api/state und /api/save ohne eigene Anmeldung. Davor
// steht immer etwas, das die Anmeldung prüft: Front bei hostdeck serve, der
// Unix-Socket des Dienstes bei hostdeck web.
func (s *Server) DataHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/save", s.handleSave)
	return mux
}

type apiError struct {
	Error    string   `json:"error"`
	Problems []string `json:"problems,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// loaded ist der aktuelle Stand auf der Platte.
type loaded struct {
	inv      *inventory.Inventory
	m        *manifest.Manifest // mit neuen Einträgen aus dem Vorschlag ergänzt
	proposal *manifest.Manifest
	view     ManifestView
	gone     []string
	version  string
}

func (s *Server) load() (*loaded, error) {
	invBytes, err := os.ReadFile(filepath.Join(s.Dir, InventoryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoInventory
	}
	if err != nil {
		return nil, err
	}
	var inv inventory.Inventory
	if err := json.Unmarshal(invBytes, &inv); err != nil {
		return nil, fmt.Errorf("%s ist beschädigt: %v", InventoryFile, err)
	}
	if inv.Version != inventory.SchemaVersion {
		return nil, fmt.Errorf("%s hat Version %d, erwartet %d; bitte hostdeck discover neu ausführen", InventoryFile, inv.Version, inventory.SchemaVersion)
	}
	manBytes, err := os.ReadFile(filepath.Join(s.Dir, ManifestFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	l := &loaded{inv: &inv, proposal: manifest.Propose(&inv)}
	sum := sha256.New()
	sum.Write(invBytes)
	sum.Write([]byte{0})
	sum.Write(manBytes)
	l.version = hex.EncodeToString(sum.Sum(nil))[:16]

	switch {
	case manBytes == nil:
		l.m, l.view.State = manifest.Propose(&inv), ManifestMissing
	default:
		if fi, err := os.Stat(filepath.Join(s.Dir, ManifestFile)); err == nil {
			t := fi.ModTime()
			l.view.Saved = &t
		}
		m, perr := manifest.Parse(bytes.NewReader(manBytes))
		if perr != nil {
			l.m, l.view.State = manifest.Propose(&inv), ManifestInvalid
			var ve *manifest.ValidationError
			if errors.As(perr, &ve) {
				l.view.Problems = ve.Problems
			} else {
				l.view.Problems = []string{perr.Error()}
			}
			break
		}
		l.m, l.view.State = m, ManifestOK
		l.gone = manifest.Missing(manifest.Compare(m, &inv).Gone)
		l.view.Added = manifest.AddNew(m, l.proposal)
		manifest.RefreshNotes(m, l.proposal)
	}
	if l.view.Added == nil {
		l.view.Added = []string{}
	}
	return l, nil
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	l, err := s.load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.state(l))
}

func (s *Server) state(l *loaded) *State {
	st := buildState(l.inv, l.m, l.view, l.gone)
	st.Version = l.version
	// Die Liste der Backup-Läufe ist nur Anzeige; ist sie kaputt oder nicht
	// lesbar, zeigt die Seite das, statt ganz zu scheitern.
	runs, err := runlog.Load(s.Dir)
	if err != nil {
		st.BackupsNote = runlog.File + " ist nicht lesbar: " + err.Error()
		runs = []runlog.Entry{}
	}
	st.Backups = runs
	return st
}

// SaveRequest enthält nur die Änderungen gegenüber dem geladenen Stand.
type SaveRequest struct {
	Version string `json:"version"`
	Edits   []Edit `json:"edits"`
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, apiError{Error: "Erwartet JSON."})
		return
	}
	var req SaveRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Anfrage nicht lesbar: " + err.Error()})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	if req.Version != l.version {
		writeJSON(w, http.StatusConflict, apiError{Error: "manifest.yaml oder inventory.json hat sich inzwischen geändert (etwa durch einen neuen hostdeck discover oder eine Änderung von Hand). Lade die Seite neu; deine ungespeicherten Änderungen gehen dabei verloren."})
		return
	}
	var problems []string
	for _, e := range req.Edits {
		if err := apply(l, e); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) == 0 {
		// Info-Felder auf den aktuellen Stand bringen.
		l.m.System = l.proposal.System
		if err := l.m.Validate(); err != nil {
			var ve *manifest.ValidationError
			if errors.As(err, &ve) {
				problems = ve.Problems
			} else {
				problems = []string{err.Error()}
			}
		}
	}
	if len(problems) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Nicht gespeichert:", Problems: problems})
		return
	}
	out, err := l.m.Marshal()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	mPath := filepath.Join(s.Dir, ManifestFile)
	if l.view.State == ManifestInvalid {
		if err := os.Rename(mPath, filepath.Join(s.Dir, InvalidFile)); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{Error: "ungültiges manifest.yaml ließ sich nicht beiseitelegen: " + err.Error()})
			return
		}
	}
	if err := outdir.WritePrivate(mPath, out); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	outdir.GiveToSudoUser(s.Dir, ManifestFile, InvalidFile)
	s.logf("manifest.yaml gespeichert (%d Änderungen)", len(req.Edits))

	l, err = s.load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.state(l))
}

func (s *Server) logf(format string, args ...any) {
	if s.Log == nil {
		return
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	fmt.Fprintf(s.Log, "%s  %s\n", now().Format("15:04:05"), fmt.Sprintf(format, args...))
}
