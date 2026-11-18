package discover

import (
	"context"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// foundThreshold: Ordner außerhalb der geprüften Orte erscheinen ab dieser
// Größe in der Auswahl. Kleineres zählt nur in der Platzübersicht.
var foundThreshold int64 = 100_000_000

// standardTop sind die üblichen Ordner direkt unter /. Alles andere dort
// hat jemand selbst angelegt und erscheint immer in der Auswahl.
var standardTop = map[string]bool{
	"bin": true, "boot": true, "cdrom": true, "dev": true, "etc": true, "home": true, "lib": true, "lib32": true,
	"lib64": true, "libx32": true, "lost+found": true, "media": true, "mnt": true, "opt": true, "proc": true,
	"root": true, "run": true, "sbin": true, "snap": true, "srv": true, "sys": true, "tmp": true, "usr": true, "var": true,
	// Markierungen von usrmerge (Ubuntu 24.04, Debian 12)
	"bin.usr-is-merged": true, "lib.usr-is-merged": true, "sbin.usr-is-merged": true,
	// Verweise auf den Kernel, die Debian direkt unter / anlegt
	"vmlinuz": true, "vmlinuz.old": true, "initrd.img": true, "initrd.img.old": true,
}

// systemSpace kommt mit dem neuen Betriebssystem oder entsteht neu.
var systemSpace = []string{
	"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/boot", "/snap", "/tmp", "/lost+found",
	"/var/cache", "/var/log", "/var/tmp", "/var/crash", "/var/backups", "/var/mail", "/var/spool",
	"/var/lib/apt", "/var/lib/dpkg", "/var/lib/snapd", "/var/lib/systemd", "/var/lib/cloud", "/var/lib/fwupd",
	"/var/lib/ubuntu-advantage", "/var/lib/command-not-found", "/var/lib/man-db", "/var/lib/PackageKit",
	"/var/lib/landscape", "/var/lib/update-notifier", "/var/lib/polkit-1", "/var/lib/private", "/var/lib/ucf",
	"/var/lib/aptitude", "/var/lib/swcatalog", "/var/lib/plymouth", "/var/lib/python", "/var/lib/grub",
	"/var/lib/initramfs-tools", "/var/lib/dhcp", "/var/lib/lxcfs", "/var/lib/caddy", "/var/lib/ufw",
	// per pip oder npm installierte Bibliotheken; die Programme dazu nennt /usr/local/bin
	"/usr/local/lib", "/usr/local/share", "/usr/local/include", "/usr/local/man",
	// HostDeck selbst: Daten des Dienstes samt Backup; auf dem neuen Server richtet
	// HostDeck sich neu ein. Das Backup in sich selbst zu sichern ergibt nichts.
	ort.DataDir,
}

// notSystem liegt unter Systemordnern, gehört aber nicht dazu.
var notSystem = []string{"/usr/local", "/usr/local/bin", "/usr/local/sbin", "/usr/local/etc"}

// spaceSkip wird nicht durchlaufen: virtuelle Dateisysteme und Docker,
// dessen Platz sich aus dem Rest ergibt.
var spaceSkip = []string{"/proc", "/sys", "/dev", "/run", "/var/run", "/var/lib/docker", "/var/lib/containerd"}

const (
	classUnknown = iota
	classData
	classSystem
	classSwap
)

// space ermittelt, wie sich der belegte Platz auf der Systemplatte
// verteilt, und meldet größere Ordner an Orten, die discover sonst nicht
// ansieht. Muss nach den Größen der Einträge laufen, die es wiederverwendet.
func (s *scan) space(ctx context.Context) {
	sf := s.d.Statfs
	if sf == nil {
		sf = func(p string) (int64, int64, error) { return statfs(s.d.FS.Real(p)) }
	}
	total, free, err := sf("/")
	if err != nil || total <= 0 {
		return
	}
	sp := &inventory.Space{Total: total, Used: total - free}

	class := map[string]int{}
	known := map[string]int64{} // Einträge mit vollständig bekannter Größe
	for _, p := range systemSpace {
		class[p] = classSystem
	}
	for _, p := range notSystem {
		class[p] = classUnknown
	}
	for _, e := range s.inv.Paths {
		if !e.Exists || e.Kind == inventory.KindParent || e.CoveredBy != "" {
			continue
		}
		class[e.Path] = classData
		if e.Size >= 0 && !e.SizePartial && len(e.Excludes) == 0 {
			known[e.Path] = e.Size
		}
	}
	for _, p := range s.inv.Projects {
		if p.DirExists && p.Path != "" {
			class[p.Path] = classData
		}
	}
	for _, u := range s.inv.Unclaimed {
		class[u.Path] = classData
		if u.Size >= 0 {
			known[u.Path] = u.Size
		}
	}
	if b, ok := s.readProc("/proc/swaps"); ok {
		for _, line := range strings.Split(string(b), "\n")[1:] {
			if f := strings.Fields(line); len(f) >= 2 && f[1] == "file" {
				class[f[0]] = classSwap
			}
		}
	}
	skip := map[string]bool{}
	for _, p := range spaceSkip {
		skip[p] = true
	}
	if rd := s.inv.Docker.RootDir; rd != "" {
		skip[hostfs.Clean(rd)] = true
	}

	// Gruppen für die größten Ordner: jeder Eintrag der Klassen hat seine
	// Wurzel (etwa /usr oder ein Projektordner); Unbekanntes geht in
	// spaceBucket. Die Daten von HostDeck zeigen ihre Unterordner einzeln.
	gmemo := map[string]string{}
	var groupOf func(p string) string
	groupOf = func(p string) string {
		if g, ok := gmemo[p]; ok {
			return g
		}
		g := ""
		switch {
		case p == "/":
		case p != ort.DataDir && strings.HasPrefix(p, ort.DataDir+"/"):
			rest := strings.TrimPrefix(p, ort.DataDir+"/")
			first, _, _ := strings.Cut(rest, "/")
			g = ort.DataDir + "/" + first
		default:
			if _, ok := class[p]; ok {
				g = p
			} else {
				g = groupOf(path.Dir(p))
			}
		}
		gmemo[p] = g
		return g
	}
	parts := map[string]int64{}

	memo := map[string]int{}
	var classOf func(p string) int
	classOf = func(p string) int {
		if c, ok := class[p]; ok {
			return c
		}
		if c, ok := memo[p]; ok {
			return c
		}
		c := classUnknown
		if p != "/" {
			c = classOf(path.Dir(p))
		}
		memo[p] = c
		return c
	}

	root := s.d.FS.Real("/")
	var rootDev uint64
	if fi, err := s.d.FS.Lstat("/"); err == nil {
		_, rootDev, _, _ = diskUsage(fi)
	}
	deadline := time.Now().Add(2 * s.o.SizeTimeout)
	seen := map[[2]uint64]bool{}
	buckets := map[string]int64{}
	files := map[string]int{} // Dateien je Bündel; leere Ordner zählen nicht als Fund
	var sums [4]int64
	_ = filepath.WalkDir(root, func(fp string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			sp.Partial = true
			return fs.SkipAll
		}
		hp := hostPath(root, fp)
		if d.IsDir() {
			if skip[hp] {
				return fs.SkipDir
			}
			if sz, ok := known[hp]; ok {
				sums[classData] += sz
				parts[hp] += sz
				return fs.SkipDir
			}
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size, dev, ino, links := diskUsage(info)
		if d.IsDir() && hp != "/" && dev != rootDev {
			return fs.SkipDir // anderes Dateisystem
		}
		if links > 1 && !d.IsDir() {
			k := [2]uint64{dev, ino}
			if seen[k] {
				return nil
			}
			seen[k] = true
		}
		c := classOf(hp)
		if !d.IsDir() {
			if fc, ok := class[hp]; ok {
				c = fc
			}
		}
		sums[c] += size
		if c != classUnknown {
			g := ""
			if _, ok := class[hp]; ok && !d.IsDir() {
				g = hp
			} else if d.IsDir() {
				g = groupOf(hp)
			} else {
				g = groupOf(path.Dir(hp))
			}
			if g != "" {
				parts[g] += size
			}
		}
		if c == classUnknown && hp != "/" {
			b := spaceBucket(hp)
			buckets[b] += size
			if !d.IsDir() {
				files[b]++
			}
		}
		return nil
	})
	sp.Data, sp.System, sp.Swap = sums[classData], sums[classSystem], sums[classSwap]
	sp.Walked = sums[0] + sums[1] + sums[2] + sums[3]
	for _, v := range s.inv.Volumes {
		if v.Size > 0 {
			sp.Volumes += v.Size
		}
	}

	// Große Ordner und alles Eigene direkt unter / kommen in die Auswahl.
	names := make([]string, 0, len(buckets))
	for b := range buckets {
		names = append(names, b)
	}
	sort.Strings(names)
	other := int64(0)
	for _, b := range names {
		sz := buckets[b]
		top, _, _ := strings.Cut(strings.TrimPrefix(b, "/"), "/")
		own := !standardTop[top] && !strings.HasPrefix(top, ".")
		if files[b] > 0 && (sz >= foundThreshold || own) {
			s.inv.Unclaimed = append(s.inv.Unclaimed, inventory.Unclaimed{Key: "path:" + b, Path: b, Area: inventory.AreaFound, Size: sz,
				Note: "liegt außerhalb der Orte, die HostDeck sonst prüft"})
			sp.Data += sz
			continue
		}
		other += sz
	}
	sp.Other = other
	sp.Docker = max(0, sp.Used-sp.Walked-sp.Volumes)
	for b, sz := range buckets {
		parts[b] += sz
	}
	sp.Top = topParts(parts, func(p string) string {
		switch {
		case p == ort.DataDir || strings.HasPrefix(p, ort.DataDir+"/"):
			return "hostdeck"
		}
		switch classOf(p) {
		case classData:
			return "daten"
		case classSystem:
			return "system"
		case classSwap:
			return "swap"
		}
		for _, u := range s.inv.Unclaimed {
			if u.Path == p {
				return "daten"
			}
		}
		return "sonst"
	})
	s.inv.Space = sp
	if sp.Partial {
		s.addFinding(inventory.LevelInfo, "platz-unvollstaendig", "", "Die Platzübersicht ist unvollständig (Zeitlimit erreicht).")
	}
}

// topN Ordner zeigt die Platzübersicht, ab topMin Byte.
const topN = 12

var topMin int64 = 50_000_000

func topParts(parts map[string]int64, kind func(string) string) []inventory.SpacePart {
	var out []inventory.SpacePart
	for p, sz := range parts {
		if sz >= topMin {
			out = append(out, inventory.SpacePart{Path: p, Size: sz})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > topN {
		out = out[:topN]
	}
	for i := range out {
		out[i].Kind = kind(out[i].Path)
	}
	return out
}

func hostPath(root, real string) string {
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

// spaceBucket fasst Unbekanntes zu Ordnern zusammen, die man versteht:
// /var/lib/mysql, /usr/local/go, /mnt/alt, eigene Ordner direkt unter /.
func spaceBucket(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	depth := 2
	switch {
	case !standardTop[parts[0]]:
		depth = 1
	case parts[0] == "var" || parts[0] == "usr":
		depth = 3
	}
	if len(parts) > depth {
		parts = parts[:depth]
	}
	return "/" + strings.Join(parts, "/")
}

// spaceText beschreibt die Platzübersicht in einem Satz für die Hinweise.
func spaceText(sp *inventory.Space) string {
	return "Belegt sind " + units.HumanSize(sp.Used) + " von " + units.HumanSize(sp.Total) + ": Daten " + units.HumanSize(sp.Data+sp.Volumes) +
		", Docker-Images und Build-Cache " + units.HumanSize(sp.Docker) + ", System " + units.HumanSize(sp.System) +
		", Swap " + units.HumanSize(sp.Swap) + ", Kleinteiliges " + units.HumanSize(sp.Other) + "."
}
