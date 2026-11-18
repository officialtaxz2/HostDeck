package discover

import (
	"encoding/json"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/ort"
)

// Am Server selbst, außerhalb von Docker, liegt oft, was beim Umzug vergessen
// wird: Skripte und Schlüssel im Home-Ordner, Cronjobs, eigene Dienste und
// geänderte Einstellungen. host sucht nur an diesen bekannten Orten, damit
// kein Müll in die Auswahl kommt.
func (s *scan) hostScan() {
	s.dpkg = s.loadDpkg()
	s.homes()
	s.websites()
	s.programs()
	s.tasks()
	s.settings()
	s.checks()
	s.settingEntries()
}

// coveredPath meldet, ob p schon mit einem Eintrag gesichert wird.
func (s *scan) coveredPath(p string) bool {
	for _, e := range s.inv.Paths {
		if e.Kind != inventory.KindParent && e.CoveredBy == "" && hostfs.Under(p, e.Path) {
			return true
		}
	}
	for _, u := range s.inv.Unclaimed {
		if hostfs.Under(p, u.Path) {
			return true
		}
	}
	return false
}

// readRegular liest eine normale Datei; Geräte, Pipes und Symlinks nicht.
func (s *scan) readRegular(p string, limit int64) ([]byte, bool) {
	fi, err := s.d.FS.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	b, err := s.d.FS.ReadFile(p, limit)
	return b, err == nil
}

func (s *scan) isRegular(p string) bool {
	fi, err := s.d.FS.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

// hostdeckBinary erkennt HostDeck selbst, das oft im Home-Ordner liegt,
// auch unter dem früheren Namen sbm.
func hostdeckBinary(name string) bool {
	for _, n := range []string{"hostdeck", "sbm"} {
		if name == n || strings.HasPrefix(name, n+"-linux-") || strings.HasPrefix(name, n+"-darwin-") || (strings.HasPrefix(name, n+"-") && strings.Contains(name, "-linux-")) {
			return true
		}
	}
	return false
}

// homeCaches sind Unterordner von Home-Ordnern, die sich neu laden lassen.
var homeCaches = []string{
	".cache", ".npm", ".yarn/cache", ".pnpm-store", ".local/share/pnpm/store", ".bun/install/cache",
	".vscode-server", ".cursor-server", ".windsurf-server", ".local/share/Trash",
	".cargo/registry", ".cargo/git", ".rustup", ".gradle/caches", ".m2/repository", "go/pkg/mod",
	".nvm/.cache", ".local/share/containers",
}

func (s *scan) homeDirs() []string {
	var out []string
	if s.d.FS.IsDir("/root") {
		out = append(out, "/root")
	}
	if ents, err := s.d.FS.ReadDir("/home"); err == nil {
		for _, e := range ents {
			if e.IsDir() && e.Name() != "lost+found" {
				out = append(out, "/home/"+e.Name())
			}
		}
	}
	return out
}

func (s *scan) homes() {
	for _, h := range s.homeDirs() {
		if s.coveredPath(h) {
			continue
		}
		ents, err := s.d.FS.ReadDir(h)
		if err != nil || len(ents) == 0 {
			continue
		}
		u := inventory.Unclaimed{Key: "path:" + h, Path: h, Area: inventory.AreaHome, Size: inventory.Unknown}
		var notes, scripts, repos []string
		for _, e := range ents {
			p := h + "/" + e.Name()
			switch {
			case e.IsDir() && !strings.HasPrefix(e.Name(), ".") && s.d.FS.Exists(p+"/.git"):
				repos = append(repos, e.Name())
			case e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") && !hostdeckBinary(e.Name()):
				if fi, err := e.Info(); err == nil && (fi.Mode()&0o111 != 0 || hasScriptExt(e.Name())) {
					scripts = append(scripts, e.Name())
				}
			}
		}
		if n := s.authorizedKeys(h); n > 0 {
			notes = append(notes, plural(n, "Login-Schlüssel", "Login-Schlüssel"))
		}
		if keys := s.privateKeys(h); len(keys) > 0 {
			notes = append(notes, "privater SSH-Schlüssel "+strings.Join(keys, ", "))
		}
		if regs := s.dockerLogins(h); len(regs) > 0 {
			notes = append(notes, "docker login bei "+strings.Join(regs, ", "))
		}
		if len(scripts) > 0 {
			notes = append(notes, "Skripte: "+shortList(scripts, 6))
		}
		if len(repos) > 0 {
			notes = append(notes, "Git-Repos: "+shortList(repos, 6))
		}
		u.Note = strings.Join(notes, "; ")
		for _, c := range homeCaches {
			if s.d.FS.IsDir(h + "/" + c) {
				u.Caches = append(u.Caches, inventory.SubSize{Path: h + "/" + c, Size: inventory.Unknown})
			}
		}
		s.inv.Unclaimed = append(s.inv.Unclaimed, u)
	}
}

func hasScriptExt(name string) bool {
	for _, ext := range []string{".sh", ".py", ".pl", ".rb", ".js", ".bash"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// authorizedKeys zählt die Login-Schlüssel eines Home-Ordners.
func (s *scan) authorizedKeys(home string) int {
	n := 0
	for _, f := range []string{"/.ssh/authorized_keys", "/.ssh/authorized_keys2"} {
		b, ok := s.readRegular(home+f, 1<<20)
		if !ok {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				n++
			}
		}
	}
	return n
}

// privateKeys nennt private SSH-Schlüssel nur beim Namen: Dateien in .ssh,
// zu denen es eine .pub-Datei gibt, oder die wie Standard-Schlüssel heißen.
// Gelesen wird keiner.
func (s *scan) privateKeys(home string) []string {
	ents, err := s.d.FS.ReadDir(home + "/.ssh")
	if err != nil {
		return nil
	}
	names := map[string]bool{}
	for _, e := range ents {
		names[e.Name()] = true
	}
	var out []string
	for _, e := range ents {
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

// dockerLogins nennt die Registries, bei denen docker login eingerichtet
// ist. Aus config.json werden nur die Namen gelesen, nie die Zugangsdaten.
func (s *scan) dockerLogins(home string) []string {
	b, ok := s.readRegular(home+"/.docker/config.json", 1<<20)
	if !ok {
		return nil
	}
	var cfg struct {
		Auths       map[string]json.RawMessage `json:"auths"`
		CredHelpers map[string]string          `json:"credHelpers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	set := map[string]bool{}
	for r := range cfg.Auths {
		set[registryName(r)] = true
	}
	for r := range cfg.CredHelpers {
		set[registryName(r)] = true
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func registryName(r string) string {
	r = strings.TrimPrefix(strings.TrimPrefix(r, "https://"), "http://")
	r, _, _ = strings.Cut(r, "/")
	if r == "index.docker.io" || r == "registry-1.docker.io" {
		return "Docker Hub"
	}
	return r
}

// websites erfasst Webseiten in /var/www, außer der Standardseite von
// Apache oder nginx.
func (s *scan) websites() {
	ents, err := s.d.FS.ReadDir("/var/www")
	if err != nil {
		return
	}
	for _, e := range ents {
		p := "/var/www/" + e.Name()
		if !e.IsDir() || s.coveredPath(p) || s.defaultWebRoot(p) {
			continue
		}
		s.inv.Unclaimed = append(s.inv.Unclaimed, inventory.Unclaimed{Key: "path:" + p, Path: p, Area: inventory.AreaWeb, Size: inventory.Unknown})
	}
}

func (s *scan) defaultWebRoot(p string) bool {
	ents, err := s.d.FS.ReadDir(p)
	if err != nil {
		return true
	}
	for _, e := range ents {
		switch {
		case e.Name() == "index.nginx-debian.html":
		case e.Name() == "index.html" && s.sameFile(p+"/index.html", "/usr/share/apache2/default-site/index.html"):
		default:
			return false
		}
	}
	return true
}

func (s *scan) sameFile(a, b string) bool {
	ma, ok1 := s.fileMD5(a)
	mb, ok2 := s.fileMD5(b)
	return ok1 && ok2 && ma == mb
}

// programs erfasst selbst abgelegte Programme in /usr/local/bin und sbin.
// Was ein Paket dort installiert hat, zählt nicht.
func (s *scan) programs() {
	for _, dir := range []string{"/usr/local/bin", "/usr/local/sbin"} {
		ents, err := s.d.FS.ReadDir(dir)
		if err != nil || s.coveredPath(dir) {
			continue
		}
		var own, npm []string
		for _, e := range ents {
			p := dir + "/" + e.Name()
			if s.dpkg != nil && s.dpkg.owned[p] != "" {
				continue
			}
			if e.Type()&fs.ModeSymlink != 0 {
				t, err := s.d.FS.Readlink(p)
				if err == nil && strings.Contains(t, "node_modules") {
					npm = append(npm, e.Name())
					continue
				}
				// Die Verknüpfung von HostDeck selbst: kommt mit HostDeck auf jeden Server.
				if err == nil && strings.HasPrefix(t, ort.ProgDir+"/") {
					continue
				}
			}
			if e.IsDir() {
				continue
			}
			own = append(own, e.Name())
		}
		if len(own) == 0 && len(npm) == 0 {
			continue
		}
		var notes []string
		if len(own) > 0 {
			notes = append(notes, "eigene Programme: "+shortList(own, 8))
		}
		if len(npm) > 0 {
			notes = append(notes, "per npm installiert: "+shortList(npm, 8))
		}
		s.inv.Unclaimed = append(s.inv.Unclaimed, inventory.Unclaimed{Key: "path:" + dir, Path: dir, Area: inventory.AreaPrograms,
			Note: strings.Join(notes, "; "), Size: inventory.Unknown})
	}
}

// crontabDirs: hier legt crontab -e die Aufgaben der Benutzer ab (Debian
// und Ubuntu, sonst Red Hat).
var crontabDirs = []string{"/var/spool/cron/crontabs", "/var/spool/cron"}

// settingEntries legt Einträge für /etc und die Crontabs an. Sie werden
// archiviert: Systemeinstellungen gehören nicht blind auf ein neues System,
// sollen aber zum Nachschlagen im Backup liegen.
func (s *scan) settingEntries() {
	if s.d.FS.IsDir("/etc") && !s.coveredPath("/etc") {
		mod, add := 0, 0
		for _, st := range s.inv.Settings {
			if st.Change == inventory.ChangeModified {
				mod++
			} else {
				add++
			}
		}
		note := "Systemeinstellungen zum Nachschlagen, nicht 1:1 zurückspielen"
		if s.dpkg != nil {
			note += "; " + plural(mod, "geänderte Datei", "geänderte Dateien") + ", " + plural(add, "eigene Datei", "eigene Dateien")
		}
		s.inv.Unclaimed = append(s.inv.Unclaimed, inventory.Unclaimed{Key: "path:/etc", Path: "/etc", Area: inventory.AreaSettings, Note: note, Size: inventory.Unknown})
	}
	for _, dir := range crontabDirs {
		users := s.crontabUsers(dir)
		if len(users) == 0 || s.coveredPath(dir) {
			continue
		}
		s.inv.Unclaimed = append(s.inv.Unclaimed, inventory.Unclaimed{Key: "path:" + dir, Path: dir, Area: inventory.AreaSettings,
			Note: "Cronjobs von " + strings.Join(users, ", "), Size: inventory.Unknown})
		break
	}
}

func (s *scan) crontabUsers(dir string) []string {
	ents, err := s.d.FS.ReadDir(dir)
	if err != nil {
		return nil
	}
	var users []string
	for _, e := range ents {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
			users = append(users, e.Name())
		}
	}
	sort.Strings(users)
	return users
}

// relEtc gibt p relativ zu /etc zurück.
func relEtc(p string) string { return strings.TrimPrefix(p, "/etc/") }
