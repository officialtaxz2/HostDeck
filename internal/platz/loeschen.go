package platz

import "errors"

// Result ist das Ergebnis eines Löschens.
type Result struct {
	Files  int64    // gelöschte Dateien
	Left   int      // Einträge, die bleiben mussten
	Errors []string // die ersten Gründe dafür
}

// Fehler beim Löschen.
var (
	ErrChanged   = errors.New("hat sich seit der Vorschau verändert (ersetzt oder verschoben)")
	ErrOtherDisk = errors.New("liegt auf einer anderen Platte oder ist eingehängt; HostDeck löscht dort nichts")
)
