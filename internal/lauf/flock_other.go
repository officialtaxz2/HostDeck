//go:build !unix

package lauf

import (
	"errors"
	"os"
)

// Auf Windows läuft kein Lauf von HostDeck; die Sperre ist dort immer frei.
var errBusy = errors.New("belegt")

func tryLock(*os.File) error { return nil }
func unlock(*os.File) error  { return nil }

func openLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}
