//go:build unix

package main

import (
	"io/fs"
	"os"
	"syscall"
)

// fileOwner ist der Besitzer einer Datei.
func fileOwner(fi fs.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// openNoFollow öffnet path nur, wenn es kein Symlink ist.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}
