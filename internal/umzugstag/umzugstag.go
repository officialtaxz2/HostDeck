// Package umzugstag hält den Stand des Umzugs fest: das
// Ergebnis der Generalprobe, den Ablauf des Umzugstags Schritt für Schritt
// und, ob dieser Server abgelöst ist. Alles liegt als kleine JSON-Datei im
// Ordner des Umzugs (/var/lib/hostdeck/umzug), damit Neuladen der Seite und ein
// Neustart des Dienstes nichts vergessen.
package umzugstag

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/outdir"
)

// Dateien im Ordner des Umzugs.
const (
	ProbeFile = "generalprobe.json"
	DayFile   = "umzugstag.json"
	DoneFile  = "abgeloest.json"
)

// ProbeMaxAge: So alt darf die bestandene Generalprobe am Umzugstag
// höchstens sein.
const ProbeMaxAge = 72 * time.Hour

// Probe ist das Ergebnis des letzten Probe-Umzugs (Generalprobe).
type Probe struct {
	Target   string    `json:"ziel"` // benutzer@server
	Host     string    `json:"host"` // Adresse wie eingegeben
	IP       string    `json:"ip"`   // Adresse des neuen Servers
	At       time.Time `json:"am"`   // Ende
	Run      string    `json:"lauf"` // zurückgespielter Lauf
	OK       bool      `json:"ok"`   // bestanden
	Problems []string  `json:"probleme,omitempty"`
	Details  []string  `json:"fehler,omitempty"` // Zeilen mit ✗ aus dem Zurückspielen
}

// Phasen des Umzugstags.
const (
	PhaseRunning     = "laeuft"       // der Ablauf arbeitet
	PhaseDNS         = "dns"          // alles drüben, wartet auf die DNS-Umstellung
	PhaseFailed      = "fehler"       // ein Schritt ging schief; Rückweg oder erneut
	PhaseInterrupted = "unterbrochen" // der Dienst startete mitten im Ablauf neu
	PhaseBack        = "zurueck"      // Rückweg genommen, Projekte laufen wieder hier
	PhaseDone        = "fertig"       // DNS zeigt auf den neuen Server; abgelöst
)

// Zustände eines Schritts.
const (
	StepOpen    = "offen"
	StepRunning = "laeuft"
	StepOK      = "ok"
	StepWarn    = "warnung"
	StepFailed  = "fehler"
	StepSkipped = "uebersprungen"
)

// Schritte des Umzugstags, in dieser Reihenfolge.
const (
	StepFinal   = "letzter-lauf"
	StepRemove  = "kopie-entfernen"
	StepRestore = "zurueckspielen"
	StepCopy    = "leitstand"
	StepServer  = "server-ebene"
	StepCheck   = "pruefen"
	StepDNS     = "dns"
)

// Titles nennt die Schritte für die Seite Umzug.
var Titles = []struct{ Key, Title string }{
	{StepFinal, "Letzter Lauf: Projekte stoppen und sichern"},
	{StepRemove, "Probe-Kopie auf dem neuen Server entfernen"},
	{StepRestore, "Zurückspielen aus dem letzten Lauf"},
	{StepCopy, "Leitstand auf den neuen Server"},
	{StepServer, "Server-Ebene übernehmen"},
	{StepCheck, "Seiten über die neue Adresse prüfen"},
	{StepDNS, "DNS umstellen"},
}

// Step ist ein Schritt des Umzugstags.
type Step struct {
	Key   string     `json:"schluessel"`
	Title string     `json:"titel"`
	State string     `json:"stand"`
	Note  string     `json:"hinweis,omitempty"`
	At    *time.Time `json:"am,omitempty"` // Ende des Schritts
}

// Day ist der Ablauf des Umzugstags.
type Day struct {
	Target   string     `json:"ziel"`
	Host     string     `json:"host"`
	IP       string     `json:"ip,omitempty"`
	Started  time.Time  `json:"gestartet"`
	Phase    string     `json:"phase"`
	Steps    []Step     `json:"schritte"`
	FinalRun string     `json:"letzter_lauf,omitempty"` // Lauf mit gestoppten Projekten
	Ended    *time.Time `json:"ende,omitempty"`         // Ende der Phase
	Note     string     `json:"hinweis,omitempty"`
}

// NewDay legt einen Ablauf mit offenen Schritten an.
func NewDay(target, host string, now time.Time) *Day {
	d := &Day{Target: target, Host: host, Started: now.UTC(), Phase: PhaseRunning}
	for _, t := range Titles {
		d.Steps = append(d.Steps, Step{Key: t.Key, Title: t.Title, State: StepOpen})
	}
	return d
}

// Step liefert den Schritt key; nil: gibt es nicht.
func (d *Day) Step(key string) *Step {
	for i := range d.Steps {
		if d.Steps[i].Key == key {
			return &d.Steps[i]
		}
	}
	return nil
}

// Set setzt den Stand eines Schritts. Ein fertiger Schritt bekommt die Zeit.
func (d *Day) Set(key, state, note string, now time.Time) {
	s := d.Step(key)
	if s == nil {
		return
	}
	s.State, s.Note = state, note
	if state == StepRunning || state == StepOpen {
		s.At = nil
		return
	}
	t := now.UTC()
	s.At = &t
}

// Active sagt, ob der Umzugstag diesen Server gerade in Beschlag hat:
// Projekte sind aus und sollen nicht gewarnt oder gesichert werden.
func (d *Day) Active() bool {
	if d == nil {
		return false
	}
	switch d.Phase {
	case PhaseRunning, PhaseDNS, PhaseFailed, PhaseInterrupted:
		return true
	}
	return false
}

// Done ist abgeloest.json: Dieser Server ist durch einen neuen ersetzt.
type Done struct {
	At     time.Time `json:"am"`
	IP     string    `json:"durch"` // Adresse des neuen Servers
	Target string    `json:"ziel,omitempty"`
	How    string    `json:"wie,omitempty"` // „DNS“ oder „von Hand“
}

// Text nennt den Stand für Banner und Protokoll.
func (d *Done) Text() string {
	return fmt.Sprintf("abgelöst am %s durch %s", d.At.Local().Format("02.01.2006 15:04"), d.IP)
}

// Store liest und schreibt die Dateien in Dir.
type Store struct {
	Dir string
}

func (s Store) path(name string) string { return filepath.Join(s.Dir, name) }

func (s Store) read(name string, v any) error {
	b, err := os.ReadFile(s.path(name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (s Store) write(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return outdir.WritePrivate(s.path(name), append(b, '\n'))
}

func (s Store) remove(name string) error {
	err := os.Remove(s.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Probe liest das Ergebnis der Generalprobe; nil: keins.
func (s Store) Probe() *Probe {
	var p Probe
	if s.read(ProbeFile, &p) != nil || p.At.IsZero() {
		return nil
	}
	return &p
}

// SaveProbe hält das Ergebnis eines Probe-Umzugs fest.
func (s Store) SaveProbe(p Probe) error { return s.write(ProbeFile, p) }

// Day liest den Ablauf des Umzugstags; nil: keiner.
func (s Store) Day() *Day {
	var d Day
	if s.read(DayFile, &d) != nil || d.Phase == "" {
		return nil
	}
	return &d
}

// SaveDay schreibt den Ablauf.
func (s Store) SaveDay(d *Day) error { return s.write(DayFile, d) }

// Done liest abgeloest.json; nil: Dieser Server ist nicht abgelöst. Eine
// unlesbare Datei heißt trotzdem abgelöst: lieber zu still als doppelt.
func (s Store) Done() *Done {
	b, err := os.ReadFile(s.path(DoneFile))
	if err != nil {
		return nil
	}
	var d Done
	if json.Unmarshal(b, &d) != nil {
		return &Done{IP: "?"}
	}
	return &d
}

// SaveDone vermerkt, dass dieser Server abgelöst ist.
func (s Store) SaveDone(d Done) error { return s.write(DoneFile, d) }

// ClearDone nimmt die Ablösung zurück (Rückweg nach der DNS-Umstellung).
func (s Store) ClearDone() error { return s.remove(DoneFile) }

// Recover setzt beim Start des Dienstes einen Ablauf, der mitten in einem
// Schritt stand, auf „unterbrochen“. Es liefert true, wenn es das tat.
func (s Store) Recover(now time.Time) bool {
	d := s.Day()
	if d == nil || d.Phase != PhaseRunning {
		return false
	}
	d.Phase = PhaseInterrupted
	for i := range d.Steps {
		if d.Steps[i].State == StepRunning {
			d.Set(d.Steps[i].Key, StepFailed, "unterbrochen: der Dienst von HostDeck startete neu", now)
		}
	}
	t := now.UTC()
	d.Ended = &t
	return s.SaveDay(d) == nil
}

// ProbeProblem sagt, warum die Generalprobe für einen Umzugstag nach host
// nicht reicht; leer: reicht.
func ProbeProblem(p *Probe, host string, now time.Time) string {
	switch {
	case p == nil:
		return "Zuerst die Generalprobe: ein Probe-Umzug auf den neuen Server, bei dem alles klappt."
	case !p.OK:
		why := ""
		if len(p.Problems) > 0 {
			why = " (" + strings.Join(p.Problems, "; ") + ")"
		}
		return "Die letzte Generalprobe am " + p.At.Local().Format("02.01. 15:04") + " hat nicht alles geschafft" + why + ". Behebe das und wiederhole den Probe-Umzug."
	case !sameHost(p.Host, host):
		return "Die Generalprobe lief auf " + p.Host + ". Der Umzugstag geht nur auf den Server der Generalprobe."
	case now.Sub(p.At) > ProbeMaxAge:
		return "Die Generalprobe ist vom " + p.At.Local().Format("02.01. 15:04") + " und damit älter als 3 Tage. Wiederhole den Probe-Umzug, dann gilt sie wieder."
	}
	return ""
}

func sameHost(a, b string) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.TrimSuffix(strings.Trim(strings.TrimSpace(s), "[]"), "."))
	}
	return norm(a) == norm(b)
}
