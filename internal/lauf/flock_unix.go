//go:build unix

package lauf

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var errBusy = errors.New("belegt")

func tryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errBusy
	}
	return err
}

func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// openLock öffnet die Sperrdatei nur für den eigenen Benutzer (0600). Für
// flock genügt Lesen; wäre die Datei für andere lesbar, könnte jeder
// Benutzer am Server sie halten und damit jeden Lauf blockieren. Eine
// Datei, die anderen offensteht oder einem anderen gehört (etwa von einer
// älteren Version), ersetzt openLock durch eine neue; wer die alte offen
// hält, hält dann nichts mehr.
func openLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil && !errors.Is(err, syscall.ELOOP) {
		return nil, err
	}
	if err == nil {
		var st syscall.Stat_t
		if err := syscall.Fstat(int(f.Fd()), &st); err != nil {
			f.Close()
			return nil, err
		}
		if st.Mode&0o077 == 0 && int(st.Uid) == os.Geteuid() && st.Mode&syscall.S_IFMT == syscall.S_IFREG {
			return f, nil
		}
		f.Close()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lauf-*.lock")
	if err != nil {
		return nil, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	return tmp, nil
}
