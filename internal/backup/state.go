package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
)

// StopEntry ist ein Container, den HostDeck gestoppt und noch nicht wieder
// gestartet hat.
type StopEntry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Run   string `json:"run"`
	Final bool   `json:"final,omitempty"` // bleibt nach dem letzten Lauf absichtlich aus
}

// StopState merkt sich auf der Platte, welche Container HostDeck gestoppt hat.
// Bricht ein Lauf hart ab (Stromausfall, kill -9), weiß der nächste Lauf
// oder hostdeck dienste-starten so, was wieder an muss.
type StopState struct {
	Path string

	mu      sync.Mutex
	entries []StopEntry
}

// LoadStopState liest den Stand; eine fehlende Datei heißt: nichts gestoppt.
func LoadStopState(path string) (*StopState, error) {
	s := &StopState{Path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.entries); err != nil {
		return nil, err
	}
	return s, nil
}

// Entries gibt eine Kopie der Einträge zurück.
func (s *StopState) Entries() []StopEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StopEntry(nil), s.entries...)
}

// Add vermerkt einen gestoppten Container. Er wird gespeichert, bevor der
// Container wirklich gestoppt wird.
func (s *StopState) Add(e StopEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.entries {
		if x.ID == e.ID {
			return nil
		}
	}
	s.entries = append(s.entries, e)
	return s.save()
}

// MarkFinal vermerkt, dass alle Einträge dieses Laufs absichtlich aus
// bleiben.
func (s *StopState) MarkFinal(run string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].Run == run {
			s.entries[i].Final = true
		}
	}
	return s.save()
}

// Remove streicht einen Container, der wieder läuft.
func (s *StopState) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.entries[:0]
	for _, x := range s.entries {
		if x.ID != id {
			out = append(out, x)
		}
	}
	s.entries = out
	return s.save()
}

func (s *StopState) save() error {
	if s.Path == "" {
		return nil
	}
	if len(s.entries) == 0 {
		err := os.Remove(s.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	b, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	return outdir.WritePrivate(s.Path, append(b, '\n'))
}

// StartRecorded startet alle Container, die als gestoppt vermerkt sind, in
// umgekehrter Reihenfolge (Datenbanken wurden zuletzt gestoppt und kommen
// so zuerst), und streicht jeden, der danach läuft.
func StartRecorded(ctx context.Context, d Docker, s *StopState, out io.Writer, settle time.Duration) (notRunning []string) {
	return startRecorded(ctx, d, s, s.Entries(), out, settle)
}

// Interrupted sind die Einträge eines abgebrochenen Laufs, also alle außer
// denen, die nach einem letzten Lauf absichtlich aus bleiben.
func (s *StopState) Interrupted() []StopEntry {
	var out []StopEntry
	for _, e := range s.Entries() {
		if !e.Final {
			out = append(out, e)
		}
	}
	return out
}

// StartInterrupted startet nur die Container eines abgebrochenen Laufs.
// Der Dienst ruft es beim Start auf: Nach einem Absturz oder Neustart des
// Servers mitten im Backup laufen die Container so wieder. Was nach einem
// letzten Lauf absichtlich aus ist, bleibt aus.
func StartInterrupted(ctx context.Context, d Docker, s *StopState, out io.Writer, settle time.Duration) (notRunning []string) {
	return startRecorded(ctx, d, s, s.Interrupted(), out, settle)
}

func startRecorded(ctx context.Context, d Docker, s *StopState, entries []StopEntry, out io.Writer, settle time.Duration) (notRunning []string) {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		fmt.Fprintf(out, "  starte %s\n", e.Name)
		if err := d.Start(ctx, e.ID); err != nil {
			if errors.Is(err, dockerapi.ErrNoSuchContainer) {
				fmt.Fprintf(out, "    gibt es nicht mehr, wird aus der Liste gestrichen\n")
				_ = s.Remove(e.ID)
				continue
			}
			fmt.Fprintf(out, "    Fehler: %v\n", err)
			notRunning = append(notRunning, e.Name)
			continue
		}
	}
	sleep(ctx, settle)
	for _, e := range entries {
		if slices.Contains(notRunning, e.Name) {
			continue
		}
		st, err := d.State(ctx, e.ID)
		if err == nil && st.Running {
			_ = s.Remove(e.ID)
			continue
		}
		if errors.Is(err, dockerapi.ErrNoSuchContainer) {
			continue
		}
		notRunning = append(notRunning, e.Name)
	}
	return notRunning
}
