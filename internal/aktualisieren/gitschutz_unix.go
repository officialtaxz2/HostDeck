//go:build unix

package aktualisieren

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// ownerOf nennt den Besitzer von .git, wenn HostDeck als root läuft und der
// Ordner jemand anderem gehört.
func ownerOf(fi os.FileInfo) *Owner {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 || st.Uid == 0 {
		return nil
	}
	o := &Owner{UID: st.Uid, GID: st.Gid, Home: "/"}
	if u, err := user.LookupId(strconv.FormatUint(uint64(st.Uid), 10)); err == nil && u.HomeDir != "" {
		o.Home = u.HomeDir
	}
	return o
}

func setOwner(attr **syscall.SysProcAttr, o *Owner) {
	*attr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: o.UID, Gid: o.GID}}
}
