//go:build !windows

package caddy

import (
	"io/fs"
	"syscall"
)

// owner liefert Besitzer und Gruppe einer Datei; -1, wenn unbekannt.
func owner(fi fs.FileInfo) (uid, gid int) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return -1, -1
}
