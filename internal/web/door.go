package web

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/sitzung"
)

// Visitor sagt, über welchen Eingang und von welcher Adresse jemand kommt.
type Visitor struct {
	Gate string // Name des Eingangs, etwa „Notausgang“
	Addr string // Adresse des Besuchers, soweit bekannt
}

// Door entscheidet, wer hineinkommt, und kennt die Anmeldungen. Bei hostdeck serve
// liegt beides im Speicher (MemoryDoor), beim Dienst prüft hostdeck dienst.
type Door interface {
	// Redeem tauscht einen Link gegen eine Anmeldung.
	Redeem(ctx context.Context, token string, v Visitor) (string, error)
	// Login meldet mit dem Leitstand-Passwort an.
	Login(ctx context.Context, password string, v Visitor) (string, error)
	// Check prüft eine Anmeldung. active zählt als Aktivität.
	Check(ctx context.Context, id string, active bool) error
	// Logout meldet ab; all meldet alle Anmeldungen ab.
	Logout(ctx context.Context, id string, all bool, v Visitor) error
}

// Fehler der Tür. Die Texte sieht der Besucher.
var (
	ErrBadLink     = errors.New("Link ungültig oder abgelaufen")
	ErrBadPassword = errors.New("Das Passwort stimmt nicht.")
	ErrNoPassword  = errors.New("Für den Leitstand ist noch kein Passwort festgelegt. Auf dem Server eingeben: sudo hostdeck passwort")
	ErrNoLogin     = errors.New("Anmelden mit Passwort geht hier nicht.")
	ErrSession     = errors.New("nicht angemeldet")
)

// LockedError: zu viele Fehlversuche, Anmelden erst nach Wait wieder.
type LockedError struct{ Wait time.Duration }

func (e *LockedError) Error() string {
	m := int((e.Wait + time.Minute - 1) / time.Minute)
	if m <= 1 {
		return "Zu viele falsche Passwörter. Anmelden geht in einer Minute wieder."
	}
	return fmt.Sprintf("Zu viele falsche Passwörter. Anmelden geht in %d Minuten wieder.", m)
}

// MemoryDoor ist die Tür von hostdeck serve: Anmeldungen im Speicher, ein Link,
// der gilt, solange hostdeck serve läuft, kein Passwort.
type MemoryDoor struct {
	Store *sitzung.Store
	Link  func(token string) bool // prüft einen Link
}

func (d *MemoryDoor) Redeem(_ context.Context, token string, v Visitor) (string, error) {
	if d.Link == nil || !d.Link(token) {
		return "", ErrBadLink
	}
	return d.Store.NewSession(sitzung.ViaLink, v.Gate, v.Addr)
}

func (d *MemoryDoor) Login(context.Context, string, Visitor) (string, error) {
	return "", ErrNoLogin
}

func (d *MemoryDoor) Check(_ context.Context, id string, active bool) error {
	if _, err := d.Store.Check(id, active); err != nil {
		return ErrSession
	}
	return nil
}

func (d *MemoryDoor) Logout(_ context.Context, id string, all bool, _ Visitor) error {
	if all {
		d.Store.DropAll()
		return nil
	}
	d.Store.Drop(id)
	return nil
}
