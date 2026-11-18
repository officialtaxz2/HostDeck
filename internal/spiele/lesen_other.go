//go:build !linux

package spiele

import (
	"errors"
	"io"
	"os"
)

// readBeneath liest die reguläre Datei rel unter dir. HostDeck misst Spielserver
// nur unter Linux; hier reicht der einfache Weg (für Tests).
func (r reader) readBeneath(dir, rel string, limit int64) ([]byte, error) {
	f, err := os.Open(r.fs.Real(dir + "/" + rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("keine reguläre Datei")
	}
	return io.ReadAll(io.LimitReader(f, limit))
}
