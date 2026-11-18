//go:build unix

package serverebene

import (
	"os"
	"syscall"
)

func fileIDs(fi os.FileInfo) (uid, gid uint32, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Uid, st.Gid, true
}

func chown(p string, uid, gid int) error { return os.Lchown(p, uid, gid) }
