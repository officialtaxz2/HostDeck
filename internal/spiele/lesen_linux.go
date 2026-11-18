//go:build linux

package spiele

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// readBeneath liest die reguläre Datei rel unter dem Ordner dir (Host-Pfad)
// mit höchstens limit Byte. Unter dir folgt es keiner Verknüpfung, auch
// nicht in Zwischenordnern wie cfg/, und öffnet nicht blockierend: Eine
// zwischen Prüfen und Öffnen getauschte FIFO hält den Suchlauf nicht an.
// Ohne openat2 (vor Linux 5.6) nur ohne Verknüpfung am Ende.
func (r reader) readBeneath(dir, rel string, limit int64) ([]byte, error) {
	dfd, err := unix.Open(r.fs.Real(dir), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dfd)
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	fd, err := unix.Openat2(dfd, rel, &unix.OpenHow{
		Flags:   uint64(flags),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) {
		fd, err = unix.Openat(dfd, rel, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), rel)
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
