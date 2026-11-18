// Package anmeldung prüft das Leitstand-Passwort und sperrt nach
// Fehlversuchen. Nur hostdeck dienst (root) benutzt es; die Website reicht das
// Passwort nur weiter.
//
// Gespeichert ist nur ein Fingerabdruck nach Argon2id (19 MiB, 2 Durchläufe,
// wie von OWASP empfohlen). Höchstens zwei Prüfungen laufen gleichzeitig,
// damit ein Ansturm den Server nicht in die Knie zwingt.
package anmeldung

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Grenzen für ein eigenes Passwort.
const (
	MinLen = 12
	MaxLen = 256
)

// Passwort ist das Leitstand-Passwort in File.
type Passwort struct {
	File string
	Now  func() time.Time

	once sync.Once
	sem  chan struct{}
	mu   sync.Mutex
}

type stored struct {
	Version int       `json:"version"`
	Salt    []byte    `json:"salz"`
	Hash    []byte    `json:"fingerabdruck"`
	Time    uint32    `json:"durchlaeufe"`
	Memory  uint32    `json:"speicher_kib"`
	Threads uint8     `json:"faeden"`
	Set     time.Time `json:"gesetzt"`
}

// Parameter für neue Fingerabdrücke.
var params = struct {
	time, memory uint32
	threads      uint8
}{2, 19 * 1024, 1}

// ErrNotSet: Es ist noch kein Passwort festgelegt.
var ErrNotSet = errors.New("noch kein Passwort festgelegt")

func (p *Passwort) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Passwort) read() (*stored, error) {
	b, err := os.ReadFile(p.File)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotSet
	}
	if err != nil {
		return nil, err
	}
	var s stored
	if err := json.Unmarshal(b, &s); err != nil || len(s.Salt) < 16 || len(s.Hash) < 32 || s.Time == 0 || s.Memory == 0 || s.Threads == 0 {
		return nil, fmt.Errorf("%s ist beschädigt; neues Passwort mit sudo hostdeck passwort", p.File)
	}
	return &s, nil
}

// IsSet sagt, ob ein Passwort festgelegt ist.
func (p *Passwort) IsSet() bool {
	_, err := p.read()
	return err == nil
}

// SetAt liefert, wann das Passwort festgelegt wurde.
func (p *Passwort) SetAt() (time.Time, error) {
	s, err := p.read()
	if err != nil {
		return time.Time{}, err
	}
	return s.Set, nil
}

// Validate prüft ein eigenes Passwort.
func Validate(pw string) error {
	if !utf8.ValidString(pw) {
		return errors.New("Das Passwort enthält ungültige Zeichen.")
	}
	n := utf8.RuneCountInString(pw)
	if n < MinLen {
		return fmt.Errorf("Das Passwort braucht mindestens %d Zeichen.", MinLen)
	}
	if len(pw) > MaxLen {
		return fmt.Errorf("Das Passwort darf höchstens %d Zeichen haben.", MaxLen)
	}
	for _, r := range pw {
		if unicode.IsControl(r) {
			return errors.New("Das Passwort enthält Steuerzeichen.")
		}
	}
	if strings.TrimSpace(pw) != pw {
		return errors.New("Das Passwort beginnt oder endet mit einem Leerzeichen.")
	}
	return nil
}

// Set legt ein neues Passwort fest.
func (p *Passwort) Set(pw string) error {
	if err := Validate(pw); err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	s := stored{Version: 1, Salt: salt, Time: params.time, Memory: params.memory, Threads: params.threads, Set: p.now().UTC()}
	p.acquire()
	s.Hash = argon2.IDKey([]byte(pw), salt, s.Time, s.Memory, s.Threads, 32)
	p.release()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	tmp := p.File + ".neu"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.File)
}

func (p *Passwort) acquire() {
	p.once.Do(func() { p.sem = make(chan struct{}, 2) })
	p.sem <- struct{}{}
}

func (p *Passwort) release() { <-p.sem }

// Verify prüft ein Passwort. Ohne festgelegtes Passwort kommt ErrNotSet.
func (p *Passwort) Verify(ctx context.Context, pw string) (bool, error) {
	s, err := p.read()
	if err != nil {
		return false, err
	}
	if len(pw) > MaxLen {
		return false, nil
	}
	p.once.Do(func() { p.sem = make(chan struct{}, 2) })
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	h := argon2.IDKey([]byte(pw), s.Salt, s.Time, s.Memory, s.Threads, uint32(len(s.Hash)))
	p.release()
	return subtle.ConstantTimeCompare(h, s.Hash) == 1, nil
}

// Alphabet für erzeugte Passwörter: ohne Zeichen, die man verwechselt
// (0/o, 1/l/i).
const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// Generate erzeugt ein Passwort aus vier Gruppen zu fünf Zeichen, etwa
// „k7m2p-…“, rund 99 Bit Zufall.
func Generate() (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for g := 0; g < 4; g++ {
		if g > 0 {
			b.WriteByte('-')
		}
		for i := 0; i < 5; {
			if _, err := rand.Read(buf); err != nil {
				return "", err
			}
			// Gleichverteilt: Werte oberhalb des größten Vielfachen verwerfen.
			if int(buf[0]) >= 256-256%len(alphabet) {
				continue
			}
			b.WriteByte(alphabet[int(buf[0])%len(alphabet)])
			i++
		}
	}
	return b.String(), nil
}
