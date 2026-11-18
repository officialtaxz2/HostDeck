//go:build linux

// Package ohnelink öffnet Ordner, ohne Verknüpfungen zu folgen: Pfad für
// Pfad mit openat2 und RESOLVE_NO_SYMLINKS. Löschen und Zurücklegen unter
// Speicherplatz und im Papierkorb arbeiten danach nur noch relativ zu den
// geöffneten Ordnern. So kann niemand zwischen Prüfen und Handeln einen
// Ordner gegen eine Verknüpfung (etwa auf /etc) tauschen.
package ohnelink

import (
	"errors"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrOld: Der Kernel kennt openat2 nicht (vor Linux 5.6) oder verbietet es.
var ErrOld = errors.New("dieser Kernel kann nicht sicher ohne Verknüpfungen öffnen (openat2 fehlt, ab Linux 5.6); HostDeck löscht und schreibt hier nichts")

// OpenDir öffnet den Ordner name in dirfd (genau ein Teil, ohne /). xdev:
// auch keinen Einhängepunkt überqueren.
func OpenDir(dirfd int, name string, xdev bool) (int, error) {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return -1, unix.EINVAL
	}
	res := uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS)
	if xdev {
		res |= unix.RESOLVE_NO_XDEV
	}
	fd, err := unix.Openat2(dirfd, name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: res,
	})
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) {
		return -1, ErrOld
	}
	return fd, err
}

// OpenParent öffnet den Ordner über host (Pfad auf dem Server) unter root
// Teil für Teil ohne Verknüpfungen und liefert ihn mit dem letzten Teil.
// Den Deskriptor schließt der Aufrufer. bad nennt bei einem Fehler den
// Teilpfad, an dem es scheiterte.
func OpenParent(root, host string, xdev bool) (dirfd int, base, bad string, err error) {
	if root == "" {
		root = "/"
	}
	host = path.Clean("/" + host)
	if host == "/" {
		return -1, "", "/", unix.EINVAL
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", "/", err
	}
	parts := strings.Split(strings.TrimPrefix(host, "/"), "/")
	cur := "/"
	for _, p := range parts[:len(parts)-1] {
		cur = path.Join(cur, p)
		next, err := OpenDir(fd, p, xdev)
		unix.Close(fd)
		if err != nil {
			return -1, "", cur, err
		}
		fd = next
	}
	return fd, parts[len(parts)-1], "", nil
}
