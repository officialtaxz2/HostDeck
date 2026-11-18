// Package protokoll schreibt, wer wann was im Leitstand getan hat: jede
// Anmeldung, jeden Fehlversuch und jede Aktion, mit Zeit und Adresse. Nur
// hostdeck dienst schreibt. Die Datei hat eine feste Höchstgröße; ist sie voll,
// wird sie zu protokoll.1.jsonl und eine neue beginnt.
//
// Passwörter, Links und Kennungen von Anmeldungen kommen nie hinein.
package protokoll

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
)

// Arten von Einträgen.
const (
	Anmeldung    = "anmeldung"
	Fehlversuch  = "fehlversuch"
	Gesperrt     = "gesperrt"
	Abmeldung    = "abmeldung"
	Link         = "link"
	Bestaetigung = "bestaetigung"
	Passwort     = "passwort"
	Auswahl      = "auswahl"
	Lauf         = "lauf"
	Update       = "update"
	Dienst       = "dienst"
	Zugang       = "zugang" // Freischalten von außen und Zurücknehmen
	Download     = "download"
	Container    = "container"    // Starten, Stoppen, Neustarten im Betrieb
	Projekt      = "projekt"      // Projekte aktualisieren und zurückstellen
	Domain       = "domain"       // Caddy ordnen, Domains anlegen, ändern, ab- und anschalten
	Aufraeumen   = "aufraeumen"   // Aufräumen, Papierkorb zurückholen und leeren
	Speicher     = "speicher"     // Ordner und Dateien unter Speicherplatz löschen
	Sicherheit   = "sicherheit"   // Ports schließen und öffnen
	Leistung     = "leistung"     // Log-Grenze, Swap-Puffer
	Ueberwachung = "ueberwachung" // Warnungen und Entwarnungen
)

// Entry ist ein Eintrag.
type Entry struct {
	Time    time.Time `json:"zeit"`
	Kind    string    `json:"art"`
	Text    string    `json:"text"`
	Addr    string    `json:"adresse,omitempty"`
	Gate    string    `json:"eingang,omitempty"`
	Details []string  `json:"einzelheiten,omitempty"`
}

// Log ist das Protokoll in File.
type Log struct {
	File    string
	MaxSize int64 // Höchstgröße einer Datei, Standard 1 MiB
	Now     func() time.Time

	mu sync.Mutex
}

func (l *Log) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Log) max() int64 {
	if l.MaxSize > 0 {
		return l.MaxSize
	}
	return 1 << 20
}

func (l *Log) old() string { return strings.TrimSuffix(l.File, ".jsonl") + ".1.jsonl" }

// Add schreibt einen Eintrag. Fehler beim Schreiben gehen verloren, damit
// das Protokoll nie eine Aktion verhindert; der Dienst meldet sie im Journal.
func (l *Log) Add(e Entry) error {
	if l == nil || l.File == "" {
		return nil
	}
	if e.Time.IsZero() {
		e.Time = l.now()
	}
	e.Time = e.Time.UTC()
	if len(e.Details) > 50 {
		e.Details = append(e.Details[:50], "… und weitere")
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if fi, err := os.Stat(l.File); err == nil && fi.Size()+int64(len(b)) > l.max() {
		if err := os.Rename(l.File, l.old()); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(l.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Last liefert die letzten n Einträge, den neuesten zuerst.
func (l *Log) Last(n int) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var all []Entry
	for _, f := range []string{l.old(), l.File} {
		b, err := os.ReadFile(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				all = append(all, e)
			}
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	out := make([]Entry, len(all))
	for i, e := range all {
		out[len(all)-1-i] = e
	}
	return out, nil
}
