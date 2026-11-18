//go:build unix

package aufraeumen

import (
	"os"
	"syscall"
)

func owner(fi os.FileInfo) (uid, gid int) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return 0, 0
}
