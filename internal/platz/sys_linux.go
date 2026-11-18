//go:build linux

package platz

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// lowerPriority setzt für den aktuellen Thread nice 19 und die Klasse idle
// für die Platte. Der Aufrufer hält den Thread mit runtime.LockOSThread
// fest und gibt ihn nicht wieder frei.
func lowerPriority() {
	tid := syscall.Gettid()
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, tid, 19)
	const whoProcess, classIdle = 1, 3
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, whoProcess, uintptr(tid), classIdle<<13)
}

// changedAt ist die letzte Änderung von fi: Inhalt (mtime) oder Eintrag
// (ctime, etwa nach Umbenennen oder Zurückholen).
func changedAt(fi fs.FileInfo) time.Time {
	t := fi.ModTime()
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		if c := time.Unix(st.Ctim.Sec, st.Ctim.Nsec); c.After(t) {
			t = c
		}
	}
	return t
}

// statOf liest belegten Platz (wie du), Gerät, Inode und Anzahl Links.
func statOf(fi fs.FileInfo) (size int64, dev, ino, links uint64) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fi.Size(), 0, 0, 1
	}
	return st.Blocks * 512, uint64(st.Dev), uint64(st.Ino), uint64(st.Nlink)
}

// MountPoints liest die Einhängepunkte aus /proc/self/mountinfo, auch
// Bind-Mounts auf derselben Platte.
func MountPoints() []string {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 5 {
			continue
		}
		out = append(out, unescapeMount(fl[4]))
	}
	return out
}

// unescapeMount löst \040 und Ähnliches in Pfaden aus mountinfo auf.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// SwapFiles nennt aktive Swap-Dateien aus /proc/swaps.
func SwapFiles() []string {
	b, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n")[1:] {
		if f := strings.Fields(line); len(f) >= 2 && f[1] == "file" {
			out = append(out, unescapeMount(f[0]))
		}
	}
	return out
}

// OpenFile ist eine Datei, die ein Prozess offen hält.
type OpenFile struct {
	Path    string // wie der Kernel sie nennt, ohne „ (deleted)“
	Proc    string // Name des Prozesses
	PID     int
	Deleted bool
	Size    int64 // belegter Platz, nur bei gelöschten gemessen
	Dev     uint64
}

// OpenFiles liest aus /proc, welche Dateien Prozesse offen halten. Ohne
// root sieht es nur die eigenen Prozesse.
func OpenFiles() []OpenFile {
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []OpenFile
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join("/proc", p.Name(), "fd")
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		comm := ""
		for _, fd := range fds {
			fp := filepath.Join(dir, fd.Name())
			target, err := os.Readlink(fp)
			if err != nil || !strings.HasPrefix(target, "/") {
				continue
			}
			if comm == "" {
				b, _ := os.ReadFile(filepath.Join("/proc", p.Name(), "comm"))
				comm = strings.TrimSpace(string(b))
			}
			of := OpenFile{Path: target, Proc: comm, PID: pid}
			if t, ok := strings.CutSuffix(target, " (deleted)"); ok {
				of.Path, of.Deleted = t, true
				if fi, err := os.Stat(fp); err == nil && fi.Mode().IsRegular() {
					of.Size, of.Dev, _, _ = statOf(fi)
				}
			}
			out = append(out, of)
		}
	}
	return out
}
