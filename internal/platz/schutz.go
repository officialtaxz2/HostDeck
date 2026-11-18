package platz

import (
	"path"
	"sort"
	"strings"
)

// Schutzstufen beim Löschen. Der Dienst prüft sie selbst; die Oberfläche
// zeigt sie nur an.
const (
	// LockAll: weder der Ordner noch etwas darin lässt sich löschen.
	LockAll = 1
	// LockSelf: der Ordner selbst nicht, sein Inhalt einzeln schon.
	LockSelf = 2
	// Free: löschbar.
	Free = 3
)

// Facts ist, was HostDeck über den Server weiß und für den Schutz braucht.
type Facts struct {
	// DockerRoot ist der Ordner von Docker laut docker info.
	DockerRoot string
	// DockerRoots sind weitere Ordner von Docker (etwa der echte Ort, wenn
	// DockerRoot eine Verknüpfung ist).
	DockerRoots []string
	// Incomplete: Was fehlt, damit der Schutz vollständig ist (etwa weil
	// Docker nicht antwortet); leer: vollständig. Dann löscht HostDeck nichts.
	Incomplete string
	// Mounts sind die Einhängepunkte (aus mountinfo).
	Mounts []string
	// Swaps sind aktive Swap-Dateien.
	Swaps []string
	// Own sind Ordner und Dateien von HostDeck selbst, je mit Weg dorthin.
	Own []Own
	// Projects sind die Compose-Projekte.
	Projects []Project
	// Containers sind alle Container mit ihren Einbindungen.
	Containers []Container
	// Volumes sind die Docker-Volumes mit ihrem Ordner, damit die Liste
	// Namen statt Hash-Ordnern zeigt.
	Volumes []Volume
	// Homes sind die Home-Ordner der Benutzer (aus /etc/passwd), Name zu
	// Ordner.
	Homes map[string]string
}

// Volume ist ein Docker-Volume.
type Volume struct {
	Name  string
	Path  string   // Ordner auf dem Server (meist …/volumes/<name>)
	Users []string // Container, die es einbinden
	// Device: Ordner auf dem Server, den das Volume einbindet (Option
	// o=bind); leer: keiner.
	Device string
}

// Own ist ein Ort von HostDeck.
type Own struct {
	Path string
	What string // etwa „Backups auf dem Server“
	Page string // Seite im Leitstand, etwa #/backup; leer: keine
}

// Project ist ein Compose-Projekt.
type Project struct {
	Name  string
	Dir   string
	Files []string // Compose-Dateien, env_file, .env
}

// Container ist ein Container mit seinen Einbindungen vom Server.
type Container struct {
	Name    string
	Running bool
	Binds   []Bind
}

// Bind ist ein Ordner oder eine Datei des Servers in einem Container.
type Bind struct {
	Source string
	RW     bool
}

// Verdict sagt, was mit einem Pfad geht und was er ist.
type Verdict struct {
	Level  int      `json:"stufe"`
	Reason string   `json:"grund,omitempty"`
	Kind   string   `json:"art,omitempty"`
	Page   string   `json:"seite,omitempty"`
	Warn   []string `json:"warnungen,omitempty"`
}

// lockedSystem: alles darunter gehört zum Betriebssystem oder ist virtuell.
var lockedSystem = []string{
	"/usr", "/etc", "/boot", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/snap", "/lost+found",
	"/proc", "/sys", "/dev", "/run", "/var/run", "/var/lock",
	"/var/lib/dpkg", "/var/lib/apt", "/var/lib/rpm", "/var/lib/dnf", "/var/lib/yum", "/var/lib/systemd", "/var/lib/snapd",
	"/var/lib/ucf", "/var/lib/polkit-1", "/var/lib/private", "/var/lib/grub", "/var/lib/initramfs-tools",
	"/var/lib/dhcp", "/var/lib/cloud", "/var/lib/sudo", "/var/cache/debconf", "/var/spool/cron",
}

// notLocked liegt unter gesperrten Systemordnern, gehört aber dir.
var notLocked = []string{"/usr/local"}

// lockedDocker: Docker und containerd verwalten das selbst.
var lockedDocker = []string{"/var/lib/docker", "/var/lib/containerd"}

// selfOnly: der Ordner selbst bleibt, sein Inhalt lässt sich einzeln löschen.
var selfOnly = []string{
	"/", "/var", "/var/lib", "/var/log", "/var/tmp", "/var/cache", "/var/spool", "/var/mail", "/var/backups",
	"/tmp", "/home", "/root", "/opt", "/srv", "/mnt", "/media",
	"/usr/local", "/usr/local/bin", "/usr/local/sbin", "/usr/local/lib", "/usr/local/share", "/usr/local/etc",
}

// broadBind: Einbindungen dieser Ordner (etwa / bei Überwachungs-Containern)
// vererben keine Warnung, sonst stünde sie an jeder Zeile.
var broadBind = map[string]bool{}

func init() {
	for _, p := range selfOnly {
		broadBind[p] = true
	}
	for _, p := range lockedSystem {
		broadBind[p] = true
	}
}

// Guard beurteilt Pfade nach den Fakten.
type Guard struct {
	all      []rule // LockAll für den Pfad und alles darunter
	self     map[string]rule
	anchors  []string // wer davon etwas enthält, ist selbst LockSelf
	projects []Project
	binds    []bindUse
	mounts   map[string]bool
	volumes  []rule // nur zum Einordnen
	// incomplete: der Schutz kennt nicht alles; dann löscht HostDeck nichts.
	incomplete string
	isDir      func(string) bool
}

// Incomplete sagt, was dem Schutz fehlt; leer: nichts.
func (g *Guard) Incomplete() string { return g.incomplete }

type rule struct {
	path, reason, kind, page string
}

type bindUse struct {
	src     string
	who     string
	running bool
	rw      bool
	file    bool
}

// NewGuard baut den Schutz aus den Fakten. isDir sagt, ob ein Pfad ein
// Ordner ist (für eingebundene Dateien).
func NewGuard(f Facts, isDir func(string) bool) *Guard {
	g := &Guard{self: map[string]rule{}, mounts: map[string]bool{}, incomplete: f.Incomplete, isDir: isDir}
	addAll := func(p, reason, kind, page string) {
		if p = cleanAbs(p); p != "" && p != "/" {
			g.all = append(g.all, rule{p, reason, kind, page})
			g.anchors = append(g.anchors, p)
		}
	}
	addSelf := func(p, reason, kind string) {
		if p = cleanAbs(p); p != "" {
			if _, ok := g.self[p]; !ok {
				g.self[p] = rule{p, reason, kind, ""}
			}
			g.anchors = append(g.anchors, p)
		}
	}
	for _, o := range f.Own {
		addAll(o.Path, "gehört zu HostDeck", "hostdeck", o.Page)
	}
	for _, p := range lockedDocker {
		addAll(p, "verwaltet Docker; aufräumen geht weiter unten unter Vorschläge", "Docker", "")
	}
	dockerRoots := append([]string{f.DockerRoot}, f.DockerRoots...)
	for _, p := range dockerRoots {
		if p != "" {
			addAll(p, "verwaltet Docker; aufräumen geht weiter unten unter Vorschläge", "Docker", "")
		}
	}
	for _, p := range lockedSystem {
		addAll(p, "gehört zum Betriebssystem", "System", "")
	}
	for _, s := range f.Swaps {
		addAll(s, "ist gerade als Swap in Benutzung", "Swap", "")
	}
	for _, p := range f.Projects {
		for _, file := range p.Files {
			addAll(file, "gehört zum Projekt "+p.Name+" (Compose)", "Projekt "+p.Name, "")
		}
		if p.Dir != "" {
			addSelf(p.Dir, "ist der Ordner des Projekts "+p.Name, "Projekt "+p.Name)
			g.projects = append(g.projects, Project{Name: p.Name, Dir: cleanAbs(p.Dir)})
		}
	}
	for _, m := range f.Mounts {
		if m = cleanAbs(m); m != "" && m != "/" {
			g.mounts[m] = true
			addSelf(m, "ist ein Einhängepunkt (eine andere Platte oder ein eingebundener Ordner)", "Einhängepunkt")
		}
	}
	// Ordner, die ein Volume mit o=bind einbindet, vor den Einbindungen:
	// Der Name des Volumes sagt mehr.
	for _, v := range f.Volumes {
		if d := cleanAbs(v.Device); d != "" && d != "/" && v.Name != "" {
			addSelf(d, "ist der Ordner des Docker-Volumes "+v.Name+"; sein Inhalt lässt sich einzeln löschen", "Volume "+v.Name)
		}
	}
	for _, c := range f.Containers {
		for _, b := range c.Binds {
			src := cleanAbs(b.Source)
			if src == "" || broadBind[src] || underAny(src, dockerRoots) || under(src, "/var/lib/docker") || under(src, "/proc") || under(src, "/sys") || under(src, "/dev") || under(src, "/run") || under(src, "/var/run") {
				continue
			}
			file := isDir != nil && !isDir(src)
			g.binds = append(g.binds, bindUse{src: src, who: c.Name, running: c.Running, rw: b.RW, file: file})
			if file {
				addAll(src, "ist in den Container "+c.Name+" eingebunden", "eingebunden in "+c.Name, "")
			} else {
				addSelf(src, "ist in den Container "+c.Name+" eingebunden; sein Inhalt lässt sich einzeln löschen", "eingebunden in "+c.Name)
			}
		}
	}
	for _, p := range selfOnly {
		addSelf(p, "ist ein Grundordner des Servers; sein Inhalt lässt sich einzeln löschen", "")
	}
	for user, h := range f.Homes {
		if h = cleanAbs(h); h != "" && !broadBind[h] {
			addSelf(h, "ist der Home-Ordner von "+user+"; sein Inhalt lässt sich einzeln löschen", "")
			addAll(path.Join(h, ".ssh"), "enthält SSH-Zugänge", "SSH", "")
		}
	}
	for _, v := range f.Volumes {
		if p := cleanAbs(v.Path); p != "" && v.Name != "" {
			kind := "Volume " + v.Name
			if len(v.Users) > 0 {
				kind += " · " + strings.Join(v.Users, ", ")
			} else {
				kind += " · ohne Container"
			}
			g.volumes = append(g.volumes, rule{path: p, kind: kind})
		}
	}
	sort.SliceStable(g.volumes, func(i, j int) bool { return len(g.volumes[i].path) > len(g.volumes[j].path) })
	// Längere Pfade zuerst: die genaueste Regel gewinnt.
	sort.SliceStable(g.all, func(i, j int) bool { return len(g.all[i].path) > len(g.all[j].path) })
	sort.SliceStable(g.projects, func(i, j int) bool { return len(g.projects[i].Dir) > len(g.projects[j].Dir) })
	return g
}

func cleanAbs(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") {
		return ""
	}
	return path.Clean(p)
}

// under sagt, ob p gleich base ist oder darin liegt.
func under(p, base string) bool {
	if base == "" {
		return false
	}
	return p == base || base == "/" || strings.HasPrefix(p, base+"/")
}

// Check beurteilt host (ein Pfad auf dem Server).
func (g *Guard) Check(host string) Verdict {
	host = path.Clean("/" + host)
	v := Verdict{Level: Free, Kind: kindByName(host)}
	for _, p := range g.projects {
		if under(host, p.Dir) && v.Kind == "" {
			v.Kind = "Projekt " + p.Name
			break
		}
	}
	for _, r := range g.all {
		if !under(host, r.path) {
			continue
		}
		if r.kind == "System" && underAny(host, notLocked) {
			continue
		}
		v.Level, v.Reason, v.Page = LockAll, r.reason, r.page
		if r.kind != "" {
			v.Kind = r.kind
		}
		if r.kind == "Docker" {
			for _, vol := range g.volumes {
				if under(host, vol.path) {
					v.Kind = vol.kind
					break
				}
			}
		}
		return v
	}
	// Direkt unter / liegen außer Ordnern nur Dinge des Systems (etwa
	// /vmlinuz, /initrd.img), dazu Debians *.usr-is-merged.
	top := host
	for path.Dir(top) != "/" && top != "/" {
		top = path.Dir(top)
	}
	if strings.HasSuffix(top, ".usr-is-merged") {
		v.Level, v.Reason, v.Kind = LockAll, "gehört zum Betriebssystem", "System"
		return v
	}
	if path.Dir(host) == "/" && host != "/" && g.isDir != nil && !g.isDir(host) {
		v.Level, v.Reason = LockAll, "liegt direkt unter /; dort löscht HostDeck nur Ordner (das System legt hier Dateien wie /vmlinuz ab). Verschieben oder per SSH löschen"
		return v
	}
	if r, ok := g.self[host]; ok {
		v.Level, v.Reason = LockSelf, r.reason
		if r.kind != "" {
			v.Kind = r.kind
		}
	}
	if v.Level == Free && (sshDir(host) || directChild(host, "/home") || directChild(host, "/var/lib")) {
		v.Level, v.Reason = LockSelf, "ist der Ordner eines Benutzers oder Dienstes; sein Inhalt lässt sich einzeln löschen"
		if sshDir(host) {
			v.Level, v.Reason, v.Kind = LockAll, "enthält SSH-Zugänge", "SSH"
			return v
		}
	}
	if v.Level == Free {
		for _, a := range g.anchors {
			if strings.HasPrefix(a, host+"/") || (host == "/" && a != "/") {
				v.Level, v.Reason = LockSelf, "enthält "+a+", das geschützt ist; der übrige Inhalt lässt sich einzeln löschen"
				break
			}
		}
	}
	// Inhalt von /var/lib/<dienst> gehört meist einem Dienst des Servers.
	if v.Level == Free {
		if parts := strings.Split(strings.TrimPrefix(host, "/"), "/"); len(parts) >= 4 && parts[0] == "var" && parts[1] == "lib" {
			v.Warn = append(v.Warn, "Gehört vermutlich zum Dienst "+parts[2]+" des Servers (/var/lib/"+parts[2]+"). Löschen kann ihn stören; wenn es geht, räume im Dienst selbst auf.")
		}
	}
	// Warnungen für Inhalt eingebundener Ordner.
	for _, b := range g.binds {
		if b.file || host == b.src || !under(host, b.src) {
			continue
		}
		if v.Kind == "" || strings.HasPrefix(v.Kind, "Archiv") || v.Kind == "Logs" {
			if v.Kind == "" {
				v.Kind = "eingebunden in " + b.who
			}
		}
		state := "gestoppt"
		if b.running {
			state = "läuft"
		}
		if b.rw {
			v.Warn = append(v.Warn, "Benutzt vom Container "+b.who+" ("+state+"). Programme führen oft eine eigene Liste dieser Dateien; wenn es geht, lösche im Programm selbst.")
		} else {
			v.Warn = append(v.Warn, "Nur lesend in den Container "+b.who+" eingebunden ("+state+").")
		}
	}
	return v
}

func underAny(p string, bases []string) bool {
	for _, b := range bases {
		if under(p, b) {
			return true
		}
	}
	return false
}

func directChild(p, parent string) bool {
	return path.Dir(p) == parent && p != parent
}

func sshDir(p string) bool {
	if path.Base(p) != ".ssh" && !strings.Contains(p, "/.ssh/") {
		return false
	}
	i := strings.Index(p, "/.ssh")
	home := p[:i]
	return home == "/root" || directChild(home, "/home")
}

// kindByName ordnet nach Endung und Ort ein (Archiv, Logs).
func kindByName(p string) string {
	b := strings.ToLower(path.Base(p))
	for _, ext := range []string{".tar", ".tar.gz", ".tgz", ".tar.xz", ".txz", ".tar.zst", ".tar.bz2", ".zip", ".7z", ".rar", ".gz", ".xz", ".bz2", ".zst", ".iso", ".img"} {
		if strings.HasSuffix(b, ext) {
			if under(p, "/var/log") {
				return "Logs"
			}
			return "Archiv"
		}
	}
	if under(p, "/var/log") || strings.HasSuffix(b, ".log") || strings.Contains(b, ".log.") {
		return "Logs"
	}
	return ""
}
