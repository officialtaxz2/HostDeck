//go:build linux

package discover

import (
	"io/fs"
	"os"
	"syscall"
)

// lowerPriority setzt für den aktuellen Thread nice 19 und die I/O-Klasse
// „idle“. Der Aufrufer muss den Thread mit runtime.LockOSThread festhalten,
// sonst wirkt die Einstellung auf einen zufälligen Thread.
func lowerPriority() {
	tid := syscall.Gettid()
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, tid, 19)
	const (
		ioprioWhoProcess = 1
		ioprioClassIdle  = 3
		ioprioClassShift = 13
	)
	_, _, _ = syscall.Syscall(syscall.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(tid), ioprioClassIdle<<ioprioClassShift)
}

// diskUsage gibt den belegten Platz einer Datei zurück (wie du) und ihre
// Inode, damit Hardlinks nur einmal zählen.
func diskUsage(fi fs.FileInfo) (size int64, dev, ino uint64, links uint64) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fi.Size(), 0, 0, 1
	}
	return st.Blocks * 512, uint64(st.Dev), uint64(st.Ino), uint64(st.Nlink)
}

// runAsOwner gibt Prozessattribute zurück, mit denen ein Kindprozess als
// Eigentümer von dir läuft. Ohne root oder bei root-eigenen Ordnern bleibt
// alles beim Alten.
func runAsOwner(dir string) *syscall.SysProcAttr {
	if os.Geteuid() != 0 {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return nil
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid == 0 {
		return nil
	}
	return &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: st.Uid, Gid: st.Gid, NoSetGroups: true}}
}
