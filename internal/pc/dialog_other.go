//go:build !windows

package pc

import (
	"errors"
	"syscall"
)

// ErrCancelled heißt: Der Benutzer hat die Auswahl abgebrochen.
var ErrCancelled = errors.New("abgebrochen")

// PickFolder gibt es nur unter Windows; anderswo fragt HostDeck in der Konsole.
func PickFolder() (string, error) { return "", errors.New("kein Dialog verfügbar") }

// HasDialog meldet, ob es einen grafischen Dialog gibt.
func HasDialog() bool { return false }

// FreeSpace meldet den freien Platz im Dateisystem von path.
func FreeSpace(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// PrepareConsole ist nur unter Windows nötig.
func PrepareConsole() {}
