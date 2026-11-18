// Package sitzung merkt sich die Anmeldungen am Leitstand. Die Kennung steht
// im Cookie des Browsers; gespeichert ist nur ihr SHA-256. Wer die Datei mit
// den Anmeldungen liest, kann sich damit also nicht anmelden.
//
// Regeln aus dem Plan: 30 Minuten ohne Aktivität, höchstens 12 Stunden,
// höchstens 50 gleichzeitig (die älteste fällt zuerst weg). Eine Bestätigung
// mit dem Passwort gilt fünf Minuten.
package sitzung

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sync"
	"time"
)

// Max begrenzt die Zahl gleichzeitiger Anmeldungen.
const Max = 50

// Wege der Anmeldung.
const (
	ViaLink     = "Link"     // Einmal-Link vom Notausgang oder von hostdeck serve
	ViaPassword = "Passwort" // Leitstand-Passwort
)

// Info beschreibt eine Anmeldung, ohne ihre Kennung.
type Info struct {
	Created   time.Time `json:"seit"`
	Seen      time.Time `json:"zuletzt"`
	Via       string    `json:"weg"`
	Gate      string    `json:"eingang"`
	Addr      string    `json:"adresse"`
	Confirmed time.Time `json:"bestaetigt_bis,omitempty"`
}

// Store hält die Anmeldungen, auf Wunsch auch über einen Neustart hinweg
// (File). Ohne File nur im Speicher.
type Store struct {
	Idle    time.Duration // ohne Aktivität so lange gültig
	MaxAge  time.Duration // höchstens so lange gültig
	Confirm time.Duration // so lange gilt eine Bestätigung mit dem Passwort
	File    string        // leer: nur im Speicher
	Now     func() time.Time

	mu    sync.Mutex
	m     map[string]*Info // Schlüssel: hex(SHA-256 der Kennung)
	saved time.Time
	ready bool
}

// New liefert einen Store mit den Regeln aus dem Plan.
func New(file string) *Store {
	return &Store{Idle: 30 * time.Minute, MaxAge: 12 * time.Hour, Confirm: 5 * time.Minute, File: file}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func key(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

// load liest die Datei beim ersten Zugriff. Ist sie kaputt, sind eben alle
// abgemeldet.
func (s *Store) load() {
	if s.ready {
		return
	}
	s.ready = true
	s.m = map[string]*Info{}
	if s.File == "" {
		return
	}
	b, err := os.ReadFile(s.File)
	if err != nil {
		return
	}
	var m map[string]*Info
	if json.Unmarshal(b, &m) == nil {
		for k, v := range m {
			if v != nil && len(k) == 64 {
				s.m[k] = v
			}
		}
	}
}

// save schreibt die Datei; force auch dann, wenn sie gerade erst
// geschrieben wurde.
func (s *Store) save(now time.Time, force bool) {
	if s.File == "" || (!force && now.Sub(s.saved) < time.Minute) {
		return
	}
	s.saved = now
	b, err := json.Marshal(s.m)
	if err != nil {
		return
	}
	tmp := s.File + ".neu"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.File)
}

// NewSession legt eine Anmeldung an und liefert ihre Kennung fürs Cookie.
// Mit dem Link Angemeldete gelten als bestätigt: Sie haben bewiesen, dass sie
// root auf dem Server sind.
func (s *Store) NewSession(via, gate, addr string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := s.now()
	s.prune(now)
	for len(s.m) >= Max {
		var oldest string
		var t time.Time
		for k, v := range s.m {
			if t.IsZero() || v.Created.Before(t) {
				oldest, t = k, v.Created
			}
		}
		delete(s.m, oldest)
	}
	s.m[key(id)] = &Info{Created: now, Seen: now, Via: via, Gate: gate, Addr: addr}
	s.save(now, true)
	return id, nil
}

// ErrUnknown: keine gültige Anmeldung.
var ErrUnknown = errors.New("nicht angemeldet")

// Check prüft eine Kennung und liefert die Anmeldung. active zählt als
// Aktivität und verlängert sie; Abfragen im Hintergrund tun das nicht.
func (s *Store) Check(id string, active bool) (Info, error) {
	if id == "" {
		return Info{}, ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	k := key(id)
	x, ok := s.m[k]
	now := s.now()
	if !ok || s.expired(x, now) {
		if ok {
			delete(s.m, k)
			s.save(now, true)
		}
		return Info{}, ErrUnknown
	}
	if active {
		x.Seen = now
		s.save(now, false)
	}
	return *x, nil
}

// Confirmed sagt, ob die Anmeldung gerade bestätigt ist.
func (s *Store) Confirmed(i Info) bool {
	return i.Via == ViaLink || s.now().Before(i.Confirmed)
}

// MarkConfirmed vermerkt eine Bestätigung mit dem Passwort.
func (s *Store) MarkConfirmed(id string) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	x, ok := s.m[key(id)]
	now := s.now()
	if !ok || s.expired(x, now) {
		return time.Time{}, ErrUnknown
	}
	x.Confirmed = now.Add(s.Confirm)
	x.Seen = now
	s.save(now, true)
	return x.Confirmed, nil
}

// Drop meldet eine Anmeldung ab.
func (s *Store) Drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	delete(s.m, key(id))
	s.save(s.now(), true)
}

// DropAll meldet alle ab und liefert, wie viele es waren.
func (s *Store) DropAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := s.now()
	s.prune(now)
	n := len(s.m)
	s.m = map[string]*Info{}
	s.save(now, true)
	return n
}

// Count zählt die gültigen Anmeldungen.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	s.prune(s.now())
	return len(s.m)
}

func (s *Store) expired(x *Info, now time.Time) bool {
	return (s.Idle > 0 && now.Sub(x.Seen) > s.Idle) || (s.MaxAge > 0 && now.Sub(x.Created) > s.MaxAge)
}

func (s *Store) prune(now time.Time) {
	for k, v := range s.m {
		if s.expired(v, now) {
			delete(s.m, k)
		}
	}
}

// Remove löscht die Datei, etwa beim Entfernen.
func Remove(file string) error {
	err := os.Remove(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
