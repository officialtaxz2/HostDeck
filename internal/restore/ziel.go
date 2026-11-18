package restore

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/units"
)

// Prüfung des neuen Servers vor dem Zurückspielen: Platz,
// belegte Ports, System, Arbeitsspeicher und Uhr. Docker prüft Run selbst.

// Note ist eine Zeile der Prüfung.
type Note struct {
	Level string // NoteOK, NoteWarn, NoteErr
	Text  string
}

const (
	NoteOK   = "ok"
	NoteWarn = "warn"
	NoteErr  = "err"
)

// HostInfo beschreibt den Server, auf den zurückgespielt wird.
type HostInfo struct {
	OS   string // lesbar, etwa „Debian GNU/Linux 13 (trixie)“
	OSID string // debian, ubuntu, …
	Arch string
	CPUs int
	RAM  int64 // 0: unbekannt
	// TimeSync: „ja“, wenn die Uhr automatisch gestellt wird, „nein“
	// wenn nicht, leer wenn unbekannt.
	TimeSync string
}

// HostProbe misst den Server, auf den zurückgespielt wird.
type HostProbe interface {
	Info(ctx context.Context) HostInfo
	// Free liefert für einen Ordner (oder seinen nächsten vorhandenen
	// Elternordner) den Einhängepunkt, freien Platz und Größe.
	Free(path string) (mount string, free, total int64, err error)
	// PortBusy meldet, ob ein TCP-Port schon belegt ist.
	PortBusy(port int) bool
}

const gib = int64(1) << 30

// CheckHost prüft, ob der Plan auf diesen Server passt. prev ist, was ein
// früherer Lauf hier schon zurückgespielt hat (nil: nichts); dessen Ports
// sind nicht belegt im Sinne der Prüfung, denn das Auffrischen spielt
// darüber.
func CheckHost(ctx context.Context, p *Plan, h HostProbe, prev *Record) []Note {
	var out []Note
	add := func(level, format string, args ...any) {
		out = append(out, Note{Level: level, Text: fmt.Sprintf(format, args...)})
	}
	info := h.Info(ctx)
	sys := info.OS
	if sys == "" {
		sys = "unbekanntes System"
	}
	desc := sys + ", " + info.Arch
	if info.CPUs > 0 {
		desc += fmt.Sprintf(", %d Kerne", info.CPUs)
	}
	if info.RAM > 0 {
		desc += ", " + units.HumanSize(info.RAM) + " Arbeitsspeicher"
	}
	switch info.OSID {
	case "debian", "ubuntu":
		add(NoteOK, "%s", desc)
	default:
		add(NoteWarn, "%s. HostDeck ist mit Debian und Ubuntu erprobt; auf anderen Systemen kann sich Docker anders verhalten.", desc)
	}
	if p.Arch != "" && info.Arch != "" && p.Arch != info.Arch && len(p.Images) > 0 {
		add(NoteErr, "Der alte Server hat einen %s-Prozessor, dieser einen %s-Prozessor. Die gesicherten Images laufen hier nicht.", p.Arch, info.Arch)
	}
	if info.RAM > 0 && info.RAM < gib {
		add(NoteWarn, "Nur %s Arbeitsspeicher. Für Docker mit mehreren Projekten ist das sehr knapp.", units.HumanSize(info.RAM))
	}

	// Platz je Dateisystem: Was in verschiedene Ordner kommt, kann auf
	// derselben Platte landen.
	type disk struct {
		need, free, total int64
	}
	disks := map[string]*disk{}
	var order []string
	for _, s := range p.Space {
		mount, free, total, err := h.Free(s.Path)
		if err != nil {
			add(NoteWarn, "Platz für %s nicht messbar: %v", s.Path, err)
			continue
		}
		d := disks[mount]
		if d == nil {
			d = &disk{free: free, total: total}
			disks[mount] = d
			order = append(order, mount)
		}
		d.need += s.Bytes
	}
	sort.Strings(order)
	for i, m := range order {
		d := disks[m]
		extra := ""
		if i == 0 && p.SpaceUnknown > 0 {
			extra = fmt.Sprintf(" (dazu %d Einträge ohne bekannte Größe)", p.SpaceUnknown)
		}
		need, free := units.HumanSize(d.need), units.HumanSize(d.free)
		rest := d.free - d.need
		// Reserve: ein Zehntel der Platte, mindestens 5 und höchstens 20 GB
		// (auf einer 2-TB-Platte wären 200 GB frei nicht „knapp“).
		reserve := min(max(d.total/10, 5*gib), 20*gib)
		switch {
		case rest < gib:
			add(NoteErr, "Zu wenig Platz auf %s: Das Zurückspielen braucht etwa %s%s, frei sind %s.", m, need, extra, free)
		case d.free < reserve:
			// Schon vorher knapp: Nicht das Zurückspielen ist das Problem,
			// sondern die volle Platte (sonst klänge 100 kB nach zu viel).
			add(NoteWarn, "Auf %s ist schon wenig frei: %s von %s; das Zurückspielen braucht nur etwa %s%s. Platz schaffen lohnt sich trotzdem.", m, free, units.HumanSize(d.total), need, extra)
		case rest < reserve:
			add(NoteWarn, "Platz auf %s reicht knapp: braucht etwa %s%s, frei sind %s; danach bleiben %s.", m, need, extra, free, units.HumanSize(rest))
		default:
			add(NoteOK, "Platz auf %s: braucht etwa %s%s, frei sind %s", m, need, extra, free)
		}
	}

	ports := p.Ports()
	if p.Caddy != nil {
		for _, pt := range []int{80, 443} {
			if _, ok := ports[pt]; !ok {
				ports[pt] = []string{"Caddy"}
			}
		}
	}
	var nums []int
	for pt := range ports {
		nums = append(nums, pt)
	}
	sort.Ints(nums)
	mine := prev.ports()
	var busy []string
	for _, pt := range nums {
		if h.PortBusy(pt) && !mine[pt] {
			busy = append(busy, fmt.Sprintf("%d (%s)", pt, strings.Join(ports[pt], ", ")))
		}
	}
	switch {
	case len(busy) > 0:
		add(NoteWarn, "Schon belegt: Port %s. Das ist nicht die Probe-Kopie von HostDeck; so startet das Projekt hier nicht.", strings.Join(busy, ", "))
	case len(nums) > 0 && p.Caddy != nil:
		add(NoteOK, "Ports frei (%d, darunter 80 und 443 für Caddy)", len(nums))
	case len(nums) > 0:
		add(NoteOK, "Ports der Projekte frei (%d)", len(nums))
	}

	switch info.TimeSync {
	case "ja":
		add(NoteOK, "Uhr wird automatisch gestellt")
	case "nein":
		add(NoteWarn, "Die Uhr wird nicht automatisch gestellt; Zertifikate und Backups brauchen eine richtige Uhr. Einschalten: timedatectl set-ntp true")
	}
	return out
}

// ports sind die Ports, die der frühere Lauf hier belegt.
func (r *Record) ports() map[int]bool {
	out := map[int]bool{}
	if r == nil {
		return out
	}
	for _, g := range r.Groups {
		for _, pt := range g.Ports {
			out[pt] = true
		}
	}
	if r.Caddy != nil {
		out[80], out[443] = true, true
	}
	return out
}

// LocalHost misst den Server, auf dem HostDeck läuft.
type LocalHost struct {
	// Root ist der Wurzelordner für Tests; leer: /.
	Root string
}

func (l LocalHost) path(p string) string { return filepath.Join(l.root(), p) }

func (l LocalHost) root() string {
	if l.Root == "" {
		return "/"
	}
	return l.Root
}

func (l LocalHost) Info(ctx context.Context) HostInfo {
	h := HostInfo{Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}
	if f, err := os.Open(l.path("/etc/os-release")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"'`)
			switch k {
			case "PRETTY_NAME":
				h.OS = v
			case "ID":
				h.OSID = v
			}
		}
		f.Close()
	}
	if b, err := os.ReadFile(l.path("/proc/meminfo")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
				if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil {
					h.RAM = kb * 1024
				}
			}
		}
	}
	if l.Root == "" {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value").Output(); err == nil {
			switch strings.TrimSpace(string(out)) {
			case "yes":
				h.TimeSync = "ja"
			case "no":
				h.TimeSync = "nein"
			}
		}
	}
	return h
}

func (l LocalHost) Free(path string) (string, int64, int64, error) {
	p := filepath.Clean(path)
	for {
		if _, err := os.Stat(l.path(p)); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	free, total, err := diskFree(l.path(p))
	if err != nil {
		return "", 0, 0, err
	}
	return l.mountOf(p), free, total, nil
}

// mountOf ist der Einhängepunkt, unter dem p liegt (längster Treffer in
// /proc/self/mounts); ohne Liste „/“.
func (l LocalHost) mountOf(p string) string {
	best := "/"
	b, err := os.ReadFile(l.path("/proc/self/mounts"))
	if err != nil {
		return best
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		m := strings.ReplaceAll(f[1], `\040`, " ")
		switch f[2] {
		case "proc", "sysfs", "cgroup", "cgroup2", "devpts", "mqueue", "tmpfs", "devtmpfs", "overlay", "nsfs":
			continue
		}
		if (p == m || strings.HasPrefix(p, strings.TrimSuffix(m, "/")+"/")) && len(m) > len(best) {
			best = m
		}
	}
	return best
}

func (l LocalHost) PortBusy(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}
