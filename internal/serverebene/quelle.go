package serverebene

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
)

// Source stellt auf dem alten Server die Punkte zusammen. Aufgaben und
// Einstellungen kommen aus der Bestandsaufnahme, die Inhalte liest HostDeck
// erst jetzt, frisch von der Platte.
type Source struct {
	Root string // Wurzel für Tests; leer: /
	Inv  *inventory.Inventory
	Host string // Rechnername, für den Kopf der Kernel-Datei
}

// SysctlFile nimmt auf dem neuen Server die Kernel-Einstellungen auf.
const SysctlFile = "/etc/sysctl.d/90-hostdeck-umzug.conf"

// CrontabFile nimmt die eigenen Zeilen aus /etc/crontab auf; /etc/crontab
// selbst ist auf jedem System anders.
const CrontabFile = "/etc/cron.d/hostdeck-crontab"

const maxFile = 1 << 20

func (s Source) path(p string) string {
	if s.Root == "" {
		return p
	}
	return filepath.Join(s.Root, p)
}

// read liest eine gewöhnliche Datei (keinen Symlink) mit Rechten und
// Besitzer.
func (s Source) read(p string) ([]byte, os.FileMode, uint32, uint32, bool) {
	fi, err := os.Lstat(s.path(p))
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFile {
		return nil, 0, 0, 0, false
	}
	b, err := os.ReadFile(s.path(p))
	if err != nil {
		return nil, 0, 0, 0, false
	}
	uid, gid, _ := fileIDs(fi)
	return b, fi.Mode().Perm(), uid, gid, true
}

// Items sind alle Punkte, die dieser Server hat, mit Inhalten.
func (s Source) Items() []Item {
	ids := readIDs(s.path("/etc/passwd"))
	gids := readIDs(s.path("/etc/group"))
	var out []Item
	if it, ok := s.timeZone(); ok {
		out = append(out, it)
	}
	if it, ok := s.kernel(); ok {
		out = append(out, it)
	}
	if it, ok := s.tuning(); ok {
		out = append(out, it)
	}
	out = append(out, s.tasks(ids, gids)...)
	out = append(out, s.access(ids)...)
	return out
}

// Rest sind geänderte Dateien in /etc, die HostDeck nicht selbst übernimmt.
func (s Source) Rest() []string {
	if s.Inv == nil {
		return nil
	}
	prio := map[string]bool{}
	for _, ps := range tuning.ReadPrio(s.Root) {
		prio[tuning.PrioDropIn(ps.Unit)] = true
	}
	var out []string
	for _, st := range s.Inv.Settings {
		p := st.Path
		switch {
		case isSysctl(p), strings.HasPrefix(p, "/etc/caddy/"), p == "/etc/docker/daemon.json",
			strings.HasPrefix(p, "/etc/systemd/system/"), strings.HasPrefix(p, "/etc/cron"), p == "/etc/timezone",
			prio[p], s.tuned(p):
			continue
		}
		out = append(out, p)
	}
	return out
}

// tuned sagt, ob vps-tune (tune.sh) die Datei verwaltet. Solche Dateien
// ziehen nicht einzeln mit; der neue Leitstand bietet das Tuning als Ganzes
// an.
func (s Source) tuned(p string) bool {
	b, _, _, _, ok := s.read(p)
	return ok && tuning.Managed(b)
}

// tuning steht nur in der Liste: Was vps-tune eingestellt hat, wendet der
// neue Leitstand nach Vorschau neu an, passend zu dessen Kernel und
// Netzwerkkarte.
func (s Source) tuning() (Item, bool) {
	a := tuning.ReadApplied(s.Root)
	prio := tuning.ReadPrio(s.Root)
	if a == nil && len(prio) == 0 {
		return Item{}, false
	}
	it := Item{Key: "tuning", Kind: KindAuto, Title: "Tuning (vps-tune)",
		Manual: "zieht nicht als einzelne Dateien mit. Der Leitstand auf dem neuen Server bietet dasselbe Tuning unter Überwachung > Tuning an, mit Vorschau; Bootparameter wirken dort erst nach einem Neustart."}
	var parts []string
	if a != nil {
		it.Title = "Tuning (vps-tune, Preset " + a.Preset + ")"
		o := a.Offer()
		for _, sw := range tuning.Switches {
			if v, ok := o.Set[sw.Key]; ok {
				parts = append(parts, "--"+sw.Key+" "+v)
			}
		}
		if len(a.Boot) > 0 {
			parts = append(parts, "Bootparameter "+strings.Join(a.Boot, " "))
		}
	}
	for _, p := range prio {
		parts = append(parts, "CPU-Vorrang "+p.Unit+" "+p.Level)
	}
	it.Detail = strings.Join(parts, " · ")
	return it, true
}

func isSysctl(p string) bool {
	return p == "/etc/sysctl.conf" || (strings.HasPrefix(p, "/etc/sysctl.d/") && strings.HasSuffix(p, ".conf"))
}

func (s Source) timeZone() (Item, bool) {
	tz := ""
	if b, err := os.ReadFile(s.path("/etc/timezone")); err == nil {
		tz = strings.TrimSpace(string(b))
	}
	if tz == "" {
		if t, err := os.Readlink(s.path("/etc/localtime")); err == nil {
			if _, after, ok := strings.Cut(t, "zoneinfo/"); ok {
				tz = after
			}
		}
	}
	if tz == "" || strings.ContainsAny(tz, " \n\t") || strings.Contains(tz, "..") {
		return Item{}, false
	}
	return Item{Key: "zeitzone", Kind: KindAuto, Title: "Zeitzone " + tz, TimeZone: tz}, true
}

// kernel sammelt die selbst gesetzten Kernel-Einstellungen in eine Datei.
// Was Pakete mitbringen, zählt die Bestandsaufnahme nicht als geändert.
func (s Source) kernel() (Item, bool) {
	if s.Inv == nil {
		return Item{}, false
	}
	var keys []string
	val := map[string]string{}
	for _, st := range s.Inv.Settings {
		if !isSysctl(st.Path) {
			continue
		}
		b, _, _, _, ok := s.read(st.Path)
		if !ok || tuning.Managed(b) {
			continue
		}
		for _, line := range keyLines(b) {
			if line[0] == ';' {
				continue
			}
			k, v, ok := strings.Cut(strings.TrimPrefix(line, "-"), "=")
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if !ok || k == "" || strings.ContainsAny(k, " \t") {
				continue
			}
			if _, seen := val[k]; !seen {
				keys = append(keys, k)
			}
			val[k] = v
		}
	}
	if len(keys) == 0 {
		return Item{}, false
	}
	var b strings.Builder
	from := "dem alten Server"
	if s.Host != "" {
		from = s.Host
	}
	fmt.Fprintf(&b, "# Kernel-Einstellungen von %s, beim Umzug übernommen von HostDeck.\n", from)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, val[k])
	}
	data := []byte(b.String())
	return Item{Key: "kernel", Kind: KindAuto, Title: "Kernel-Einstellungen (" + strconv.Itoa(len(keys)) + ")", Detail: shortList(keys, 6), Sysctl: true,
		Files: []File{{Path: SysctlFile, Owner: "root", Group: "root", Mode: 0o644, How: HowFile, Hash: hashOf(data), Data: data}}}, true
}

func shortList(list []string, n int) string {
	if len(list) <= n {
		return strings.Join(list, ", ")
	}
	return strings.Join(list[:n], ", ") + " und " + strconv.Itoa(len(list)-n) + " weitere"
}

// file baut einen Eintrag aus einer Datei dieses Servers.
func (s Source) file(p, dest, how string, ids, gids map[uint32]string) (File, bool) {
	b, mode, uid, gid, ok := s.read(p)
	if !ok {
		return File{}, false
	}
	f := File{Path: dest, Owner: nameOf(ids, uid), Group: nameOf(gids, gid), Mode: uint32(mode), How: how, Hash: hashOf(b), Data: b}
	switch how {
	case HowLines:
		f.Lines = lineHashes(b)
	case HowDocker:
		auths, ok := dockerAuths(b)
		if !ok {
			return File{}, false
		}
		for r := range auths {
			f.Names = append(f.Names, r)
		}
		sort.Strings(f.Names)
	}
	return f, true
}

func nameOf(m map[uint32]string, id uint32) string {
	if n, ok := m[id]; ok {
		return n
	}
	return strconv.FormatUint(uint64(id), 10)
}

// ---------- Aufgaben ----------

var cronParts = map[string]bool{"/etc/cron.hourly": true, "/etc/cron.daily": true, "/etc/cron.weekly": true, "/etc/cron.monthly": true}

func (s Source) tasks(ids, gids map[uint32]string) []Item {
	if s.Inv == nil {
		return nil
	}
	bySource := map[string][]inventory.Task{}
	var order []string
	for _, t := range s.Inv.Tasks {
		if _, ok := bySource[t.Source]; !ok {
			order = append(order, t.Source)
		}
		bySource[t.Source] = append(bySource[t.Source], t)
	}
	var out []Item
	for _, src := range order {
		ts := bySource[src]
		var it Item
		var ok bool
		if ts[0].Kind != "cron" && s.tuned(src) {
			continue // vps-tune-zram.service: kommt mit dem Tuning
		}
		if ts[0].Kind == "cron" {
			it, ok = s.cronItem(src, ts, ids, gids)
		} else {
			it, ok = s.unitItem(ts[0], ids, gids)
		}
		if ok {
			out = append(out, it)
		}
	}
	return out
}

func taskLines(ts []inventory.Task) string {
	var parts []string
	for _, t := range ts {
		when := t.When
		if when == "" {
			when = t.Schedule
		}
		parts = append(parts, strings.TrimSpace(when+": "+t.Command))
	}
	return shortList(parts, 4)
}

func (s Source) cronItem(src string, ts []inventory.Task, ids, gids map[uint32]string) (Item, bool) {
	it := Item{Key: "aufgabe:" + src, Kind: KindTask, Cron: true, Detail: taskLines(ts)}
	users := map[string]bool{}
	for _, t := range ts {
		if t.User != "" {
			users[t.User] = true
		}
	}
	dir := path.Dir(src)
	switch {
	case src == "/etc/crontab":
		b, _, _, _, ok := s.read(src)
		if !ok {
			return Item{}, false
		}
		var keep []string
		for _, line := range keyLines(b) {
			f := strings.Fields(line)
			if k, _, isVar := strings.Cut(f[0], "="); isVar && !strings.ContainsAny(k, "*/@") {
				keep = append(keep, line) // SHELL=, PATH=, MAILTO= gelten nur in ihrer Datei
				continue
			}
			if strings.Contains(line, "run-parts") || strings.Contains(line, "/usr/sbin/anacron") {
				continue
			}
			keep = append(keep, line)
			it.Needs = append(it.Needs, program(cronCommand(f, true)))
		}
		data := []byte("# Eigene Zeilen aus /etc/crontab des alten Servers, beim Umzug übernommen von HostDeck.\n" + strings.Join(keep, "\n") + "\n")
		it.Title = "Cronjobs aus /etc/crontab (" + strconv.Itoa(len(ts)) + ")"
		it.Files = []File{{Path: CrontabFile, Owner: "root", Group: "root", Mode: 0o644, How: HowFile, Hash: hashOf(data), Data: data}}
	case cronParts[dir]:
		f, ok := s.file(src, src, HowFile, ids, gids)
		if !ok {
			return Item{}, false
		}
		it.Title = "Skript " + path.Base(src) + " (" + ts[0].When + ")"
		it.Files = []File{f}
		it.Needs = append(it.Needs, s.shebang(f.Data))
	case dir == "/etc/cron.d":
		f, ok := s.file(src, src, HowFile, ids, gids)
		if !ok {
			return Item{}, false
		}
		it.Title = "Cronjobs aus " + src + " (" + strconv.Itoa(len(ts)) + ")"
		it.Files = []File{f}
		for _, line := range keyLines(f.Data) {
			it.Needs = append(it.Needs, program(cronCommand(strings.Fields(line), true)))
		}
	default: // Crontab eines Benutzers
		user := path.Base(src)
		f, ok := s.file(src, "/var/spool/cron/crontabs/"+user, HowCrontab, ids, gids)
		if !ok {
			return Item{}, false
		}
		f.Owner, f.Group, f.Mode = user, "crontab", 0o600
		it.Title = "Cronjobs von " + user + " (" + strconv.Itoa(len(ts)) + ")"
		it.Files = []File{f}
		users[user] = true
		for _, line := range keyLines(f.Data) {
			it.Needs = append(it.Needs, program(cronCommand(strings.Fields(line), false)))
		}
	}
	for u := range users {
		it.Users = append(it.Users, u)
	}
	sort.Strings(it.Users)
	it.Needs = cleanNeeds(it.Needs)
	return it, true
}

// cronCommand ist der Befehl einer Cron-Zeile; system: mit Benutzer-Spalte.
func cronCommand(f []string, system bool) []string {
	if len(f) == 0 {
		return nil
	}
	if k, _, ok := strings.Cut(f[0], "="); ok && !strings.ContainsAny(k, "*/@") {
		return nil
	}
	n := 5
	if strings.HasPrefix(f[0], "@") {
		n = 1
	}
	if system {
		n++
	}
	if len(f) <= n {
		return nil
	}
	return f[n:]
}

// program ist das erste Wort eines Befehls, wenn es ein fester Pfad ist.
func program(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}
	p := strings.Trim(cmd[0], `"'`)
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "$`*?") {
		return ""
	}
	return path.Clean(p)
}

// shebang ist das Programm aus der ersten Zeile eines Skripts.
func (s Source) shebang(b []byte) string {
	line, _, _ := strings.Cut(string(b), "\n")
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	return program(strings.Fields(strings.TrimPrefix(line, "#!")))
}

func cleanNeeds(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range list {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (s Source) unitItem(t inventory.Task, ids, gids map[uint32]string) (Item, bool) {
	name := path.Base(t.Source)
	it := Item{Key: "aufgabe:" + t.Source, Kind: KindTask, Detail: strings.TrimSpace(strings.Trim(t.Description+" · "+t.Command, " ·"))}
	switch {
	case t.Timer != "":
		it.Title = "Timer " + strings.TrimSuffix(t.Timer, ".timer")
		if t.When != "" {
			it.Title += " (" + t.When + ")"
		}
	case strings.HasSuffix(name, ".mount"), strings.HasSuffix(name, ".automount"):
		it.Title = "Laufwerk " + name
		it.Manual = "Laufwerke sind auf dem neuen Server anders; ein falscher Eintrag kann den Start aufhalten. Bitte von Hand einrichten."
		return it, true
	default:
		it.Title = "Dienst " + strings.TrimSuffix(name, ".service")
	}
	files := []string{t.Source}
	if t.Timer != "" {
		files = append(files, path.Join(path.Dir(t.Source), t.Timer))
	}
	for _, unit := range append([]string{}, files...) {
		if es, err := os.ReadDir(s.path(unit + ".d")); err == nil {
			for _, e := range es {
				if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".conf") {
					files = append(files, unit+".d/"+e.Name())
				}
			}
		}
	}
	for _, p := range files {
		f, ok := s.file(p, p, HowFile, ids, gids)
		if !ok {
			return Item{}, false
		}
		it.Files = append(it.Files, f)
		needs, user := unitNeeds(f.Data)
		it.Needs = append(it.Needs, needs...)
		if user != "" {
			it.Users = append(it.Users, user)
		}
	}
	if t.Enabled {
		if t.Timer != "" {
			it.Units = []string{t.Timer}
		} else {
			it.Units = []string{name}
		}
	}
	it.Needs = cleanNeeds(it.Needs)
	it.Users = cleanNeeds(it.Users)
	return it, true
}

// unitNeeds liest aus einer Unit die Programme (ExecStart und Co.), die
// Pflicht-EnvironmentFiles und den Benutzer.
func unitNeeds(b []byte) (needs []string, user string) {
	for _, line := range keyLines(b) {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case strings.HasPrefix(k, "Exec"):
			needs = append(needs, program(strings.Fields(strings.TrimLeft(v, "-@+!:"))))
		case k == "EnvironmentFile" && !strings.HasPrefix(v, "-"):
			needs = append(needs, program([]string{v}))
		case k == "WorkingDirectory" && !strings.HasPrefix(v, "-"):
			needs = append(needs, program([]string{v}))
		case k == "User" && v != "" && strings.Trim(v, "0123456789") != "":
			user = v
		}
	}
	return needs, user
}

// ---------- Zugänge ----------

func (s Source) homes() []string {
	var out []string
	if fi, err := os.Stat(s.path("/root")); err == nil && fi.IsDir() {
		out = append(out, "/root")
	}
	if es, err := os.ReadDir(s.path("/home")); err == nil {
		for _, e := range es {
			if e.IsDir() && e.Name() != "lost+found" {
				out = append(out, "/home/"+e.Name())
			}
		}
	}
	return out
}

func (s Source) access(ids map[uint32]string) []Item {
	gids := readIDs(s.path("/etc/group"))
	var out []Item
	for _, h := range s.homes() {
		user := "root"
		if h != "/root" {
			fi, err := os.Stat(s.path(h))
			if err != nil {
				continue
			}
			uid, _, ok := fileIDs(fi)
			if !ok {
				continue
			}
			user = nameOf(ids, uid)
		}
		ssh := h + "/.ssh"
		if keys := s.privateKeys(ssh); len(keys) > 0 {
			it := Item{Key: "zugang:ssh:" + h, Kind: KindAccess, Users: []string{user},
				Title:  "SSH-Schlüssel von " + user + ": " + strings.Join(keys, ", "),
				Detail: "Damit meldet sich der Server woanders an, etwa bei GitHub für git pull. Dazu config und known_hosts, wenn es sie gibt."}
			var names []string
			for _, k := range keys {
				names = append(names, k, k+".pub")
			}
			names = append(names, "config")
			for _, n := range names {
				if f, ok := s.file(ssh+"/"+n, ssh+"/"+n, HowFile, ids, gids); ok {
					f.DirMode = 0o700
					it.Files = append(it.Files, f)
				}
			}
			if f, ok := s.file(ssh+"/known_hosts", ssh+"/known_hosts", HowLines, ids, gids); ok {
				f.DirMode = 0o700
				it.Files = append(it.Files, f)
			}
			out = append(out, it)
		}
		if f, ok := s.file(ssh+"/authorized_keys", ssh+"/authorized_keys", HowLines, ids, gids); ok && len(f.Lines) > 0 {
			f.DirMode = 0o700
			n := len(f.Lines)
			out = append(out, Item{Key: "zugang:login:" + h, Kind: KindAccess, Users: []string{user}, Files: []File{f},
				Title:  "Login-Schlüssel für " + user + " (" + strconv.Itoa(n) + ")",
				Detail: "Wer sich bisher mit Schlüssel anmeldet, kann das dann auch auf dem neuen Server. Fehlende Zeilen kommen dazu, vorhandene bleiben."})
		}
		if f, ok := s.file(h+"/.docker/config.json", h+"/.docker/config.json", HowDocker, ids, gids); ok && len(f.Names) > 0 {
			f.DirMode = 0o700
			it := Item{Key: "zugang:docker:" + h, Kind: KindAccess, Users: []string{user}, Files: []File{f},
				Title:  "Docker-Anmeldung von " + user + ": " + registryNames(f.Names),
				Detail: "Damit lädt Docker private Images, etwa von ghcr.io. Fehlende Registries kommen dazu, vorhandene bleiben."}
			if dockerHelper(f.Data) {
				// Ohne die Zugangsdaten in der Datei hielte sich der neue Server
				// für angemeldet, ohne es zu sein.
				it.Manual = "Die Zugangsdaten liegen in einem Schlüsselspeicher (credsStore/credHelpers), nicht in der Datei; melde dich auf dem neuen Server mit docker login neu an"
				it.Files = nil
			}
			out = append(out, it)
		}
	}
	return out
}

// registryNames macht aus den Schlüsseln in config.json lesbare Namen.
func registryNames(keys []string) string {
	seen := map[string]bool{}
	var out []string
	for _, r := range keys {
		r = strings.TrimPrefix(strings.TrimPrefix(r, "https://"), "http://")
		r, _, _ = strings.Cut(r, "/")
		if r == "index.docker.io" || r == "registry-1.docker.io" || r == "docker.io" {
			r = "Docker Hub"
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return strings.Join(out, ", ")
}

// privateKeys: Dateien in .ssh mit passender .pub-Datei oder mit dem Namen
// eines Standard-Schlüssels. Gelesen wird dabei keiner.
func (s Source) privateKeys(dir string) []string {
	es, err := os.ReadDir(s.path(dir))
	if err != nil {
		return nil
	}
	names := map[string]bool{}
	for _, e := range es {
		names[e.Name()] = true
	}
	var out []string
	for _, e := range es {
		n := e.Name()
		if !e.Type().IsRegular() || strings.HasSuffix(n, ".pub") {
			continue
		}
		if names[n+".pub"] || n == "id_rsa" || n == "id_ed25519" || n == "id_ecdsa" || n == "id_dsa" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// readIDs liest /etc/passwd oder /etc/group: ID → Name.
func readIDs(file string) map[uint32]string {
	out := map[uint32]string{}
	b, err := os.ReadFile(file)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 {
			continue
		}
		if id, err := strconv.ParseUint(f[2], 10, 32); err == nil {
			if _, seen := out[uint32(id)]; !seen {
				out[uint32(id)] = f[0]
			}
		}
	}
	return out
}
