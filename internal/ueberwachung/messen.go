// Package ueberwachung misst den Server und seine Container rund um die Uhr
// und hebt den Verlauf auf. Es zeigt nur an; es ändert nie
// etwas am Server.
//
// Gemessen wird aus /proc und cgroup v2, nicht über docker stats: das
// braucht je Container rund eine Sekunde, die Dateien lesen sich in
// Millisekunden.
package ueberwachung

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

// Docker ist, was die Messung von Docker liest (dockerapi.Ops).
type Docker interface {
	List(ctx context.Context) ([]dockerapi.OpsContainer, error)
	Inspect(ctx context.Context, id string) (dockerapi.Container, error)
}

// Schlüssel der Server-Werte. Container-Werte heißen c/<name>/<wert>,
// Summen je Compose-Projekt p/<projekt>/<wert>, Laufwerke d/<einhängepunkt>.
const (
	KeyCPU     = "s/cpu"    // % aller Kerne
	KeySteal   = "s/steal"  // %
	KeyIOWait  = "s/iowait" // %
	KeyLoad    = "s/last"   // Last über 1 Minute
	KeyRAM     = "s/ram"    // belegt in Byte (gesamt minus verfügbar)
	KeySwap    = "s/swap"   // belegt in Byte
	KeyRead    = "s/lesen"  // Byte/s von den Platten
	KeyWrite   = "s/schreiben"
	KeyNetIn   = "s/ein" // Byte/s über die echten Netzkarten
	KeyNetOut  = "s/aus"
	ValCPU     = "cpu"       // Container: % eines Kerns, wie docker stats
	ValRAM     = "ram"       // Byte, ohne Seiten-Cache, wie docker stats
	ValRead    = "lesen"     // Byte/s
	ValWrite   = "schreiben" // Byte/s
	ValNetIn   = "ein"       // Byte/s
	ValNetOut  = "aus"       // Byte/s
	diskPrefix = "d/"
)

// ContainerKey ist der Schlüssel eines Container-Werts.
func ContainerKey(name, val string) string { return "c/" + name + "/" + val }

// ProjectKey ist der Schlüssel einer Summe über die Container eines
// Compose-Projekts.
func ProjectKey(project, val string) string { return "p/" + project + "/" + val }

// Frame ist eine Messung: alle Werte zu einem Zeitpunkt.
type Frame struct {
	At     time.Time
	Values map[string]float64
}

// Info ist, was zu den Werten gehört, aber keinen Verlauf braucht.
type Info struct {
	Cores      int             `json:"kerne"`
	RAMTotal   int64           `json:"ram_gesamt"`
	SwapTotal  int64           `json:"swap_gesamt"`
	Disks      []DiskInfo      `json:"laufwerke"`
	Containers []ContainerInfo `json:"container"`
	Problems   []string        `json:"probleme,omitempty"`
}

// DiskInfo ist ein Laufwerk.
type DiskInfo struct {
	Mount string `json:"pfad"`
	Total int64  `json:"gesamt"`
	Used  int64  `json:"belegt"`
}

// ContainerInfo beschreibt einen Container.
type ContainerInfo struct {
	Name     string `json:"name"`
	Project  string `json:"projekt,omitempty"`
	State    string `json:"zustand"` // running, exited, …
	Health   string `json:"gesundheit,omitempty"`
	Restarts int    `json:"neustarts"`
	HostNet  bool   `json:"host_netz,omitempty"` // Netz zählt beim Server
	MemLimit int64  `json:"speicher_grenze,omitempty"`
}

// Collector misst. Er merkt sich die Zähler der letzten Messung, um daraus
// Raten zu bilden; die erste Messung hat darum noch keine Raten.
type Collector struct {
	Docker Docker
	Proc   string // Standard /proc
	Sys    string // Standard /sys
	// Disk misst Größe und freien Platz des Dateisystems von path.
	Disk func(path string) (total, free int64, err error)
	// ListEvery: Die Liste der Container höchstens so oft bei Docker
	// holen; dazwischen gilt die letzte (für den schnellen Takt in Live).
	// 0: bei jeder Messung.
	ListEvery time.Duration

	mu      sync.Mutex
	prev    map[string]counter // Zähler der letzten Messung
	inspect map[string]inspected
	keys    map[string]string // ID → Zustand beim letzten Inspect
	list    []dockerapi.OpsContainer
	listAt  time.Time
}

// Reset vergisst die Zählerstände der letzten Messung; die nächste
// Messung liefert für Raten erst wieder ab der übernächsten Werte.
func (c *Collector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prev = nil
}

type counter struct {
	v  float64
	at time.Time
}

type inspected struct {
	pid      int
	hostNet  bool
	memLimit int64
	restarts int
	health   string
}

func (c *Collector) proc() string {
	if c.Proc == "" {
		return "/proc"
	}
	return c.Proc
}

func (c *Collector) sys() string {
	if c.Sys == "" {
		return "/sys"
	}
	return c.Sys
}

// Collect misst einmal.
func (c *Collector) Collect(ctx context.Context, now time.Time) (Frame, Info) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prev == nil {
		c.prev = map[string]counter{}
	}
	f := Frame{At: now, Values: map[string]float64{}}
	var info Info
	next := map[string]counter{}
	rate := func(key string, total float64) {
		next[key] = counter{total, now}
		p, ok := c.prev[key]
		if !ok || total < p.v {
			return // erste Messung oder Zähler neu (Neustart)
		}
		dt := now.Sub(p.at).Seconds()
		if dt <= 0 {
			return
		}
		f.Values[key] = (total - p.v) / dt
	}

	c.hostCPU(f, next, &info)
	c.hostMem(f, &info)
	c.hostLoad(f)
	r, w := c.diskIO()
	rate(KeyRead, r)
	rate(KeyWrite, w)
	in, out := c.netDev(filepath.Join(c.proc(), "1", "net", "dev"), c.physical)
	rate(KeyNetIn, in)
	rate(KeyNetOut, out)
	c.disks(f, &info)
	c.containers(ctx, f, &info, rate)

	c.prev = next
	return f, info
}

// hostCPU liest /proc/stat. Prozent brauchen zwei Messungen; die Zähler
// liegen darum in next.
func (c *Collector) hostCPU(f Frame, next map[string]counter, info *Info) {
	b, err := os.ReadFile(filepath.Join(c.proc(), "stat"))
	if err != nil {
		info.Problems = append(info.Problems, "Prozessor: "+err.Error())
		return
	}
	var vals []float64
	for _, line := range strings.Split(string(b), "\n") {
		fs := strings.Fields(line)
		if len(fs) == 0 {
			continue
		}
		switch {
		case fs[0] == "cpu":
			for _, x := range fs[1:] {
				n, _ := strconv.ParseFloat(x, 64)
				vals = append(vals, n)
			}
		case strings.HasPrefix(fs[0], "cpu"):
			info.Cores++
		}
	}
	if len(vals) < 8 {
		return
	}
	// user nice system idle iowait irq softirq steal
	names := []string{"user", "nice", "system", "idle", "iowait", "irq", "softirq", "steal"}
	var d [8]float64
	var total float64
	ok := true
	for i, n := range names {
		k := "cpu/" + n
		next[k] = counter{v: vals[i], at: f.At}
		p, seen := c.prev[k]
		if !seen || vals[i] < p.v {
			ok = false
			continue
		}
		d[i] = vals[i] - p.v
		total += d[i]
	}
	if !ok || total == 0 {
		return
	}
	f.Values[KeyCPU] = 100 * (total - d[3] - d[4]) / total
	f.Values[KeyIOWait] = 100 * d[4] / total
	f.Values[KeySteal] = 100 * d[7] / total
}

func (c *Collector) hostMem(f Frame, info *Info) {
	m := readKV(filepath.Join(c.proc(), "meminfo"))
	total, avail := m["MemTotal"]*1024, m["MemAvailable"]*1024
	if total == 0 {
		info.Problems = append(info.Problems, "Arbeitsspeicher: /proc/meminfo nicht lesbar")
		return
	}
	info.RAMTotal = total
	info.SwapTotal = m["SwapTotal"] * 1024
	f.Values[KeyRAM] = float64(total - avail)
	f.Values[KeySwap] = float64((m["SwapTotal"] - m["SwapFree"]) * 1024)
}

func (c *Collector) hostLoad(f Frame) {
	b, err := os.ReadFile(filepath.Join(c.proc(), "loadavg"))
	if err != nil {
		return
	}
	if fs := strings.Fields(string(b)); len(fs) > 0 {
		if v, err := strconv.ParseFloat(fs[0], 64); err == nil {
			f.Values[KeyLoad] = v
		}
	}
}

// readKV liest Zeilen wie „MemTotal:  1234 kB“ (meminfo) oder
// „usage_usec 1234“ (cpu.stat, memory.stat).
func readKV(path string) map[string]int64 {
	out := map[string]int64{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		fs := strings.Fields(line)
		if len(fs) < 2 {
			continue
		}
		n, err := strconv.ParseInt(fs[1], 10, 64)
		if err != nil {
			continue
		}
		out[strings.TrimSuffix(fs[0], ":")] = n
	}
	return out
}

// diskIO summiert gelesene und geschriebene Byte aller ganzen Platten
// (Einträge in /sys/block ohne loop, ram, zram, dm und md; Partitionen
// und LVM zählten sonst doppelt).
func (c *Collector) diskIO() (read, write float64) {
	b, err := os.ReadFile(filepath.Join(c.proc(), "diskstats"))
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		fs := strings.Fields(line)
		if len(fs) < 10 || !c.wholeDisk(fs[2]) {
			continue
		}
		r, _ := strconv.ParseFloat(fs[5], 64)
		w, _ := strconv.ParseFloat(fs[9], 64)
		read += r * 512
		write += w * 512
	}
	return read, write
}

func (c *Collector) wholeDisk(dev string) bool {
	for _, p := range []string{"loop", "ram", "zram", "dm-", "md", "sr", "fd"} {
		if strings.HasPrefix(dev, p) {
			return false
		}
	}
	_, err := os.Stat(filepath.Join(c.sys(), "block", dev))
	return err == nil
}

// physical: Netzkarten mit Gerät (auch virtio); Brücken, veth, WireGuard
// und Tunnel haben keins und zählten sonst doppelt.
func (c *Collector) physical(name string) bool {
	if name == "lo" {
		return false
	}
	_, err := os.Stat(filepath.Join(c.sys(), "class", "net", name, "device"))
	return err == nil
}

// netDev summiert empfangene und gesendete Byte aus einer net/dev-Datei.
func (c *Collector) netDev(path string, keep func(string) bool) (in, out float64) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if !keep(name) {
			continue
		}
		fs := strings.Fields(rest)
		if len(fs) < 9 {
			continue
		}
		r, _ := strconv.ParseFloat(fs[0], 64)
		t, _ := strconv.ParseFloat(fs[8], 64)
		in += r
		out += t
	}
	return in, out
}

// Dateisysteme, die echte Laufwerke sind.
var realFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "f2fs": true}

// disks misst die belegten Byte je Laufwerk (eines je Gerät, das kürzeste
// Einhängen gewinnt; /boot und ähnliche bleiben, sie können auch volllaufen).
func (c *Collector) disks(f Frame, info *Info) {
	if c.Disk == nil {
		return
	}
	b, err := os.ReadFile(filepath.Join(c.proc(), "1", "mounts"))
	if err != nil {
		return
	}
	byDev := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		fs := strings.Fields(line)
		if len(fs) < 3 || !realFS[fs[2]] {
			continue
		}
		dev, mnt := fs[0], unescape(fs[1])
		// Einzelne Dateien, die Docker in einen Container einhängt
		// (/etc/hosts, /etc/resolv.conf), sind kein Laufwerk; läuft HostDeck
		// selbst in einem Container, hießen sie sonst „Laufwerk /etc/hosts“.
		// Was sich nicht prüfen lässt, bleibt.
		if st, err := os.Stat(mnt); err == nil && !st.IsDir() {
			continue
		}
		if old, ok := byDev[dev]; !ok || len(mnt) < len(old) {
			byDev[dev] = mnt
		}
	}
	mounts := make([]string, 0, len(byDev))
	for _, m := range byDev {
		mounts = append(mounts, m)
	}
	sort.Strings(mounts)
	for _, m := range mounts {
		total, free, err := c.Disk(m)
		if err != nil {
			info.Problems = append(info.Problems, "Platte "+m+": "+err.Error())
			continue
		}
		if total <= 0 {
			continue
		}
		info.Disks = append(info.Disks, DiskInfo{Mount: m, Total: total, Used: total - free})
		f.Values[diskPrefix+m] = float64(total - free)
	}
}

// unescape löst \040 und Ähnliches aus /proc/mounts auf.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// containers misst jeden laufenden Container über seine cgroup und sein
// Netz. Inspect nur, wenn sich der Container geändert hat.
func (c *Collector) containers(ctx context.Context, f Frame, info *Info, rate func(string, float64)) {
	if c.Docker == nil {
		return
	}
	if c.inspect == nil {
		c.inspect, c.keys = map[string]inspected{}, map[string]string{}
	}
	list := c.list
	if c.ListEvery <= 0 || c.listAt.IsZero() || f.At.Sub(c.listAt) >= c.ListEvery || f.At.Before(c.listAt) {
		l, err := c.Docker.List(ctx)
		if dockerapi.Missing(err) {
			// Kein Docker auf dem Server: nichts zu messen, keine Warnung.
			l, err = nil, nil
		}
		if err != nil {
			info.Problems = append(info.Problems, "Docker: "+err.Error())
			return
		}
		list, c.list, c.listAt = l, l, f.At
	}
	seen := map[string]bool{}
	for _, oc := range list {
		name := oc.Name()
		ci := ContainerInfo{Name: name, Project: oc.Labels["com.docker.compose.project"], State: oc.State}
		cacheKey := oc.ID + "|" + oc.State + "|" + oc.Status
		seen[oc.ID] = true
		in, ok := c.inspect[oc.ID]
		// Status trägt Laufzeit und Gesundheit („Up 3 hours (healthy)“):
		// Ändert er sich oder ist der Prozess weg, fragt HostDeck neu.
		if !ok || cacheKey != c.lastKey(oc.ID) || (oc.State == "running" && !c.alive(in.pid)) {
			ins, err := c.Docker.Inspect(ctx, oc.ID)
			if err == nil {
				in = inspected{pid: ins.State.Pid, hostNet: ins.HostConfig.NetworkMode == "host" || strings.HasPrefix(ins.HostConfig.NetworkMode, "container:"),
					memLimit: ins.HostConfig.Memory, restarts: ins.RestartCount}
				if ins.State.Health != nil {
					in.health = ins.State.Health.Status
				}
				c.inspect[oc.ID] = in
				c.keys[oc.ID] = cacheKey
			}
		}
		ci.HostNet, ci.MemLimit, ci.Restarts, ci.Health = in.hostNet, in.memLimit, in.restarts, in.health
		info.Containers = append(info.Containers, ci)
		if oc.State != "running" || in.pid <= 0 {
			continue
		}
		cg := c.cgroupOf(in.pid)
		if cg == "" {
			continue
		}
		cpu := readKV(filepath.Join(cg, "cpu.stat"))
		if u, ok := cpu["usage_usec"]; ok {
			rate(ContainerKey(name, ValCPU), float64(u)/1e4) // µs/s / 1e4 = % eines Kerns
		}
		if cur, err := readInt(filepath.Join(cg, "memory.current")); err == nil {
			inactive := readKV(filepath.Join(cg, "memory.stat"))["inactive_file"]
			if inactive > cur {
				inactive = 0
			}
			f.Values[ContainerKey(name, ValRAM)] = float64(cur - inactive)
		}
		r, w := ioStat(filepath.Join(cg, "io.stat"))
		rate(ContainerKey(name, ValRead), r)
		rate(ContainerKey(name, ValWrite), w)
		if !in.hostNet {
			ni, no := c.netDev(filepath.Join(c.proc(), strconv.Itoa(in.pid), "net", "dev"), func(n string) bool { return n != "lo" })
			rate(ContainerKey(name, ValNetIn), ni)
			rate(ContainerKey(name, ValNetOut), no)
		}
	}
	// Summen je Compose-Projekt: Nur so ist die Spitze eines Projekts
	// genau. Die Summe der Spitzen seiner Container wäre zu hoch, wenn sie
	// nicht gleichzeitig kamen.
	for _, ci := range info.Containers {
		if ci.Project == "" {
			continue
		}
		for _, val := range []string{ValCPU, ValRAM, ValNetIn, ValNetOut} {
			if v, ok := f.Values[ContainerKey(ci.Name, val)]; ok {
				f.Values[ProjectKey(ci.Project, val)] += v
			}
		}
	}
	for id := range c.inspect {
		if !seen[id] {
			delete(c.inspect, id)
			delete(c.keys, id)
		}
	}
	sort.Slice(info.Containers, func(i, j int) bool { return info.Containers[i].Name < info.Containers[j].Name })
}

func (c *Collector) lastKey(id string) string { return c.keys[id] }

func (c *Collector) alive(pid int) bool {
	_, err := os.Stat(filepath.Join(c.proc(), strconv.Itoa(pid)))
	return err == nil
}

// cgroupOf liest die cgroup-v2-Zeile „0::/pfad“ aus /proc/<pid>/cgroup.
// Das klappt mit dem systemd- und dem cgroupfs-Treiber gleich.
func (c *Collector) cgroupOf(pid int) string {
	b, err := os.ReadFile(filepath.Join(c.proc(), strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok && p != "" {
			return filepath.Join(c.sys(), "fs", "cgroup", filepath.Clean("/"+p))
		}
	}
	return ""
}

func readInt(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// ioStat summiert rbytes und wbytes aller Geräte aus io.stat.
func ioStat(path string) (read, write float64) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		for _, kv := range strings.Fields(line) {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			n, _ := strconv.ParseFloat(v, 64)
			switch k {
			case "rbytes":
				read += n
			case "wbytes":
				write += n
			}
		}
	}
	return read, write
}
