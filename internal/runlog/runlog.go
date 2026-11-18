// Package runlog führt backup-laeufe.json im Ergebnisordner: eine kurze
// Liste der letzten Backup-Läufe, damit hostdeck serve ohne root zeigen kann,
// wann zuletzt gesichert wurde und ob das Backup gültig ist. Die Liste
// enthält keine Pfade, Passwörter oder Inhalte, nur Zeiten, Größen und das
// Urteil des Laufs.
package runlog

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/outdir"
)

// File ist der Dateiname im Ergebnisordner.
const File = "backup-laeufe.json"

// Keep ist die Zahl der Läufe, die die Liste behält.
const Keep = 30

// Entry ist ein Lauf.
type Entry struct {
	Run      string    `json:"run"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Valid    bool      `json:"valid"`
	Final    bool      `json:"final,omitempty"`   // letzter Lauf vor dem Umzug
	Aborted  bool      `json:"aborted,omitempty"` // abgebrochen
	Verdict  string    `json:"verdict"`           // Urteil wie im Bericht, etwa „GÜLTIG“
	Size     int64     `json:"size"`              // geplante Größe aller Einträge
	Added    int64     `json:"added"`             // neu übertragen
	Items    int       `json:"items"`             // Einträge im Plan
	Projects int       `json:"projects"`          // Projekte und einzelne Container im Plan
	Downtime int64     `json:"downtime_s"`        // längste Pause eines Projekts in Sekunden
	Target   string    `json:"target,omitempty"`  // Ziel: „server“ (Backup auf dem Server); leer: dein PC
}

// Load liest die Liste, neueste zuerst. Fehlt die Datei, ist sie leer.
func Load(dir string) ([]Entry, error) {
	b, err := os.ReadFile(filepath.Join(dir, File))
	if errors.Is(err, fs.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Entry
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	sortNewest(list)
	if list == nil {
		list = []Entry{}
	}
	return list, nil
}

// Append fügt einen Lauf hinzu und behält die neuesten Keep Einträge. Eine
// beschädigte Liste wird ersetzt statt den Lauf scheitern zu lassen.
func Append(dir string, e Entry) error {
	list, err := Load(dir)
	if err != nil {
		list = nil
	}
	out := []Entry{e}
	for _, x := range list {
		if x.Run != e.Run {
			out = append(out, x)
		}
	}
	sortNewest(out)
	if len(out) > Keep {
		out = out[:Keep]
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := outdir.WritePrivate(filepath.Join(dir, File), append(b, '\n')); err != nil {
		return err
	}
	outdir.GiveToSudoUser(dir, File)
	return nil
}

func sortNewest(list []Entry) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].Started.After(list[j].Started) })
}
